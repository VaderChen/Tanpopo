//go:build darwin && arm64

package distributed_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"LlamaLoader/src/cluster"
	"LlamaLoader/src/config"
)

// 三個真實 Go Server，各自管理一個 Swift Runtime；不使用 SSH 或 Python。
func TestTanpopoDiscoveredRingSmoke(t *testing.T) {
	if os.Getenv("TANPOPO_DISTRIBUTED_SMOKE") != "1" {
		t.Skip("設定 TANPOPO_DISTRIBUTED_SMOKE=1 執行 Server UDP 探索與一鍵 TCP Ring Smoke")
	}
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	directory := t.TempDir()
	binary := filepath.Join(directory, "tanpopo-server")
	build := exec.Command("go", "build", "-buildvcs=false", "-ldflags", "-X LlamaLoader/src/appversion.Repository=", "-o", binary, "./src/cmd/llamaloader")
	build.Dir = project
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("建置 Server：%v\n%s", err, output)
	}
	mlxBinary := os.Getenv("TANPOPO_MLX_SERVER")
	if mlxBinary == "" {
		mlxBinary = filepath.Join(project, "mlx-runtime/prebuilt/darwin-arm64/bin/mlx-server")
	}
	var err error
	mlxBinary, err = filepath.Abs(mlxBinary)
	must(t, err)
	runtimeDirectory := filepath.Join(directory, "mlx-runtime/prebuilt/darwin-arm64")
	must(t, os.MkdirAll(runtimeDirectory, 0700))
	must(t, os.Symlink(filepath.Dir(mlxBinary), filepath.Join(runtimeDirectory, "bin")))
	servers := make([]*smokeServer, 3)
	for i := range servers {
		nodeDirectory := filepath.Join(directory, fmt.Sprintf("node-%d", i))
		must(t, os.MkdirAll(nodeDirectory, 0700))
		s := &smokeServer{directory: nodeDirectory, binary: binary, agent: filepath.Join(nodeDirectory, "agent.properties")}
		servers[i] = s
		models := filepath.Join(nodeDirectory, "models")
		must(t, os.MkdirAll(models, 0700))
		writeFixture(t, filepath.Join(models, "shared-model"), "llama")
		settings := config.DefaultSettings()
		settings.ModelDirectory, settings.MLXModelDirectory = models, models
		settings.MemoryProtectionEnabled, settings.AutoCalibrationEnabled = false, false
		writeJSON(t, filepath.Join(nodeDirectory, "settings.json"), settings)
		agent := config.DefaultAgentConfig()
		agent.HTTPHost, agent.HTTPPort = "127.0.0.1", freePorts(t, 1)[0]
		agent.WebPath = filepath.Join(project, "website")
		agent.SettingsPath = filepath.Join(nodeDirectory, "settings.json")
		agent.StartupCommandsPath = filepath.Join(nodeDirectory, "startup-commands.json")
		agent.AccessControlPath = filepath.Join(nodeDirectory, "access-control.json")
		agent.RuntimeStatePath = filepath.Join(nodeDirectory, "runtime-state.json")
		agent.DisableAuthentication = false
		agent.DefaultAccount, agent.DefaultPassword = "smoke", rand.Text()
		writeJSON(t, s.agent, agent)
		s.baseURL = fmt.Sprintf("http://127.0.0.1:%d", agent.HTTPPort)
		jar, err := cookiejar.New(nil)
		must(t, err)
		s.client = &http.Client{Jar: jar, Timeout: 90 * time.Second, Transport: &http.Transport{}}
		t.Cleanup(func() {
			s.stop(t)
			for _, group := range s.groups {
				_ = syscall.Kill(-group, syscall.SIGKILL)
			}
			s.client.CloseIdleConnections()
			if t.Failed() {
				logs, _ := filepath.Glob(filepath.Join(s.directory, "*.log"))
				for _, log := range logs {
					data, _ := os.ReadFile(log)
					t.Log(string(data))
				}
			}
		})
		s.start(t)
		s.request(t, "GET", "/api/cluster/status", nil, 401, false)
		s.request(t, "POST", "/api/login", map[string]any{"account": agent.DefaultAccount, "password": agent.DefaultPassword, "remember_me": true}, 200, false)
		s.request(t, "POST", "/api/cluster/control", map[string]any{"kind": "prepare"}, 401, false)
	}
	a, b, c := servers[0], servers[1], servers[2]
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	must(t, err)
	port := udp.LocalAddr().(*net.UDPAddr).Port
	_ = udp.Close()
	interfaces, err := net.Interfaces()
	must(t, err)
	ifaceName := ""
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			ifaceName = iface.Name
			break
		}
	}
	if ifaceName == "" {
		t.Fatal("找不到 Loopback 介面")
	}
	for _, server := range servers {
		data := server.request(t, "PUT", "/api/cluster/config", map[string]any{"enabled": true, "discovery_port": port, "interface": ifaceName}, 200, false)
		if strings.Contains(string(data), "pairing_key") {
			t.Fatal("探索不應產生配對金鑰")
		}
	}
	peerID := b.clusterStatus(t).Local.ID
	peerIDs := []string{peerID, c.clusterStatus(t).Local.ID}
	awaitDiscovered := func() {
		waitFor(t, 15*time.Second, "三台 Server 免金鑰互相發現", func() bool {
			for _, server := range servers {
				status := server.clusterStatus(t)
				if len(status.Peers) != len(servers)-1 {
					return false
				}
				for _, peer := range status.Peers {
					if peer.Busy {
						return false
					}
				}
			}
			return true
		})
	}

	awaitDiscovered()
	profile := a.profile(t, "discovery-smoke", freePorts(t, 1)[0], "")
	workerProfile := b.profile(t, "worker-single", freePorts(t, 1)[0], "")
	a.startModel(t, "shared-model", profile.ID)
	expected := a.chat(t, "hello")
	a.stopModel(t)
	// 模型設定相同、權重不同；另一路徑已存在相同內容。
	weightPath := filepath.Join(b.directory, "models/shared-model/model.safetensors")
	changedWeights, err := os.ReadFile(weightPath)
	must(t, err)
	changedWeights[len(changedWeights)-1] ^= 1
	must(t, os.WriteFile(weightPath, changedWeights, 0600))
	must(t, os.Rename(filepath.Join(c.directory, "models/shared-model"), filepath.Join(c.directory, "models/renamed-model")))
	awaitDiscovered()
	start := func() (int, int, int) {
		awaitDiscovered()
		a.request(t, "POST", "/api/cluster/start", map[string]any{"peer_ids": peerIDs, "model": "shared-model", "startup_command_id": profile.ID}, 202, false)
		waitFor(t, 90*time.Second, "背景同步完成並啟動主節點", func() bool {
			status := a.clusterStatus(t)
			if status.Session == nil {
				t.Fatalf("背景同步失敗：%s", status.LastError)
			}
			return a.status(t).Running
		})
		root := a.ready(t)
		worker := b.status(t)
		if !worker.Running || worker.PID == root.PID || worker.Ready || worker.URL != "" {
			t.Fatalf("工作節點狀態錯誤：%+v", worker)
		}
		var health struct {
			Distributed struct {
				WorldSize int `json:"world_size"`
			} `json:"distributed"`
		}
		getJSON(t, root.URL+"/health", &health)
		if health.Distributed.WorldSize != 3 {
			t.Fatal("未啟動三個 Rank")
		}
		if actual := a.chat(t, "hello"); actual != expected {
			t.Fatalf("多選三節點與單機結果不同：%+v / %+v", actual, expected)
		}
		third := c.status(t)
		if !third.Running || third.Ready || third.PID == worker.PID {
			t.Fatal("第三個 Rank 未啟動")
		}
		return root.PID, worker.PID, third.PID
	}
	rootPID, workerPID, thirdPID := start()
	if !strings.HasPrefix(b.status(t).Model, "cluster-models/") || c.status(t).Model != "renamed-model" {
		t.Fatal("不同內容未下載、或同內容不同路徑未重用")
	}
	preserved, err := os.ReadFile(weightPath)
	must(t, err)
	if !bytes.Equal(preserved, changedWeights) {
		t.Fatal("原模型被覆寫")
	}
	stream, cancelStream := a.stream(t, "hello", 8)
	verifyStream(t, stream)
	cancelStream()
	b.request(t, "POST", "/api/runtime/start", map[string]any{"model": "shared-model", "startup_command_id": workerProfile.ID}, 400, false)
	b.request(t, "PUT", "/api/cluster/config", map[string]any{"enabled": false}, 400, false)
	// 原本的「停止服務」也必須停止雙端。
	a.stopModel(t)
	waitGone(t, rootPID, workerPID, thirdPID)
	if b.clusterStatus(t).Session != nil || b.status(t).Running || c.clusterStatus(t).Session != nil || c.status(t).Running {
		t.Fatal("一般停止 API 未清理對端")
	}
	t.Log("真實 UDP 免金鑰探索、複選三節點交握、單／三機推論一致、SSE 與整組停止通過")
	// 缺少整份模型時也須自動下載，不要求使用者手動安裝。
	must(t, os.RemoveAll(filepath.Join(c.directory, "models/renamed-model")))
	awaitDiscovered()
	rootPID, workerPID, thirdPID = start()
	// 強制關閉工作 Server，其 stdin EOF 必須終止原生 worker，租約再回收主節點。
	must(t, b.process.command.Process.Kill())
	<-b.process.done
	_ = b.process.log.Close()
	b.process = nil
	waitFor(t, 40*time.Second, "工作 Server 故障後主節點釋放租約", func() bool {
		return a.clusterStatus(t).Session == nil && !a.status(t).Running && c.clusterStatus(t).Session == nil && !c.status(t).Running
	})
	waitGone(t, rootPID, workerPID, thirdPID)
	b.start(t)
	if b.status(t).Running {
		t.Fatal("探索租約不可在重開 Server 後以過期位址自動恢復")
	}
	if b.clusterStatus(t).Local.ID != peerID {
		t.Fatal("Server 重啟改變節點身分")
	}
	rootPID, workerPID, thirdPID = start()
	// 從 worker 介面停止亦沿用同一個生命週期。
	b.request(t, "POST", "/api/cluster/stop", nil, 200, false)
	waitGone(t, rootPID, workerPID, thirdPID)
	rootPID, workerPID, thirdPID = start()
	// 主 Server 的 EOF 同樣必須回收 Rank 0，不能留下孤立的 API 程序。
	rootNodeID := a.clusterStatus(t).Local.ID
	must(t, a.process.command.Process.Kill())
	<-a.process.done
	_ = a.process.log.Close()
	a.process = nil
	waitFor(t, 40*time.Second, "主 Server 故障後工作節點釋放租約", func() bool {
		return b.clusterStatus(t).Session == nil && !b.status(t).Running && c.clusterStatus(t).Session == nil && !c.status(t).Running
	})
	waitGone(t, rootPID, workerPID, thirdPID)
	a.start(t)
	if a.status(t).Running || a.clusterStatus(t).Local.ID != rootNodeID {
		t.Fatal("主 Server 重啟應保留節點身分並等待重新握手")
	}
	awaitDiscovered()
	for _, s := range servers {
		data := s.request(t, "GET", "/api/cluster/status", nil, 200, false)
		if strings.Contains(string(data), "pairing_key") {
			t.Fatal("狀態 API 不應包含配對金鑰")
		}
	}
	t.Log("模型內容同步、缺少時下載、獨立 Server 強制結束、父端 EOF、租約回收、重啟重新探索與 worker 停止通過")
}

