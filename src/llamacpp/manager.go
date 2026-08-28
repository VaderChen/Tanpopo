package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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

type SettingsProvider func() domain.Settings

type Manager struct {
	mu                sync.Mutex
	settings          SettingsProvider
	accessControlPath string
	cmd               *exec.Cmd
	done              chan struct{}
	stopping          bool
	status            domain.LlamaStatus
	logs              *logBuffer
}

func NewManager(settings SettingsProvider, accessControlPath string) *Manager {
	accessControlPath = strings.TrimSpace(accessControlPath)
	if absolute, err := filepath.Abs(accessControlPath); err == nil {
		accessControlPath = absolute
	}
	manager := &Manager{
		settings:          settings,
		accessControlPath: accessControlPath,
		logs:              newLogBuffer(128 * 1024),
	}
	manager.status.Runtime = domain.RuntimeLlamaServer
	manager.refreshURL()
	return manager
}

func (m *Manager) Start(model, mmproj string, startupCommand domain.StartupCommand) (domain.LlamaStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Running {
		return m.status, errors.New("模型服務已在執行中；請先停止目前模型")
	}
	settings := m.settings()
	if startupCommand.Runtime == domain.RuntimeMLXServer {
		return m.startMLXLocked(settings, model, startupCommand)
	}
	return m.startLlamaLocked(settings, model, mmproj, startupCommand)
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
	var mmprojPath string
	if strings.TrimSpace(mmproj) != "" {
		mmprojPath, err = resolveModelFile(settings.ModelDirectory, mmproj, "mmproj")
		if err != nil {
			return m.status, err
		}
	}
	var draftModelPath string
	if draftModel := strings.TrimSpace(startupCommand.DraftModel); draftModel != "" {
		draftModelPath, err = resolveModelFile(settings.ModelDirectory, draftModel, "Draft 模型")
		if err != nil {
			return m.status, err
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
	return m.status, nil
}

func (m *Manager) startMLXLocked(settings domain.Settings, model string, startupCommand domain.StartupCommand) (domain.LlamaStatus, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return m.status, fmt.Errorf("mlx-server 僅支援 macOS Apple Silicon；目前平台為 %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	binary, err := ResolveMLXServer()
	if err != nil {
		return m.status, err
	}
	modelArgument, err := resolveMLXModel(settings.MLXModelDirectory, model, "模型")
	if err != nil {
		return m.status, err
	}
	if isMLXDFlashDraftDirectory(modelArgument) {
		return m.status, errors.New("DFlash Draft 模型不可作為 Target 模型啟動")
	}
	var draftModelArgument string
	if draftModel := strings.TrimSpace(startupCommand.DraftModel); draftModel != "" {
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
	// DFlash 必須精確回退 target cache；設定 Draft 時不可用 --max-kv-size 建立
	// rotating target cache。未使用 DFlash 時仍套用啟動設定的 Context Size。
	if draftModelArgument == "" {
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
		DraftModel:         filepath.ToSlash(strings.TrimSpace(startupCommand.DraftModel)),
		Binary:             binary,
		StartupCommandID:   startupCommand.ID,
		StartupCommandName: startupCommand.Name,
		URL:                serverURL(startupCommand.ServerHost, startupCommand.ServerPort),
		StartedAt:          time.Now(),
	}
	go m.wait(command, done)
	return m.status, nil
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
	m.mu.Lock()
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
		result = append(result, domain.ModelFile{
			Path:       filepath.ToSlash(relative),
			Size:       info.Size(),
			ModifiedAt: info.ModTime(),
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

// ListMLXDFlashModels 只列出目前原生 Swift Runtime 已支援的 DFlash Draft。
// Target 與 Draft 使用相同模型根目錄，但透過 role 分流避免 Draft 被誤選為主模型。
func ListMLXDFlashModels(directory string) ([]domain.ModelFile, error) {
	return listMLXModels(directory, true)
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
		if draftsOnly {
			if !isSupportedMLXDFlashDraftDirectory(modelDirectory) {
				return filepath.SkipDir
			}
		} else if isMLXDFlashDraftDirectory(modelDirectory) {
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
			Path:       filepath.ToSlash(relative),
			Size:       size,
			ModifiedAt: modified,
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
	if err != nil {
		return false
	}
	for _, architecture := range configuration.Architectures {
		if architecture == "DFlashDraftModel" || architecture == "DFlash2DraftModel" {
			return true
		}
	}
	return false
}

func isSupportedMLXDFlashDraftDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	if err != nil || configuration.ModelType != "qwen3" && configuration.ModelType != "qwen3_5" {
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
