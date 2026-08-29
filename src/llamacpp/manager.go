package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"LlamaLoader/src/domain"
	"LlamaLoader/src/download"
)

const mlxGGUFPathPrefix = "gguf:"

type SettingsProvider func() domain.Settings

type Manager struct {
	mu                sync.Mutex
	settings          SettingsProvider
	accessControlPath string
	stateStore        *runtimeStateStore
	cmd               *exec.Cmd
	done              chan struct{}
	stopping          bool
	status            domain.LlamaStatus
	logs              *logBuffer
}

func NewManager(settings SettingsProvider, accessControlPath, runtimeStatePath string) (*Manager, error) {
	accessControlPath = strings.TrimSpace(accessControlPath)
	if absolute, err := filepath.Abs(accessControlPath); err == nil {
		accessControlPath = absolute
	}
	stateStore, err := newRuntimeStateStore(runtimeStatePath)
	if err != nil {
		return nil, err
	}
	saved := stateStore.Get()
	manager := &Manager{
		settings:          settings,
		accessControlPath: accessControlPath,
		stateStore:        stateStore,
		logs:              newLogBuffer(128 * 1024),
		status: domain.LlamaStatus{
			DesiredRunning:     saved.DesiredRunning,
			Runtime:            saved.Runtime,
			Model:              saved.Model,
			MMProj:             saved.MMProj,
			DraftModel:         saved.DraftModel,
			DFlashEnabled:      saved.DFlashEnabled,
			StartupCommandID:   saved.StartupCommandID,
			StartupCommandName: saved.StartupCommandName,
		},
	}
	manager.refreshURL()
	return manager, nil
}

func (m *Manager) Start(model, mmproj, draftModel string, dflashEnabled bool, startupCommand domain.StartupCommand) (domain.LlamaStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Running {
		return m.status, errors.New("模型服務已在執行中；請先停止目前模型")
	}
	if dflashEnabled && startupCommand.Runtime == domain.RuntimeMLXServer {
		startupCommand.ExtraArgs = withoutDraftModelArguments(startupCommand.ExtraArgs)
	} else {
		startupCommand.ExtraArgs = withoutDFlashArguments(startupCommand.ExtraArgs)
	}
	startupCommand.DraftModel = ""
	if dflashEnabled {
		draftModel = strings.TrimSpace(draftModel)
		if draftModel == "" {
			return m.status, errors.New("找不到可用的 DFlash Draft 模型，請先到「模型下載」取得配對的 Draft")
		}
		startupCommand.DraftModel = draftModel
		if startupCommand.Runtime != domain.RuntimeMLXServer {
			startupCommand.ExtraArgs = append(startupCommand.ExtraArgs, "--spec-type", "draft-dflash")
		}
	}
	settings := m.settings()
	var status domain.LlamaStatus
	var err error
	if startupCommand.Runtime == domain.RuntimeMLXServer {
		status, err = m.startMLXLocked(settings, model, mmproj, startupCommand)
	} else {
		status, err = m.startLlamaLocked(settings, model, mmproj, startupCommand)
	}
	if err != nil {
		return status, err
	}
	m.status.DesiredRunning = true
	m.status.DFlashEnabled = dflashEnabled
	if err := m.persistStatusLocked(true); err != nil {
		m.status.DesiredRunning = false
		m.stopping = true
		if m.cmd != nil && m.cmd.Process != nil {
			_ = m.cmd.Process.Kill()
		}
		return m.status, fmt.Errorf("保存模型服務狀態失敗，已取消啟動: %w", err)
	}
	return m.status, nil
}

