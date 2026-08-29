// Package netpass 以受控子程序整合 NetPassClient，並保存不對前端揭露的連線憑證。
package netpass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultEndpoint = "https://netpass.mars-cloud.com"
	maxLogBytes     = 32 * 1024
)

var (
	clientIDPattern = regexp.MustCompile(`NetPassClient starting with\s*:\s*([^/\s]+)`)
	namePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,62}[A-Za-z0-9]$`)
)

// Config 是保存於本機的 NetPassClient 連線設定；APIKey 不會出現在 Status。
type Config struct {
	Endpoint string `json:"endpoint"`
	APIKey   string `json:"api_key,omitempty"`
	Name     string `json:"name,omitempty"`
}

// ConfigUpdate 支援留空保留既有金鑰，或由 ClearAPIKey 明確清除。
type ConfigUpdate struct {
	Endpoint    string `json:"endpoint"`
	APIKey      string `json:"api_key"`
	ClearAPIKey bool   `json:"clear_api_key"`
	Name        string `json:"name"`
}

// Status 是提供給管理介面的非敏感連線摘要。
type Status struct {
	RuntimeChecked bool      `json:"runtime_checked"`
	Available      bool      `json:"available"`
	Running        bool      `json:"running"`
	Connected      bool      `json:"connected"`
	Endpoint       string    `json:"endpoint"`
	APIKeySet      bool      `json:"api_key_set"`
	Name           string    `json:"name,omitempty"`
	PID            int       `json:"pid,omitempty"`
	ClientID       string    `json:"client_id,omitempty"`
	PublicURL      string    `json:"public_url,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	LastOutput     string    `json:"-"`
}

type runtimeConfig struct {
	APIKey     string `json:"api_key"`
	Host       string `json:"host"`
	Name       string `json:"name,omitempty"`
	AutoUpdate bool   `json:"auto_update"`
}

// Manager 只允許一個由 Tanpopo 管理的 NetPassClient 子程序。
type Manager struct {
	ctx            context.Context
	configPath     string
	runtimeDir     string
	managementPort int

	mu          sync.Mutex
	config      Config
	status      Status
	command     *exec.Cmd
	outputCarry string
}

func NewManager(ctx context.Context, configPath string, managementPort int) *Manager {
	absoluteConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		absoluteConfigPath = configPath
	}
	manager := &Manager{
		ctx:            ctx,
		configPath:     absoluteConfigPath,
		runtimeDir:     filepath.Join(filepath.Dir(absoluteConfigPath), "netpass-client"),
		managementPort: managementPort,
		config:         Config{Endpoint: defaultEndpoint},
	}
	if loadErr := manager.load(); loadErr != nil {
		manager.status.LastError = loadErr.Error()
	}
	manager.syncConfigStatusLocked()
	go func() {
		<-ctx.Done()
		_ = manager.Stop()
	}()
	return manager
}

func (m *Manager) load() error {
	content, err := os.ReadFile(m.configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("讀取 NetPass 設定失敗: %w", err)
	}
	var value Config
	if err := json.Unmarshal(content, &value); err != nil {
		return fmt.Errorf("解析 NetPass 設定失敗: %w", err)
	}
	value, err = normalizeConfig(value)
	if err != nil {
		return err
	}
	m.config = value
	return nil
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	status := m.status
	m.mu.Unlock()
	return status
}

func (m *Manager) UpdateConfig(update ConfigUpdate) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Running {
		return m.status, errors.New("請先停止 NetPass 連線，再修改設定")
	}
	value := Config{
		Endpoint: update.Endpoint,
		APIKey:   m.config.APIKey,
		Name:     update.Name,
	}
	if update.ClearAPIKey {
		value.APIKey = ""
	} else if strings.TrimSpace(update.APIKey) != "" {
		value.APIKey = strings.TrimSpace(update.APIKey)
	}
	value, err := normalizeConfig(value)
	if err != nil {
		return m.status, err
	}
	if err := writeJSONAtomic(m.configPath, value); err != nil {
		return m.status, fmt.Errorf("保存 NetPass 設定失敗: %w", err)
	}
	m.config = value
	m.status.LastError = ""
	m.syncConfigStatusLocked()
	return m.status, nil
}