func (s *smokeServer) clusterStatus(t *testing.T) cluster.Status {
	t.Helper()
	var status cluster.Status
	must(t, json.Unmarshal(s.request(t, "GET", "/api/cluster/status", nil, 200, false), &status))
	return status
}

// 三份無法整除 8 列權重，驗證補齊／去除通訊暫存列不影響一般與量化運算。
func TestNativeThreeRankCollectivesSmoke(t *testing.T) {
	if os.Getenv("TANPOPO_DISTRIBUTED_SMOKE") != "1" {
		t.Skip("需明確啟用原生分散式 Smoke")
	}
	_, file, _, _ := runtime.Caller(0)
	binary := os.Getenv("TANPOPO_MLX_SERVER")
	if binary == "" {
		binary = filepath.Join(filepath.Dir(file), "../../mlx-runtime/prebuilt/darwin-arm64/bin/mlx-server")
	}
	directory := t.TempDir()
	ports := freePorts(t, 3)
	nodes := make([]map[string]string, 3)
	for i, port := range ports {
		nodes[i] = map[string]string{"launch": "manual", "ringAddress": fmt.Sprintf("127.0.0.1:%d", port)}
	}
	configuration := filepath.Join(directory, "ring.json")
	writeJSON(t, configuration, map[string]any{"version": 1, "backend": "ring", "nodes": nodes, "startupTimeoutSeconds": 30, "operationTimeoutSeconds": 15})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	commands, outputs := make([]*exec.Cmd, 3), make([]bytes.Buffer, 3)
	for i := range commands {
		commands[i] = exec.CommandContext(ctx, binary, "--distributed-config", configuration, "--distributed-rank", fmt.Sprint(i), "--distributed-smoke")
		commands[i].Stdout, commands[i].Stderr = &outputs[i], &outputs[i]
		must(t, commands[i].Start())
		t.Cleanup(func() { _ = commands[i].Process.Kill() })
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("Rank %d：%v\n%s", i, err, outputs[i].String())
		}
		t.Logf("Rank %d：%s", i, outputs[i].String())
	}
}