func (m *Manager) startLlamaLocked(settings domain.Settings, model, mmproj string, startupCommand domain.StartupCommand) (domain.LlamaStatus, error) {
	binary, err := ResolveServerBinary()
	if err != nil {
		return m.status, err
	}
	modelPath, err := resolveModelFile(settings.ModelDirectory, model, "模型")
	if err != nil {
		return m.status, err
	}
	if isGGUFDFlashDraft(modelPath) {
		return m.status, errors.New("DFlash Draft 模型不可作為 Target 模型啟動")
	}
	var mmprojPath string
	if strings.TrimSpace(mmproj) != "" {
		mmprojPath, err = resolveModelFile(settings.ModelDirectory, mmproj, "mmproj")
		if err != nil {
			return m.status, err
		}
	}
	var draftModelPath string
	if draftModel := strings.TrimSpace(startupCommand.DraftModel); draftModel != "" {
		if !isSupportedGGUFDFlashTarget(modelPath) {
			return m.status, errors.New("目前 Target GGUF 架構不支援 DFlash")
		}
		draftModelPath, err = resolveModelFile(settings.ModelDirectory, draftModel, "Draft 模型")
		if err != nil {
			return m.status, err
		}
		if !isGGUFDFlashDraft(draftModelPath) {
			return m.status, errors.New("指定的 Draft GGUF 不是 DFlash Draft 架構")
		}
		if draftModelPath == modelPath {
			return m.status, errors.New("DFlash Draft 模型不可與 Target 模型相同")
		}
	}

	args := append([]string(nil), startupCommand.ExtraArgs...)
	args = append(args, "--model", modelPath)
	if mmprojPath != "" {
		args = append(args, "--mmproj", mmprojPath)
	}
	if draftModelPath != "" {
		args = append(args, "--model-draft", draftModelPath)
	}
	args = append(args,
		"--host", startupCommand.ServerHost,
		"--port", strconv.Itoa(startupCommand.ServerPort),
		"--ctx-size", strconv.Itoa(startupCommand.ContextSize),
		"--n-gpu-layers", strconv.Itoa(startupCommand.GPULayers),
		"--openloader-access-control", m.accessControlPath,
	)
	if startupCommand.Threads > 0 {
		args = append(args, "--threads", strconv.Itoa(startupCommand.Threads))
	}

	command := exec.Command(binary, args...)
	command.Stdout = m.logs
	command.Stderr = m.logs
	m.logs.Append("\n$ " + binary + " " + strings.Join(args, " ") + "\n")
	if err := command.Start(); err != nil {
		return m.status, fmt.Errorf("啟動 llama-server 失敗: %w", err)
	}
	done := make(chan struct{})
	m.cmd = command
	m.done = done
	m.stopping = false
	m.status = domain.LlamaStatus{
		Running:            true,
		Runtime:            domain.RuntimeLlamaServer,
		PID:                command.Process.Pid,
		Model:              filepath.ToSlash(strings.TrimSpace(model)),
		MMProj:             filepath.ToSlash(strings.TrimSpace(mmproj)),
		DraftModel:         filepath.ToSlash(strings.TrimSpace(startupCommand.DraftModel)),
		Binary:             binary,
		StartupCommandID:   startupCommand.ID,
		StartupCommandName: startupCommand.Name,
		URL:                serverURL(startupCommand.ServerHost, startupCommand.ServerPort),
		StartedAt:          time.Now(),
	}
	go m.wait(command, done)
	go m.monitorReady(command, m.status.URL)
	return m.status, nil
}