func (m *Manager) Start() (Status, error) {
	m.mu.Lock()
	if m.status.Running {
		status := m.status
		m.mu.Unlock()
		return status, nil
	}
	if strings.TrimSpace(m.config.APIKey) == "" {
		m.mu.Unlock()
		return m.Status(), errors.New("請先設定 NetPass API Key")
	}
	binary, err := findBinary()
	m.status.RuntimeChecked = true
	if err != nil {
		m.status.Available = false
		m.status.LastError = err.Error()
		status := m.status
		m.mu.Unlock()
		return status, err
	}
	m.status.Available = true
	if err := os.MkdirAll(m.runtimeDir, 0700); err != nil {
		m.mu.Unlock()
		return m.Status(), fmt.Errorf("建立 NetPass 工作目錄失敗: %w", err)
	}
	clientConfig := runtimeConfig{
		APIKey:     m.config.APIKey,
		Host:       m.config.Endpoint,
		Name:       m.config.Name,
		AutoUpdate: false,
	}
	if err := writeJSONAtomic(filepath.Join(m.runtimeDir, "config.json"), clientConfig); err != nil {
		m.mu.Unlock()
		return m.Status(), fmt.Errorf("建立 NetPassClient 設定失敗: %w", err)
	}

	command := exec.Command(binary, "-d")
	command.Dir = m.runtimeDir
	command.Env = append(os.Environ(), "LANG=en_US.UTF-8")
	command.Stdout = m
	command.Stderr = m
	if err := command.Start(); err != nil {
		m.status.LastError = fmt.Sprintf("啟動 NetPassClient 失敗: %v", err)
		status := m.status
		m.mu.Unlock()
		return status, errors.New(m.status.LastError)
	}
	m.command = command
	m.outputCarry = ""
	m.status.Running = true
	m.status.Connected = false
	m.status.PID = command.Process.Pid
	m.status.ClientID = ""
	m.status.PublicURL = ""
	m.status.StartedAt = time.Now().UTC()
	m.status.LastError = ""
	m.status.LastOutput = ""
	m.syncConfigStatusLocked()
	status := m.status
	m.mu.Unlock()

	go m.wait(command)
	return status, nil
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	command := m.command
	m.command = nil
	m.status.Running = false
	m.status.Connected = false
	m.status.PID = 0
	m.status.ClientID = ""
	m.status.PublicURL = ""
	m.mu.Unlock()
	_ = os.Remove(filepath.Join(m.runtimeDir, "config.json"))
	if command == nil || command.Process == nil {
		return nil
	}
	if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("停止 NetPassClient 失敗: %w", err)
	}
	return nil
}

func (m *Manager) wait(command *exec.Cmd) {
	err := command.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.command != command {
		return
	}
	m.command = nil
	m.status.Running = false
	m.status.Connected = false
	m.status.PID = 0
	m.status.ClientID = ""
	m.status.PublicURL = ""
	_ = os.Remove(filepath.Join(m.runtimeDir, "config.json"))
	if err != nil && m.ctx.Err() == nil {
		m.status.LastError = fmt.Sprintf("NetPassClient 已停止: %v", err)
	}
}

// Write 接收 NetPassClient 的 stdout/stderr，並從既有公開訊息解析連線狀態與分配 ID。
func (m *Manager) Write(content []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	text := m.outputCarry + string(content)
	lines := strings.Split(text, "\n")
	m.outputCarry = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		m.processLineLocked(strings.TrimSpace(line))
	}
	return len(content), nil
}

