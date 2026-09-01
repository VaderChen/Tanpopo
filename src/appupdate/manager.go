package appupdate

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxUploadBytes 限制壓縮檔本身，避免管理介面被超大型請求耗盡磁碟。
	MaxUploadBytes       int64  = 4 << 30
	maxArchiveFiles             = 100_000
	maxArchiveEntryBytes uint64 = 8 << 30
	maxArchiveTotalBytes uint64 = 16 << 30
	statusFilename              = "app-update-status.json"
)

// Status 是可持續跨越服務重啟的 Linux ZIP 更新狀態。
type Status struct {
	Available bool      `json:"available"`
	State     string    `json:"state"`
	Message   string    `json:"message,omitempty"`
	Version   string    `json:"version,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type Manager struct {
	mu             sync.Mutex
	executablePath string
	targetDir      string
	available      bool
	active         bool
	helperPID      int
}

type ApplyOptions struct {
	PayloadDir string
	TargetDir  string
	Workspace  string
	ParentPID  int
}

// NewManager 只在正式安裝的 Linux 目錄開啟更新功能。開發模式的 go run
// 執行檔不具備完整部署包結構，因此不允許覆寫工作區。
func NewManager() *Manager {
	manager := &Manager{}
	if runtime.GOOS != "linux" {
		return manager
	}
	executablePath, err := os.Executable()
	if err != nil {
		return manager
	}
	executablePath, err = filepath.Abs(executablePath)
	if err != nil {
		return manager
	}
	targetDir := filepath.Dir(executablePath)
	if filepath.Base(executablePath) != "Tanpopo" || !isRegularFile(filepath.Join(targetDir, "run.sh")) {
		return manager
	}
	manager.executablePath = executablePath
	manager.targetDir = targetDir
	manager.available = true
	return manager
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.available {
		return Status{Available: false, State: "unavailable"}
	}
	status, err := readStatus(m.statusPath())
	if err != nil {
		return Status{Available: true, State: "idle"}
	}
	if m.active && (status.State == "failed" || status.State == "completed") {
		m.active = false
		m.helperPID = 0
	} else if m.active && m.helperPID > 0 && !processAlive(m.helperPID) {
		status.State = "failed"
		status.Message = "更新程序意外停止，現有版本未變更。"
		_ = writeStatus(m.statusPath(), status)
		m.active = false
		m.helperPID = 0
	}
	status.Available = true
	return status
}

func (m *Manager) Start(archive io.Reader) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.available {
		return Status{}, errors.New("透過 ZIP 更新僅支援正式安裝的 Linux 版本")
	}
	if m.active {
		return Status{}, errors.New("已有 ZIP 更新正在進行")
	}
	m.active = true
	succeeded := false
	defer func() {
		if !succeeded {
			m.active = false
		}
	}()

	workspace, err := os.MkdirTemp(filepath.Dir(m.targetDir), ".tanpopo-update-")
	if err != nil {
		return Status{}, fmt.Errorf("建立更新暫存目錄失敗: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(workspace)
		}
	}()

	archivePath := filepath.Join(workspace, "update.zip")
	archiveFile, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Status{}, fmt.Errorf("建立更新檔案失敗: %w", err)
	}
	written, copyErr := io.Copy(archiveFile, io.LimitReader(archive, MaxUploadBytes+1))
	closeErr := archiveFile.Close()
	if copyErr != nil {
		return Status{}, fmt.Errorf("保存更新 ZIP 失敗: %w", copyErr)
	}
	if closeErr != nil {
		return Status{}, fmt.Errorf("關閉更新 ZIP 失敗: %w", closeErr)
	}
	if written == 0 {
		return Status{}, errors.New("上傳的 ZIP 是空檔案")
	}
	if written > MaxUploadBytes {
		return Status{}, errors.New("更新 ZIP 超過 4 GiB 上限")
	}

	extractRoot := filepath.Join(workspace, "extracted")
	payloadDir, version, err := extractAndValidate(archivePath, extractRoot)
	if err != nil {
		return Status{}, err
	}
	status := Status{
		Available: true,
		State:     "preparing",
		Message:   "更新套件已驗證，正在準備安裝。",
		Version:   version,
		UpdatedAt: time.Now(),
	}
	if err := writeStatus(m.statusPath(), status); err != nil {
		return Status{}, fmt.Errorf("寫入更新狀態失敗: %w", err)
	}

	logPath := filepath.Join(workspace, "update-helper.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return Status{}, fmt.Errorf("建立更新日誌失敗: %w", err)
	}
	command := exec.Command(
		m.executablePath,
		"--apply-linux-update", payloadDir,
		"--update-target", m.targetDir,
		"--update-workspace", workspace,
		"--update-parent-pid", strconv.Itoa(os.Getpid()),
	)
	command.Dir = payloadDir
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	detachCommand(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return Status{}, fmt.Errorf("啟動更新程序失敗: %w", err)
	}
	m.helperPID = command.Process.Pid
	_ = command.Process.Release()
	_ = logFile.Close()
	cleanup = false
	succeeded = true
	return status, nil
}

func (m *Manager) statusPath() string {
	return filepath.Join(m.targetDir, "data", statusFilename)
}

func extractAndValidate(archivePath, extractRoot string) (string, string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", "", fmt.Errorf("無法開啟更新 ZIP: %w", err)
	}
	defer reader.Close()
	if len(reader.File) == 0 {
		return "", "", errors.New("更新 ZIP 沒有任何檔案")
	}
	if len(reader.File) > maxArchiveFiles {
		return "", "", errors.New("更新 ZIP 的檔案數量過多")
	}
	if err := os.MkdirAll(extractRoot, 0700); err != nil {
		return "", "", fmt.Errorf("建立解壓縮目錄失敗: %w", err)
	}

	seen := make(map[string]struct{}, len(reader.File))
	topLevel := ""
	var totalBytes uint64
	for _, entry := range reader.File {
		cleanName, rootName, err := validateArchiveName(entry.Name)
		if err != nil {
			return "", "", err
		}
		if topLevel == "" {
			topLevel = rootName
		} else if topLevel != rootName {
			return "", "", errors.New("更新 ZIP 必須只包含一個最上層目錄")
		}
		if _, exists := seen[cleanName]; exists {
			return "", "", fmt.Errorf("更新 ZIP 包含重複路徑: %s", cleanName)
		}
		seen[cleanName] = struct{}{}
		if entry.Flags&1 != 0 {
			return "", "", fmt.Errorf("更新 ZIP 不支援加密檔案: %s", cleanName)
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!entry.FileInfo().IsDir() && !mode.IsRegular()) {
			return "", "", fmt.Errorf("更新 ZIP 包含不支援的檔案類型: %s", cleanName)
		}
		if entry.UncompressedSize64 > maxArchiveEntryBytes {
			return "", "", fmt.Errorf("更新 ZIP 的單一檔案過大: %s", cleanName)
		}
		if totalBytes > maxArchiveTotalBytes-entry.UncompressedSize64 {
			return "", "", errors.New("更新 ZIP 解壓後超過 16 GiB 上限")
		}
		totalBytes += entry.UncompressedSize64

		destination := filepath.Join(extractRoot, filepath.FromSlash(cleanName))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0755); err != nil {
				return "", "", fmt.Errorf("建立更新目錄失敗: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return "", "", fmt.Errorf("建立更新目錄失敗: %w", err)
		}
		source, err := entry.Open()
		if err != nil {
			return "", "", fmt.Errorf("讀取更新檔案失敗: %w", err)
		}
		permissions := mode.Perm() & 0755
		if permissions == 0 {
			permissions = 0644
		}
		destinationFile, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
		if err != nil {
			_ = source.Close()
			return "", "", fmt.Errorf("建立更新檔案失敗: %w", err)
		}
		copied, copyErr := io.Copy(destinationFile, io.LimitReader(source, int64(entry.UncompressedSize64)+1))
		closeDestinationErr := destinationFile.Close()
		closeSourceErr := source.Close()
		if copyErr != nil || closeDestinationErr != nil || closeSourceErr != nil {
			return "", "", fmt.Errorf("解壓縮更新檔案失敗: %s", cleanName)
		}
		if uint64(copied) != entry.UncompressedSize64 {
			return "", "", fmt.Errorf("更新檔案大小不符: %s", cleanName)
		}
	}

	payloadDir := filepath.Join(extractRoot, filepath.FromSlash(topLevel))
	version, err := validatePayload(payloadDir)
	if err != nil {
		return "", "", err
	}
	return payloadDir, version, nil
}

func validateArchiveName(name string) (string, string, error) {
	if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) {
		return "", "", fmt.Errorf("更新 ZIP 包含無效路徑: %q", name)
	}
	cleanName := path.Clean(name)
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
		return "", "", fmt.Errorf("更新 ZIP 包含不安全路徑: %q", name)
	}
	parts := strings.Split(cleanName, "/")
	if len(parts) == 0 || parts[0] == "" || parts[0] == "." || parts[0] == ".." {
		return "", "", fmt.Errorf("更新 ZIP 包含無效路徑: %q", name)
	}
	return cleanName, parts[0], nil
}

func validatePayload(payloadDir string) (string, error) {
	platformBinary := ""
	expectedPlatform := ""
	switch runtime.GOARCH {
	case "amd64":
		platformBinary = "bin/Tanpopo_linux_x64"
		expectedPlatform = "linux-amd64"
	case "arm64":
		platformBinary = "bin/Tanpopo_linux_arm64"
		expectedPlatform = "linux-arm64"
	default:
		return "", fmt.Errorf("ZIP 更新不支援目前 Linux 架構: %s", runtime.GOARCH)
	}
	for _, required := range []string{
		"VERSION",
		"BUILD_INFO.txt",
		"agent.sample.properties",
		"install.sh",
		"run.sh",
		"website/settings.html",
	} {
		if !isRegularFile(filepath.Join(payloadDir, filepath.FromSlash(required))) {
			return "", fmt.Errorf("更新 ZIP 缺少必要檔案: %s", required)
		}
	}
	for _, reserved := range []string{"agent.properties", "data"} {
		if _, err := os.Lstat(filepath.Join(payloadDir, reserved)); err == nil {
			return "", fmt.Errorf("更新 ZIP 不可包含使用者資料路徑: %s", reserved)
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("檢查更新 ZIP 失敗: %w", err)
		}
	}
	buildInfo, err := os.ReadFile(filepath.Join(payloadDir, "BUILD_INFO.txt"))
	if err != nil {
		return "", fmt.Errorf("讀取 BUILD_INFO.txt 失敗: %w", err)
	}
	values := parseBuildInfo(string(buildInfo))
	if values["app"] != "Tanpopo" {
		return "", errors.New("此 ZIP 不是 Tanpopo 發布套件")
	}
	packagePlatform := strings.TrimSpace(values["platform"])
	if packagePlatform != "" && packagePlatform != expectedPlatform {
		return "", fmt.Errorf("更新 ZIP 平台不符: 套件為 %s，目前需要 %s", packagePlatform, expectedPlatform)
	}
	if packagePlatform == expectedPlatform {
		if !isRegularFile(filepath.Join(payloadDir, "Tanpopo")) {
			return "", errors.New("單平台 Linux 更新 ZIP 缺少 Tanpopo 執行檔")
		}
	} else if !isRegularFile(filepath.Join(payloadDir, filepath.FromSlash(platformBinary))) {
		return "", fmt.Errorf("多平台更新 ZIP 缺少目前架構執行檔: %s", platformBinary)
	}
	version := strings.TrimSpace(values["app_display_version"])
	if version == "" {
		versionBytes, readErr := os.ReadFile(filepath.Join(payloadDir, "VERSION"))
		if readErr != nil {
			return "", fmt.Errorf("讀取更新版本失敗: %w", readErr)
		}
		version = strings.TrimSpace(string(versionBytes))
	}
	return version, nil
}

func parseBuildInfo(content string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

func isRegularFile(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular()
}

func readStatus(name string) (Status, error) {
	content, err := os.ReadFile(name)
	if err != nil {
		return Status{}, err
	}
	var status Status
	if err := json.Unmarshal(content, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func writeStatus(name string, status Status) error {
	status.Available = true
	status.UpdatedAt = time.Now()
	content, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(name), ".app-update-status-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(content, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, name)
}