func (m *Manager) startMLXLocked(settings domain.Settings, model, mmproj string, startupCommand domain.StartupCommand) (domain.LlamaStatus, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return m.status, fmt.Errorf("mlx-server 僅支援 macOS Apple Silicon；目前平台為 %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	binary, err := ResolveMLXServer()
	if err != nil {
		return m.status, err
	}
	modelArgument, isGGUF, err := resolveMLXTargetModel(settings, model, "模型")
	if err != nil {
		return m.status, err
	}
	ggufArchitecture := ""
	if isGGUF {
		if isMMProjGGUF(modelArgument) {
			return m.status, errors.New("mmproj 不可作為 GGUF Target 模型啟動")
		}
		architecture, metadataErr := readGGUFStringMetadata(modelArgument, "general.architecture")
		if metadataErr != nil || !isSupportedMLXGGUFArchitecture(architecture) {
			return m.status, fmt.Errorf("mlx-server 尚未支援此 GGUF 架構：%s", strings.TrimSpace(architecture))
		}
		ggufArchitecture = canonicalMLXGGUFArchitecture(architecture)
	}
	if (isGGUF && isGGUFDFlashDraft(modelArgument)) || (!isGGUF && isMLXDFlashDraftDirectory(modelArgument)) {
		return m.status, errors.New("DFlash Draft 模型不可作為 Target 模型啟動")
	}
	var mmprojArgument string
	statusMMProj := strings.TrimSpace(mmproj)
	if strings.TrimSpace(mmproj) != "" {
		if !isGGUF {
			return m.status, errors.New("mmproj 只能搭配 GGUF Target 模型")
		}
		mmprojArgument, err = resolveMLXGGUFFile(settings.ModelDirectory, mmproj, "mmproj")
		if err != nil {
			return m.status, err
		}
	} else if ggufArchitecture == "qwen35" {
		mmprojArgument, statusMMProj, err = resolveCompanionMMProj(settings.ModelDirectory, modelArgument)
		if err != nil {
			return m.status, err
		}
	}
	var draftModelArgument string
	if draftModel := strings.TrimSpace(startupCommand.DraftModel); draftModel != "" {
		if isGGUF {
			return m.status, errors.New("GGUF Target 目前使用一般 MLX 生成；DFlash 請搭配 MLX safetensors Target")
		}
		if !isSupportedMLXDFlashTargetDirectory(modelArgument) {
			return m.status, errors.New("目前 Target MLX 模型架構不支援 DFlash")
		}
		draftModelArgument, err = resolveMLXModel(settings.MLXModelDirectory, draftModel, "DFlash Draft 模型")
		if err != nil {
			return m.status, err
		}
		if !isSupportedMLXDFlashDraftDirectory(draftModelArgument) {
			return m.status, errors.New("只支援 Qwen3／Qwen3.5 的 DFlashDraftModel 或 DFlash2DraftModel")
		}
		if draftModelArgument == modelArgument {
			return m.status, errors.New("DFlash Draft 模型不可與 Target 模型相同")
		}
	}

	args := append([]string(nil), startupCommand.ExtraArgs...)
	args = append(args, "--model", modelArgument)
	if mmprojArgument != "" {
		args = append(args, "--mmproj", mmprojArgument)
	}
	if draftModelArgument != "" {
		// Qwen3.5 的完整 checkpoint 可能同時帶有 Vision 設定；DFlash 目前只使用
		// language target，因此明確覆寫自動偵測結果。
		args = append(args,
			"--model-type", "text",
			"--dflash-draft", draftModelArgument,
		)
	}
	args = append(args,
		"--host", startupCommand.ServerHost,
		"--port", strconv.Itoa(startupCommand.ServerPort),
		"--openloader-access-control", m.accessControlPath,
	)
	// 未量化的 256K rotating KV Cache 對大型模型會一次占用過多記憶體；
	// 一般 MLX 與 DFlash 使用可逐步成長的 Cache。只有參數明確啟用 KV
	// 量化時，才把啟動參數的 Context Size 套用為 rotating Cache 上限。
	if draftModelArgument == "" && hasAnyArgument(startupCommand.ExtraArgs, "--kv-bits", "--kv-scheme") {
		args = append(args, "--max-kv-size", strconv.Itoa(startupCommand.ContextSize))
	}

	command := exec.Command(binary, args...)
	command.Stdout = m.logs
	command.Stderr = m.logs
	m.logs.Append("\n$ " + binary + " " + strings.Join(args, " ") + "\n")
	if err := command.Start(); err != nil {
		return m.status, fmt.Errorf("啟動 mlx-server 失敗: %w", err)
	}
	done := make(chan struct{})
	m.cmd = command
	m.done = done
	m.stopping = false
	m.status = domain.LlamaStatus{
		Running:            true,
		Runtime:            domain.RuntimeMLXServer,
		PID:                command.Process.Pid,
		Model:              strings.TrimSpace(model),
		MMProj:             statusMMProj,
		DraftModel:         filepath.ToSlash(strings.TrimSpace(startupCommand.DraftModel)),
		Binary:             binary,
		StartupCommandID:   startupCommand.ID,
		StartupCommandName: startupCommand.Name,
		URL:                serverURL(startupCommand.ServerHost, startupCommand.ServerPort),
		StartedAt:          time.Now(),
	}
	go m.wait(command, done)
	go m.monitorReady(command, m.status.URL)
	return m.status, nil
}

func resolveMLXTargetModel(settings domain.Settings, value, label string) (string, bool, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, mlxGGUFPathPrefix) {
		path, err := resolveMLXGGUFFile(settings.ModelDirectory, value, label)
		return path, true, err
	}
	path, err := resolveMLXModel(settings.MLXModelDirectory, value, label)
	return path, false, err
}

func resolveMLXGGUFFile(directory, value, label string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), mlxGGUFPathPrefix))
	path, err := resolveModelFile(directory, value, label)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(path), ".gguf") {
		return "", fmt.Errorf("指定的%s不是 GGUF 檔案", label)
	}
	return path, nil
}

func resolveMLXModel(directory, value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s不可為空", label)
	}
	path, err := download.SafeJoin(directory, value)
	if err != nil {
		return "", fmt.Errorf("%s路徑格式錯誤: %w", label, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s目錄無法讀取: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("指定的%s不是 MLX 模型目錄", label)
	}
	if !isMLXModelDirectory(path) {
		return "", fmt.Errorf("指定的%s缺少 config.json 或 safetensors 權重", label)
	}
	return path, nil
}

