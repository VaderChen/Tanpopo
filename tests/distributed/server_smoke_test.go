//go:build darwin && arm64

package distributed_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"LlamaLoader/src/config"
	"LlamaLoader/src/domain"
)

type runningServer struct {
	command *exec.Cmd
	done    chan struct{}
	log     *os.File
}

type smokeServer struct {
	directory, binary, agent, baseURL string
	client                            *http.Client
	process                           *runningServer
	key                               string
	starts                            int
	groups                            []int
}

func TestTanpopoDistributedSmoke(t *testing.T) {
	if os.Getenv("TANPOPO_DISTRIBUTED_SMOKE") != "1" {
		t.Skip("設定 TANPOPO_DISTRIBUTED_SMOKE=1，執行原生 Tanpopo Server 雙節點 Smoke")
	}
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	mlxBinary := os.Getenv("TANPOPO_MLX_SERVER")
	if mlxBinary == "" {
		mlxBinary = filepath.Join(project, "mlx-runtime/prebuilt/darwin-arm64/bin/mlx-server")
	}
	mlxBinary, err := filepath.Abs(mlxBinary)
	must(t, err)
	if _, err := os.Stat(mlxBinary); err != nil {
		t.Fatal("請先執行 scripts/build-mlx-server-runtime.sh：", err)
	}
	directory := t.TempDir()
	server := &smokeServer{directory: directory, binary: filepath.Join(directory, "tanpopo-server"),
		agent: filepath.Join(directory, "agent.properties")}
	t.Cleanup(func() {
		server.stop(t)
		// 案例結束後才清理失敗測試的程序，避免替正常回收測試代勞。
		for _, group := range server.groups {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		}
		if t.Failed() {
			logs, _ := filepath.Glob(filepath.Join(directory, "*.log"))
			for _, name := range logs {
				data, _ := os.ReadFile(name)
				if len(data) > 12000 {
					data = data[len(data)-12000:]
				}
				t.Log(filepath.Base(name), string(data))
			}
		}
	})
	build := exec.Command("go", "build", "-buildvcs=false", "-ldflags", "-X LlamaLoader/src/appversion.Repository=", "-o", server.binary, "./src/cmd/llamaloader")
	build.Dir = project
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("建置 Tanpopo Server：%v\n%s", err, output)
	}
	// 讓正式 Runtime resolver 在獨立目錄找到相同原生成品與 Metal bundle。
	runtimeDirectory := filepath.Join(directory, "mlx-runtime/prebuilt/darwin-arm64")
	must(t, os.MkdirAll(runtimeDirectory, 0700))
	must(t, os.Symlink(filepath.Dir(mlxBinary), filepath.Join(runtimeDirectory, "bin")))
	models := filepath.Join(directory, "models")
	workers := filepath.Join(directory, "worker models")
	must(t, os.MkdirAll(models, 0700))
	must(t, os.MkdirAll(workers, 0700))
	settings := config.DefaultSettings()
	settings.ModelDirectory, settings.MLXModelDirectory = models, models
	// 固定微型 fixture 沿用預設關閉的 Go 啟動前壓力檢查，避免桌面其他工作
	// 改變流程測試結果；Runtime 的分片／請求預算仍生效。系統保留另有 Go 測試。
	settings.MemoryProtectionEnabled, settings.AutoCalibrationEnabled = false, false
	writeJSON(t, filepath.Join(directory, "settings.json"), settings)
	agent := config.DefaultAgentConfig()
	agent.HTTPHost, agent.HTTPPort = "127.0.0.1", freePorts(t, 1)[0]
	agent.WebPath = filepath.Join(project, "website")
	agent.SettingsPath = filepath.Join(directory, "settings.json")
	agent.StartupCommandsPath = filepath.Join(directory, "startup-commands.json")
	agent.AccessControlPath = filepath.Join(directory, "access-control.json")
	agent.RuntimeStatePath = filepath.Join(directory, "runtime-state.json")
	agent.DisableAuthentication = false
	agent.DefaultAccount, agent.DefaultPassword = "smoke", rand.Text()
	writeJSON(t, server.agent, agent)
	server.baseURL = fmt.Sprintf("http://127.0.0.1:%d", agent.HTTPPort)
	jar, err := cookiejar.New(nil)
	must(t, err)
	server.client = &http.Client{Jar: jar, Timeout: 90 * time.Second, Transport: &http.Transport{}}
	defer server.client.CloseIdleConnections()
	server.start(t)
	server.request(t, "GET", "/api/runtime/status", nil, 401, false)
	server.request(t, "POST", "/api/login", map[string]any{"account": agent.DefaultAccount, "password": agent.DefaultPassword, "remember_me": true}, 200, false)
	issued := server.request(t, "POST", "/api/access-control/keys", map[string]any{"name": "原生 Smoke 暫時金鑰"}, 201, false)
	var key struct{ Key string }
	must(t, json.Unmarshal(issued, &key))
	server.key = key.Key
	server.request(t, "PUT", "/api/access-control", map[string]any{"api_key_enabled": true, "ip_allowlist_enabled": true, "ip_allowlist": []string{"127.0.0.1"}}, 200, false)

	// 同一套 native worker 啟動方式先跑底層 collective／量化數值測試。
	transportConfig := filepath.Join(directory, "transport.json")
	writeCluster(t, transportConfig, "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	smoke := exec.CommandContext(ctx, mlxBinary, "--distributed-config", transportConfig, "--distributed-smoke")
	output, err := smoke.CombinedOutput()
	cancel()
	if err != nil {
		t.Fatalf("原生雙程序通訊：%v\n%s", err, output)
	}
	t.Log("原生程序自動啟停、collective、FP32／FP16／BF16、一般／Q4 線性層通過")

	for _, architecture := range []string{"llama", "llama_q4", "llama_q8", "mistral", "phi3", "gemma2", "starcoder2", "qwen2", "qwen3", "qwen3_moe", "qwen3_5", "qwen3_5_text"} {
		if !t.Run(architecture, func(t *testing.T) {
			writeFixture(t, filepath.Join(models, architecture), architecture)
			writeFixture(t, filepath.Join(workers, architecture), architecture)
			cluster := filepath.Join(directory, architecture+"-cluster.json")
			writeCluster(t, cluster, filepath.Join(workers, architecture))
			apiPort := freePorts(t, 1)[0]
			single := server.profile(t, architecture+"-single", apiPort, "")
			distributed := server.profile(t, architecture+"-distributed", apiPort, cluster)
			server.startModel(t, architecture, single.ID)
			prompts := []string{"hello", strings.Repeat("hello", 50)}
			expected := make([]completion, len(prompts))
			for index, prompt := range prompts {
				expected[index] = server.chat(t, prompt)
			}
			server.stopModel(t)
			status := server.startModel(t, architecture, distributed.ID)
			workerPID := server.workerPID(t)
			if workerPID == status.PID {
				t.Fatal("worker 與主節點不是獨立程序")
			}
			var health struct {
				Distributed struct {
					WorldSize     int   `json:"world_size"`
					Layers        int   `json:"sharded_layers"`
					LocalBytes    int64 `json:"local_linear_bytes"`
					OriginalBytes int64 `json:"original_linear_bytes"`
					ResidentBytes int64 `json:"coordinator_replicated_bytes"`
				}
			}
			getJSON(t, status.URL+"/health", &health)
			if health.Distributed.WorldSize != 2 || health.Distributed.Layers == 0 || health.Distributed.LocalBytes >= health.Distributed.OriginalBytes {
				t.Fatalf("未建立權重分片雙節點：%+v", health)
			}
			if architecture == "qwen3_moe" && health.Distributed.ResidentBytes < 4*3*128*64*4 {
				t.Fatal("MoE 專家權重未保留在主節點")
			}
			for _, stream := range []bool{false, true} {
				server.request(t, "POST", "/api/chat/completions", chatBody("hello", 4, stream), 401, false)
			}
			for index, prompt := range prompts {
				actual := server.chat(t, prompt)
				if actual != expected[index] {
					t.Fatalf("單機／雙節點代理結果不同：%+v / %+v", expected[index], actual)
				}
			}
			if architecture == "llama" {
				server.concurrency(t)
			}
			stream, cancelStream := server.stream(t, "hello", 8)
			verifyStream(t, stream)
			cancelStream()
			server.stopModel(t)
			waitGone(t, status.PID, workerPID)
			server.request(t, "POST", "/api/chat/completions", chatBody("hello", 4, false), 409, true)
			t.Log("管理登入、Profile、單／雙節點結果與 Token 數、長 Prefill、金鑰、SSE、停止回收通過")
			if architecture == "llama" {
				server.lifecycle(t, architecture, distributed.ID, agent.RuntimeStatePath)
				server.startupFailures(t, architecture, apiPort)
			}
		}) {
			return
		}
	}
	server.stop(t)
	t.Log("Tanpopo Server 原生雙節點 Smoke 全部通過；全程 Go／Swift，未呼叫 Python")
}