func (m *Manager) processLineLocked(line string) {
	if line == "" {
		return
	}
	m.status.LastOutput = strings.TrimSpace(m.status.LastOutput + "\n" + line)
	if len(m.status.LastOutput) > maxLogBytes {
		m.status.LastOutput = m.status.LastOutput[len(m.status.LastOutput)-maxLogBytes:]
	}
	if match := clientIDPattern.FindStringSubmatch(line); len(match) == 2 {
		m.status.ClientID = strings.TrimSpace(match[1])
		m.status.PublicURL = publicURL(m.config.Endpoint, m.status.ClientID, m.managementPort)
	}
	if strings.Contains(line, "Connected to NetPass Tunnel") {
		m.status.Connected = true
		m.status.LastError = ""
	}
	if strings.Contains(line, "Connect lost:") || strings.Contains(line, "Initial connection failed:") {
		m.status.Connected = false
		m.status.LastError = line
	}
}

func (m *Manager) syncConfigStatusLocked() {
	m.status.Endpoint = m.config.Endpoint
	m.status.APIKeySet = strings.TrimSpace(m.config.APIKey) != ""
	m.status.Name = m.config.Name
}

func normalizeConfig(value Config) (Config, error) {
	value.Endpoint = strings.TrimRight(strings.TrimSpace(value.Endpoint), "/")
	value.APIKey = strings.TrimSpace(value.APIKey)
	value.Name = strings.TrimSpace(value.Name)
	if value.Endpoint == "" {
		value.Endpoint = defaultEndpoint
	}
	parsed, err := url.Parse(value.Endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return Config{}, errors.New("NetPass Server URL 僅支援不含帳密的 http 或 https 網址")
	}
	if value.Name != "" && !namePattern.MatchString(value.Name) {
		return Config{}, errors.New("裝置名稱須為 3–64 個英數字元，可包含句點、底線與連字號")
	}
	return value, nil
}

func publicURL(endpoint, clientID string, port int) string {
	if strings.TrimSpace(clientID) == "" || port < 1 || port > 65535 {
		return ""
	}
	return fmt.Sprintf("%s/pass/%s/%d/", strings.TrimRight(endpoint, "/"), url.PathEscape(clientID), port)
}

func findBinary() (string, error) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	filename := "NetPassClient"
	externalFilename := "NetPassClient_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOARCH == "amd64" {
		externalFilename = "NetPassClient_" + runtime.GOOS + "_x64"
	}
	if runtime.GOOS == "windows" {
		filename += ".exe"
		externalFilename += ".exe"
	}

	workingDirectory, _ := os.Getwd()
	executable, _ := os.Executable()
	executableDirectory := filepath.Dir(executable)
	roots := uniquePaths([]string{
		workingDirectory,
		executableDirectory,
		filepath.Dir(executableDirectory),
		filepath.Dir(workingDirectory),
	})
	candidates := make([]string, 0, len(roots)*4)
	for _, root := range roots {
		candidates = append(candidates,
			// 安裝包只注入閉源執行檔；macOS .app 與 Windows MSI 都使用固定資源位置。
			filepath.Join(root, "netpass-client", filename),
			filepath.Join(root, "netpass-client", "prebuilt", platform, "bin", filename),
			filepath.Join(root, "Resources", "netpass-client", filename),
			filepath.Join(root, "NetPassService", "Client", "bin", externalFilename),
			filepath.Join(root, "NetPassService", "Client", filename),
		)
	}
	for _, candidate := range uniquePaths(candidates) {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm()&0111 != 0) {
			absolute, absoluteErr := filepath.Abs(candidate)
			if absoluteErr == nil {
				return absolute, nil
			}
			return candidate, nil
		}
	}
	return "", fmt.Errorf("找不到 %s/%s 的 NetPassClient；請確認部署包已包含 netpass-client Runtime", runtime.GOOS, runtime.GOARCH)
}

func uniquePaths(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.Clean(strings.TrimSpace(value))
		if value == "." || value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func writeJSONAtomic(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".netpass-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