func resolveModelFile(directory, relativePath, label string) (string, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		return "", fmt.Errorf("%s不可為空", label)
	}
	path, err := download.SafeJoin(directory, relativePath)
	if err != nil {
		return "", fmt.Errorf("%s路徑格式錯誤: %w", label, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s檔案無法讀取: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("指定的%s不是一般檔案", label)
	}
	return path, nil
}

func (m *Manager) Stop(ctx context.Context) error {
	return m.stop(ctx, true)
}

// Shutdown 僅停止本次應用程式的子程序，保留「下次啟動時恢復」旗標。
func (m *Manager) Shutdown(ctx context.Context) error {
	return m.stop(ctx, false)
}

func (m *Manager) stop(ctx context.Context, clearDesired bool) error {
	m.mu.Lock()
	if clearDesired {
		previousDesired := m.status.DesiredRunning
		m.status.DesiredRunning = false
		if err := m.persistStatusLocked(false); err != nil {
			m.status.DesiredRunning = previousDesired
			m.mu.Unlock()
			return fmt.Errorf("保存停止狀態失敗: %w", err)
		}
	}
	if !m.status.Running || m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return nil
	}
	command := m.cmd
	done := m.done
	m.stopping = true
	m.mu.Unlock()

	if err := command.Process.Signal(os.Interrupt); err != nil {
		_ = command.Process.Kill()
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = command.Process.Kill()
		return ctx.Err()
	case <-timer.C:
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Restore 在 Tanpopo 重新啟動時，依狀態檔重新載入上次仍在執行的模型。
// 回傳 attempted=true 代表狀態檔要求恢復，即使恢復失敗亦然。
func (m *Manager) Restore(resolveCommand func(string) (domain.StartupCommand, error)) (attempted bool, err error) {
	saved := m.stateStore.Get()
	if !saved.DesiredRunning {
		return false, nil
	}
	command, err := resolveCommand(saved.StartupCommandID)
	if err == nil && command.Runtime != saved.Runtime {
		err = fmt.Errorf("啟動參數 Runtime 已由 %s 變更為 %s", saved.Runtime, command.Runtime)
	}
	if err == nil {
		_, err = m.Start(saved.Model, saved.MMProj, saved.DraftModel, saved.DFlashEnabled, command)
	}
	if err == nil {
		return true, nil
	}

	m.mu.Lock()
	m.status.Running = false
	m.status.DesiredRunning = false
	m.status.PID = 0
	m.status.LastError = "自動恢復失敗：" + err.Error()
	m.status.StoppedAt = time.Now()
	persistErr := m.persistStatusLocked(false)
	m.mu.Unlock()
	if persistErr != nil {
		return true, fmt.Errorf("%v；另無法保存失敗狀態: %w", err, persistErr)
	}
	return true, err
}

func (m *Manager) persistStatusLocked(desiredRunning bool) error {
	return m.stateStore.Save(persistedRuntimeState{
		Version:            runtimeStateVersion,
		DesiredRunning:     desiredRunning,
		Runtime:            m.status.Runtime,
		Model:              m.status.Model,
		MMProj:             m.status.MMProj,
		DraftModel:         m.status.DraftModel,
		DFlashEnabled:      m.status.DFlashEnabled,
		StartupCommandID:   m.status.StartupCommandID,
		StartupCommandName: m.status.StartupCommandName,
	})
}

func (m *Manager) Status() domain.LlamaStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *Manager) Logs() string {
	return m.logs.String()
}

func (m *Manager) ClearLogs() {
	m.logs.Reset()
}

func (m *Manager) wait(command *exec.Cmd, done chan struct{}) {
	err := command.Wait()
	m.mu.Lock()
	if m.cmd == command {
		wasStopping := m.stopping
		m.status.Running = false
		m.status.Ready = false
		m.status.PID = 0
		m.status.StoppedAt = time.Now()
		if err != nil && !wasStopping {
			m.status.LastError = err.Error()
		} else {
			m.status.LastError = ""
		}
		m.cmd = nil
		m.done = nil
		m.stopping = false
	}
	close(done)
	m.mu.Unlock()
}

func (m *Manager) monitorReady(command *exec.Cmd, baseURL string) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 1200 * time.Millisecond}
	healthURL := strings.TrimRight(baseURL, "/") + "/health"

	for {
		m.mu.Lock()
		active := m.cmd == command && m.status.Running
		m.mu.Unlock()
		if !active {
			return
		}

		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, healthURL, nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4*1024))
				_ = response.Body.Close()
				// 舊版 Runtime 可能讓 /health 先經過模型 API 的 Access Key
				// 驗證。401／403 已足以證明服務完成監聽，不能因此讓管理介面
				// 永遠停在「載入中」；實際模型請求仍會照原安全策略驗證。
				if runtimeHealthResponseReady(response.StatusCode) {
					m.mu.Lock()
					if m.cmd == command && m.status.Running {
						m.status.Ready = true
					}
					m.mu.Unlock()
					return
				}
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func runtimeHealthResponseReady(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300 ||
		statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden
}