func (s *smokeServer) start(t *testing.T) {
	t.Helper()
	s.starts++
	log, err := os.Create(filepath.Join(s.directory, fmt.Sprintf("server-%d.log", s.starts)))
	must(t, err)
	command := exec.Command(s.binary, "-config", s.agent)
	command.Dir = s.directory
	command.Env = append(os.Environ(), "TANPOPO_UI=shell")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout, command.Stderr = log, log
	must(t, command.Start())
	s.groups = append(s.groups, command.Process.Pid)
	p := &runningServer{command: command, done: make(chan struct{}), log: log}
	s.process = p
	go func() { _ = command.Wait(); close(p.done) }()
	waitFor(t, 30*time.Second, "Tanpopo Server 就緒", func() bool {
		response, err := s.client.Get(s.baseURL + "/api/health")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200
	})
}

func (s *smokeServer) stop(t *testing.T) {
	t.Helper()
	if s.process == nil {
		return
	}
	p := s.process
	_ = p.command.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
		_ = syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL)
		<-p.done
		t.Error("Tanpopo Server 正常關閉逾時")
	}
	_ = p.log.Close()
	s.process = nil
}

func (s *smokeServer) request(t *testing.T, method, path string, body any, want int, withKey bool) []byte {
	t.Helper()
	data, err := json.Marshal(body)
	must(t, err)
	request, err := http.NewRequest(method, s.baseURL+path, bytes.NewReader(data))
	must(t, err)
	request.Header.Set("Content-Type", "application/json")
	if withKey {
		request.Header.Set("X-Tanpopo-Key", s.key)
	}
	response, err := s.client.Do(request)
	must(t, err)
	defer response.Body.Close()
	result, err := io.ReadAll(response.Body)
	must(t, err)
	if response.StatusCode != want {
		t.Fatalf("%s %s：HTTP %d，預期 %d：%s", method, path, response.StatusCode, want, result)
	}
	return result
}