func (m *Manager) refreshURL() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshURLLocked()
}

func (m *Manager) refreshURLLocked() {
	settings := m.settings()
	m.status.URL = serverURL(settings.ServerHost, settings.ServerPort)
}

func serverURL(host string, port int) string {
	displayHost := host
	if host == "0.0.0.0" || host == "::" || host == "" {
		displayHost = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(displayHost, strconv.Itoa(port))
}

func ResolveServerBinary() (string, error) {
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	platform := runtimePlatform()
	candidates := make([]string, 0, 16)
	for _, root := range runtimeRoots() {
		candidates = append(candidates,
			filepath.Join(root, "llama-server", "prebuilt", platform, "bin", name),
			filepath.Join(root, "llama-runtime", "prebuilt", platform, "bin", name),
			filepath.Join(root, "llama.cpp", "current", "bin", name),
			filepath.Join(root, "llama.cpp", "build", "bin", name),
		)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, "services", "llama.cpp", "current", "bin", name),
			filepath.Join(home, "services", "llama.cpp", "bin", name),
			filepath.Join(home, "llama.cpp", "build", "bin", name),
		)
	}
	if binary, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, binary)
	}
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		candidateInfo, statErr := os.Stat(path)
		if statErr == nil && candidateInfo.Mode().IsRegular() && isExecutable(candidateInfo) {
			return path, nil
		}
	}
	return "", errors.New("找不到應用程式內附或已安裝的 llama-server；請先執行部署包 install.sh，開發環境則建立 llama-runtime 預編譯檔")
}

func ResolveMLXServer() (string, error) {
	platform := runtimePlatform()
	candidates := make([]string, 0, 10)
	for _, root := range runtimeRoots() {
		candidates = append(candidates,
			filepath.Join(root, "mlx-server", "prebuilt", platform, "bin", "mlx-server"),
			filepath.Join(root, "mlx-runtime", "prebuilt", platform, "bin", "mlx-server"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, "services", "mlx-server", "current", "bin", "mlx-server"),
		)
	}
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		candidateInfo, statErr := os.Stat(path)
		if statErr != nil || !candidateInfo.Mode().IsRegular() || !isExecutable(candidateInfo) {
			continue
		}
		metalLibrary := filepath.Join(
			filepath.Dir(path),
			"mlx-swift_Cmlx.bundle", "Contents", "Resources", "default.metallib",
		)
		if metalInfo, metalErr := os.Stat(metalLibrary); metalErr != nil || !metalInfo.Mode().IsRegular() {
			continue
		}
		return path, nil
	}
	return "", errors.New("找不到應用程式內附或已安裝的 mlx-server Runtime；請先執行部署包 install.sh，開發環境則執行 scripts/build-mlx-server-runtime.sh")
}

func runtimePlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func runtimeRoots() []string {
	result := make([]string, 0, 4)
	seen := make(map[string]bool)
	appendRoot := func(path string) {
		absolute, err := filepath.Abs(path)
		if err != nil || seen[absolute] {
			return
		}
		seen[absolute] = true
		result = append(result, absolute)
	}
	if executable, err := os.Executable(); err == nil {
		executableDirectory := filepath.Dir(executable)
		appendRoot(executableDirectory)
		if filepath.Base(executableDirectory) == "bin" {
			appendRoot(filepath.Dir(executableDirectory))
		}
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		appendRoot(workingDirectory)
	}
	return result
}

func ListModels(directory string) ([]domain.ModelFile, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, errors.New("請先設定模型目錄")
	}
	base, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return nil, err
	}
	result := make([]domain.ModelFile, 0)
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		architecture, _ := readGGUFStringMetadata(path, "general.architecture")
		architecture = normalizeModelArchitecture(architecture)
		isDraft := architecture == "dflash"
		result = append(result, domain.ModelFile{
			Path:            filepath.ToSlash(relative),
			Format:          "gguf",
			Size:            info.Size(),
			ModifiedAt:      info.ModTime(),
			Architecture:    architecture,
			DFlashSupported: !isDraft && isSupportedDFlashTargetArchitecture(architecture),
			DFlashDraft:     isDraft,
			DFlashVariant:   ggufDFlashVariant(entry.Name(), isDraft),
		})
		if len(result) >= 5000 {
			return io.EOF
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Path) < strings.ToLower(result[j].Path) })
	return result, nil
}

func ListMLXModels(directory string) ([]domain.ModelFile, error) {
	return listMLXModels(directory, false)
}

// ListMLXRuntimeModels 合併原生 MLX safetensors 目錄與 mlx-server 可載入的
// GGUF。GGUF 使用明確前綴，避免兩個模型根目錄出現同名項目時解析錯誤。
func ListMLXRuntimeModels(mlxDirectory, ggufDirectory string) ([]domain.ModelFile, error) {
	if strings.TrimSpace(mlxDirectory) == "" && strings.TrimSpace(ggufDirectory) == "" {
		return nil, errors.New("請先設定 MLX 或 GGUF 模型目錄")
	}
	result := make([]domain.ModelFile, 0)
	if strings.TrimSpace(mlxDirectory) != "" {
		mlxModels, err := ListMLXModels(mlxDirectory)
		if err != nil {
			return nil, err
		}
		result = append(result, mlxModels...)
	}
	if strings.TrimSpace(ggufDirectory) != "" {
		ggufModels, err := ListModels(ggufDirectory)
		if err != nil {
			return nil, err
		}
		for _, model := range ggufModels {
			if model.DFlashDraft {
				continue
			}
			if !isMMProjGGUF(model.Path) && !isSupportedMLXGGUFArchitecture(model.Architecture) {
				continue
			}
			model.Path = mlxGGUFPathPrefix + model.Path
			model.Format = "gguf"
			// GGUF Target 目前不接 DFlash Draft；保留原生 MLX Target 的既有支援。
			model.DFlashSupported = false
			result = append(result, model)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Format != result[j].Format {
			return result[i].Format < result[j].Format
		}
		return strings.ToLower(result[i].Path) < strings.ToLower(result[j].Path)
	})
	return result, nil
}

func isSupportedMLXGGUFArchitecture(architecture string) bool {
	canonical := canonicalMLXGGUFArchitecture(architecture)
	switch canonical {
	case "qwen35", "qwen3", "qwen2", "llama":
		return true
	default:
		return false
	}
}

func canonicalMLXGGUFArchitecture(architecture string) string {
	return strings.NewReplacer("_", "", "-", "", ".", "").Replace(
		normalizeModelArchitecture(architecture),
	)
}

func resolveCompanionMMProj(modelRoot, targetPath string) (string, string, error) {
	entries, err := os.ReadDir(filepath.Dir(targetPath))
	if err != nil {
		return "", "", fmt.Errorf("讀取 GGUF 模型目錄失敗: %w", err)
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if entry.IsDir() || !isMMProjGGUF(entry.Name()) || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			continue
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(targetPath), entry.Name()))
	}
	if len(candidates) == 0 {
		return "", "", errors.New("Qwen3.5 GGUF 需要搭配同目錄的 mmproj 才能由 mlx-server 正確生成內容；請先下載配對的 mmproj")
	}
	if len(candidates) > 1 {
		return "", "", errors.New("找到多個 mmproj，請在執行狀態頁選擇與 GGUF 模型配對的檔案")
	}
	relative, err := filepath.Rel(modelRoot, candidates[0])
	if err != nil {
		return "", "", fmt.Errorf("解析 mmproj 路徑失敗: %w", err)
	}
	return candidates[0], mlxGGUFPathPrefix + filepath.ToSlash(relative), nil
}

func isMMProjGGUF(path string) bool {
	return strings.Contains(strings.ToLower(filepath.Base(path)), "mmproj")
}

// ListMLXDFlashModels 只列出目前原生 Swift Runtime 已支援的 DFlash Draft。
// Target 與 Draft 使用相同模型根目錄，但透過 role 分流避免 Draft 被誤選為主模型。
func ListMLXDFlashModels(directory string) ([]domain.ModelFile, error) {
	return listMLXModels(directory, true)
}

func normalizeModelArchitecture(architecture string) string {
	return strings.ToLower(strings.TrimSpace(architecture))
}

func isSupportedDFlashTargetArchitecture(architecture string) bool {
	switch normalizeModelArchitecture(architecture) {
	case "qwen3", "qwen3moe", "qwen3_moe", "qwen35", "qwen35moe", "qwen3_5", "qwen3_5_moe":
		return true
	default:
		return false
	}
}

func isGGUFDFlashDraft(path string) bool {
	architecture, err := readGGUFStringMetadata(path, "general.architecture")
	return err == nil && normalizeModelArchitecture(architecture) == "dflash"
}