func (s *smokeServer) profile(t *testing.T, name string, port int, cluster string) domain.StartupCommand {
	t.Helper()
	args := []string{"--temperature", "0", "--prefill-step-size", "8"}
	if cluster != "" {
		args = append(args, "--distributed-config", cluster)
	}
	data := s.request(t, "POST", "/api/startup-commands", map[string]any{
		"name": name, "runtime": "mlx-server", "server_host": "127.0.0.1", "server_port": port,
		"context_size": 4096, "gpu_layers": -1, "extra_args": args,
	}, 201, false)
	var profile domain.StartupCommand
	must(t, json.Unmarshal(data, &profile))
	return profile
}

func (s *smokeServer) status(t *testing.T) domain.LlamaStatus {
	t.Helper()
	var status domain.LlamaStatus
	must(t, json.Unmarshal(s.request(t, "GET", "/api/runtime/status", nil, 200, false), &status))
	return status
}

func (s *smokeServer) ready(t *testing.T) domain.LlamaStatus {
	t.Helper()
	var status domain.LlamaStatus
	waitFor(t, 90*time.Second, "受管 Runtime 就緒", func() bool {
		status = s.status(t)
		if !status.Running {
			t.Fatalf("Runtime 提早停止：%s\n%s", status.LastError, s.request(t, "GET", "/api/runtime/logs", nil, 200, false))
		}
		return status.Ready
	})
	return status
}

func (s *smokeServer) startModel(t *testing.T, model, profile string) domain.LlamaStatus {
	t.Helper()
	s.request(t, "DELETE", "/api/runtime/logs", nil, 200, false)
	s.request(t, "POST", "/api/runtime/start", map[string]any{"model": model, "startup_command_id": profile, "skip_saved_calibration": true}, 202, false)
	return s.ready(t)
}

func (s *smokeServer) stopModel(t *testing.T) {
	t.Helper()
	s.request(t, "POST", "/api/runtime/stop", nil, 200, false)
	status := s.status(t)
	if status.Running || status.Ready || status.DesiredRunning {
		t.Fatalf("停止狀態未清除：%+v", status)
	}
}