func isSupportedGGUFDFlashTarget(path string) bool {
	architecture, err := readGGUFStringMetadata(path, "general.architecture")
	return err == nil && isSupportedDFlashTargetArchitecture(architecture)
}

func ggufDFlashVariant(filename string, isDraft bool) string {
	if !isDraft {
		return ""
	}
	if strings.Contains(strings.ToLower(filename), "dflash2") {
		return "dflash2"
	}
	return "dflash1"
}

func listMLXModels(directory string, draftsOnly bool) ([]domain.ModelFile, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, errors.New("請先設定 MLX 模型目錄")
	}
	base, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return nil, err
	}

	result := make([]domain.ModelFile, 0)
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != base && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "config.json" || path == filepath.Join(base, "config.json") {
			return nil
		}
		modelDirectory := filepath.Dir(path)
		if !isMLXModelDirectory(modelDirectory) {
			return nil
		}
		configuration, configurationErr := readMLXModelConfiguration(modelDirectory)
		if configurationErr != nil {
			return nil
		}
		architecture := normalizeModelArchitecture(configuration.ModelType)
		isDraft := isMLXDFlashDraftConfiguration(configuration)
		if draftsOnly {
			if !isSupportedMLXDFlashDraftConfiguration(configuration) {
				return filepath.SkipDir
			}
		} else if isDraft {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(base, modelDirectory)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(modelDirectory)
		if err != nil {
			return err
		}
		var size int64
		var modified time.Time
		for _, modelEntry := range entries {
			if modelEntry.IsDir() {
				continue
			}
			info, infoErr := modelEntry.Info()
			if infoErr != nil || !info.Mode().IsRegular() {
				continue
			}
			size += info.Size()
			if info.ModTime().After(modified) {
				modified = info.ModTime()
			}
		}
		result = append(result, domain.ModelFile{
			Path:            filepath.ToSlash(relative),
			Format:          "mlx",
			Size:            size,
			ModifiedAt:      modified,
			Architecture:    architecture,
			DFlashSupported: !isDraft && isSupportedDFlashTargetArchitecture(architecture),
			DFlashDraft:     isDraft,
			DFlashVariant:   mlxDFlashVariant(configuration),
		})
		if len(result) >= 5000 {
			return io.EOF
		}
		return filepath.SkipDir
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Path) < strings.ToLower(result[j].Path) })
	return result, nil
}

type mlxModelConfiguration struct {
	Architectures []string `json:"architectures"`
	ModelType     string   `json:"model_type"`
	HiddenSize    int      `json:"hidden_size"`
	LayerTypes    []string `json:"layer_types"`
	SlidingWindow int      `json:"sliding_window"`
	DFlash        struct {
		ConvolutionKernelSize int `json:"conv_kernel_size"`
		ConvolutionGroupSize  int `json:"conv_group_size"`
		SelectorRank          int `json:"selector_rank"`
		SelectorTopK          int `json:"selector_top_k"`
	} `json:"dflash_config"`
}

func readMLXModelConfiguration(directory string) (mlxModelConfiguration, error) {
	content, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		return mlxModelConfiguration{}, err
	}
	var configuration mlxModelConfiguration
	if err := json.Unmarshal(content, &configuration); err != nil {
		return mlxModelConfiguration{}, err
	}
	return configuration, nil
}

func isMLXDFlashDraftDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	return err == nil && isMLXDFlashDraftConfiguration(configuration)
}

func isMLXDFlashDraftConfiguration(configuration mlxModelConfiguration) bool {
	for _, architecture := range configuration.Architectures {
		if architecture == "DFlashDraftModel" || architecture == "DFlash2DraftModel" {
			return true
		}
	}
	return false
}

func isSupportedMLXDFlashDraftDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	return err == nil && isSupportedMLXDFlashDraftConfiguration(configuration)
}

func isSupportedMLXDFlashTargetDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	return err == nil && !isMLXDFlashDraftConfiguration(configuration) && isSupportedDFlashTargetArchitecture(configuration.ModelType)
}

func isSupportedMLXDFlashDraftConfiguration(configuration mlxModelConfiguration) bool {
	if !isSupportedDFlashTargetArchitecture(configuration.ModelType) {
		return false
	}
	foundVariants := 0
	isDFlash2 := false
	for _, architecture := range configuration.Architectures {
		if architecture == "DFlash2DraftModel" {
			foundVariants++
			isDFlash2 = true
		}
		if architecture == "DFlashDraftModel" {
			foundVariants++
		}
	}
	if foundVariants != 1 || len(configuration.LayerTypes) == 0 {
		return false
	}
	for _, layerType := range configuration.LayerTypes {
		if layerType != "full_attention" && layerType != "sliding_attention" {
			return false
		}
		if layerType == "sliding_attention" && configuration.SlidingWindow <= 1 {
			return false
		}
	}
	if isDFlash2 {
		config := configuration.DFlash
		if config.ConvolutionKernelSize < 2 || config.ConvolutionGroupSize <= 0 ||
			configuration.HiddenSize <= 0 || configuration.HiddenSize%config.ConvolutionGroupSize != 0 ||
			config.SelectorRank <= 0 || config.SelectorTopK <= 1 {
			return false
		}
	}
	return true
}