type completion struct {
	Content      string `json:"content"`
	Reasoning    string `json:"reasoning"`
	FinishReason string `json:"finish_reason"`
	Usage        struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
		Total      int `json:"total_tokens"`
	} `json:"usage"`
}

func chatBody(prompt string, tokens int, stream bool) map[string]any {
	return map[string]any{"messages": []map[string]string{{"role": "user", "content": prompt}}, "max_tokens": tokens, "stream": stream}
}

func (s *smokeServer) chat(t *testing.T, prompt string) completion {
	t.Helper()
	var result completion
	must(t, json.Unmarshal(s.request(t, "POST", "/api/chat/completions", chatBody(prompt, 12, false), 200, true), &result))
	return result
}

func (s *smokeServer) stream(t *testing.T, prompt string, tokens int) (*http.Response, context.CancelFunc) {
	t.Helper()
	data, err := json.Marshal(chatBody(prompt, tokens, true))
	must(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, "POST", s.baseURL+"/api/chat/completions", bytes.NewReader(data))
	must(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Tanpopo-Key", s.key)
	response, err := s.client.Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		defer response.Body.Close()
		cancel()
		t.Fatalf("SSE HTTP %d", response.StatusCode)
	}
	t.Cleanup(func() { cancel(); response.Body.Close() })
	return response, cancel
}

func verifyStream(t *testing.T, response *http.Response) {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	must(t, err)
	if !bytes.Contains(data, []byte("[DONE]")) || bytes.Contains(data, []byte(`"error"`)) {
		t.Fatalf("SSE 未正常完成：%s", data)
	}
}

func (s *smokeServer) concurrency(t *testing.T) {
	t.Helper()
	responses := make([]*http.Response, 4)
	cancels := make([]context.CancelFunc, 4)
	for index := range responses {
		responses[index], cancels[index] = s.stream(t, strings.Repeat("hello", 300), 128)
	}
	for _, stream := range []bool{false, true} {
		s.request(t, "POST", "/api/chat/completions", chatBody("hello", 1, stream), 429, true)
	}
	for index := 0; index < 2; index++ {
		cancels[index]()
		responses[index].Body.Close()
	}
	for index := 2; index < 4; index++ {
		verifyStream(t, responses[index])
		cancels[index]()
	}
	s.chat(t, "hello")
	t.Log("經 Go 代理四人並行、一般／SSE 第五人 429、取消隔離與名額回收通過")
}

func (s *smokeServer) workerPID(t *testing.T) int {
	t.Helper()
	data := s.request(t, "GET", "/api/runtime/logs", nil, 200, false)
	match := regexp.MustCompile(`TANPOPO_DISTRIBUTED_WORKER launch=local pid=(\d+)`).FindSubmatch(data)
	if len(match) != 2 {
		t.Fatalf("未記錄本機 worker：%s", data)
	}
	pid, err := strconv.Atoi(string(match[1]))
	must(t, err)
	return pid
}

func (s *smokeServer) lifecycle(t *testing.T, model, profile, statePath string) {
	t.Helper()
	status := s.startModel(t, model, profile)
	worker := s.workerPID(t)
	must(t, syscall.Kill(worker, syscall.SIGKILL))
	waitFor(t, 20*time.Second, "worker 故障傳回 Server", func() bool { return !s.status(t).Running })
	if status := s.status(t); status.Ready || status.LastError == "" {
		t.Fatalf("故障未反映於狀態：%+v", status)
	}
	waitGone(t, status.PID, worker)
	s.request(t, "GET", "/api/health", nil, 200, false)
	s.request(t, "POST", "/api/chat/completions", chatBody("hello", 4, false), 409, true)
	status = s.startModel(t, model, profile)
	worker = s.workerPID(t)
	s.chat(t, "hello")
	// 主節點遭強制結束時，worker 必須自行透過父端 EOF 收尾。
	must(t, syscall.Kill(status.PID, syscall.SIGKILL))
	waitFor(t, 20*time.Second, "主節點故障傳回 Server", func() bool { return !s.status(t).Running })
	waitGone(t, status.PID, worker)
	status = s.startModel(t, model, profile)
	worker = s.workerPID(t)
	s.chat(t, "hello")
	// 真正關閉 Go Server；它應停止兩個 Rank，並保存正常服務的恢復意圖。
	s.stop(t)
	waitGone(t, status.PID, worker)
	state, err := os.ReadFile(statePath)
	must(t, err)
	var desired struct {
		Desired bool `json:"desired_running"`
	}
	must(t, json.Unmarshal(state, &desired))
	if !desired.Desired {
		t.Fatal("Server 關閉遺失自動恢復旗標")
	}
	s.start(t)
	status = s.ready(t)
	worker = s.workerPID(t)
	s.chat(t, "hello")
	s.stopModel(t)
	waitGone(t, status.PID, worker)
	t.Log("worker／主節點故障回報、父端 EOF、重新啟動、Server 關閉雙節點回收與重開自動恢復通過")
}

func writeCluster(t *testing.T, name, model string) {
	t.Helper()
	ports := freePorts(t, 2)
	worker := map[string]any{"ringAddress": fmt.Sprintf("127.0.0.1:%d", ports[1]), "launch": "local"}
	if model != "" {
		worker["modelPath"] = model
	}
	writeJSON(t, name, map[string]any{"version": 1, "backend": "ring", "operationTimeoutSeconds": 10, "startupTimeoutSeconds": 60,
		"nodes": []any{map[string]any{"ringAddress": fmt.Sprintf("127.0.0.1:%d", ports[0])}, worker}})
}

func (s *smokeServer) startupFailures(t *testing.T, model string, apiPort int) {
	t.Helper()
	for _, scenario := range []string{"missing", "mismatch"} {
		cluster := filepath.Join(s.directory, scenario+"-cluster.json")
		if scenario == "missing" {
			ports := freePorts(t, 2)
			writeJSON(t, cluster, map[string]any{"version": 1, "backend": "ring", "startupTimeoutSeconds": 10,
				"nodes": []any{map[string]any{"ringAddress": fmt.Sprintf("127.0.0.1:%d", ports[0])},
					map[string]any{"ringAddress": fmt.Sprintf("127.0.0.1:%d", ports[1])}}})
		} else {
			different := filepath.Join(s.directory, "different-model")
			writeFixture(t, different, "qwen2")
			writeCluster(t, cluster, different)
		}
		profile := s.profile(t, scenario, apiPort, cluster)
		s.request(t, "DELETE", "/api/runtime/logs", nil, 200, false)
		s.request(t, "POST", "/api/runtime/start", map[string]any{"model": model, "startup_command_id": profile.ID}, 202, false)
		waitFor(t, 20*time.Second, scenario+" 啟動失敗回報", func() bool {
			status := s.status(t)
			if status.Ready {
				t.Fatal("缺少節點或模型不一致仍回報就緒")
			}
			return !status.Running
		})
		if s.status(t).LastError == "" {
			t.Fatal("啟動失敗未留下錯誤")
		}
		if scenario == "mismatch" {
			logs := s.request(t, "GET", "/api/runtime/logs", nil, 200, false)
			if !bytes.Contains(logs, []byte("Runtime 版本與模型內容不一致")) {
				t.Fatalf("模型不一致未被核對：%s", logs)
			}
			waitGone(t, s.workerPID(t))
		}
		s.stopModel(t)
	}
	t.Log("經 Server 啟動的缺少節點逾時、模型內容不一致與失敗狀態回報通過")
}

func freePorts(t *testing.T, count int) []int {
	t.Helper()
	ports := make([]int, count)
	var listeners []net.Listener
	defer func() {
		for _, listener := range listeners {
			listener.Close()
		}
	}()
	for index := range ports {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		must(t, err)
		listeners = append(listeners, listener)
		ports[index] = listener.Addr().(*net.TCPAddr).Port
	}
	return ports
}

func waitFor(t *testing.T, timeout time.Duration, label string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal(label + "逾時")
}

func waitGone(t *testing.T, pids ...int) {
	t.Helper()
	waitFor(t, 12*time.Second, "程序回收", func() bool {
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				return false
			}
		}
		return true
	})
}

func getJSON(t *testing.T, endpoint string, result any) {
	t.Helper()
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	response, err := client.Get(endpoint)
	must(t, err)
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("%s：HTTP %d", endpoint, response.StatusCode)
	}
	must(t, json.NewDecoder(response.Body).Decode(result))
}