func mlxDFlashVariant(configuration mlxModelConfiguration) string {
	for _, architecture := range configuration.Architectures {
		if architecture == "DFlash2DraftModel" {
			return "dflash2"
		}
		if architecture == "DFlashDraftModel" {
			return "dflash1"
		}
	}
	return ""
}

func withoutDFlashArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		switch argument {
		case "--model-draft", "--dflash-draft", "--dflash-block-size":
			if index+1 < len(arguments) {
				index++
			}
			continue
		case "--spec-type":
			if index+1 >= len(arguments) {
				continue
			}
			index++
			if value := withoutDFlashSpecType(arguments[index]); value != "" {
				filtered = append(filtered, "--spec-type", value)
			}
			continue
		}
		if strings.HasPrefix(argument, "--model-draft=") ||
			strings.HasPrefix(argument, "--dflash-draft=") ||
			strings.HasPrefix(argument, "--dflash-block-size=") {
			continue
		}
		if strings.HasPrefix(argument, "--spec-type=") {
			if value := withoutDFlashSpecType(strings.TrimPrefix(argument, "--spec-type=")); value != "" {
				filtered = append(filtered, "--spec-type="+value)
			}
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

func hasAnyArgument(arguments []string, expected ...string) bool {
	for _, rawArgument := range arguments {
		argument := strings.TrimSpace(rawArgument)
		for _, candidate := range expected {
			if argument == candidate || strings.HasPrefix(argument, candidate+"=") {
				return true
			}
		}
	}
	return false
}

func withoutDraftModelArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		if argument == "--model-draft" || argument == "--dflash-draft" {
			if index+1 < len(arguments) {
				index++
			}
			continue
		}
		if strings.HasPrefix(argument, "--model-draft=") || strings.HasPrefix(argument, "--dflash-draft=") {
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

func withoutDFlashSpecType(value string) string {
	values := strings.Split(value, ",")
	filtered := values[:0]
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item != "" && !strings.EqualFold(item, "draft-dflash") {
			filtered = append(filtered, item)
		}
	}
	return strings.Join(filtered, ",")
}

func isMLXModelDirectory(directory string) bool {
	configInfo, err := os.Stat(filepath.Join(directory, "config.json"))
	if err != nil || !configInfo.Mode().IsRegular() {
		return false
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	hasSafetensors := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".safetensors") {
			hasSafetensors = true
		}
	}
	if !hasSafetensors {
		return false
	}
	indexPath := filepath.Join(directory, "model.safetensors.index.json")
	content, err := os.ReadFile(indexPath)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	var index struct {
		WeightMap map[string]string `json:"weight_map"`
	}
	if err := json.Unmarshal(content, &index); err != nil || len(index.WeightMap) == 0 {
		return false
	}
	for _, filename := range index.WeightMap {
		path, joinErr := download.SafeJoin(directory, filepath.ToSlash(filename))
		if joinErr != nil {
			return false
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func isExecutable(info os.FileInfo) bool {
	return runtime.GOOS == "windows" || info.Mode().Perm()&0111 != 0
}

type logBuffer struct {
	mu    sync.Mutex
	max   int
	bytes bytes.Buffer
}

func newLogBuffer(max int) *logBuffer {
	return &logBuffer{max: max}
}

func (b *logBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	count, err := b.bytes.Write(value)
	b.trimLocked()
	return count, err
}

func (b *logBuffer) Append(value string) {
	_, _ = b.Write([]byte(value))
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bytes.String()
}

func (b *logBuffer) Reset() {
	b.mu.Lock()
	b.bytes.Reset()
	b.mu.Unlock()
}

func (b *logBuffer) trimLocked() {
	if b.bytes.Len() <= b.max {
		return
	}
	content := b.bytes.Bytes()
	start := len(content) - b.max
	if nextLine := bytes.IndexByte(content[start:], '\n'); nextLine >= 0 {
		start += nextLine + 1
	}
	trimmed := append([]byte(nil), content[start:]...)
	b.bytes.Reset()
	_, _ = b.bytes.Write(trimmed)
}
