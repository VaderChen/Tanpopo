package appupdate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"LlamaLoader/src/appversion"
)

const automaticStatusFilename = "automatic-update-status.json"

// AutomaticOptions 僅由主程序提供；HTTP 請求不能指定來源、路徑或重啟命令。
type AutomaticOptions struct {
	Shutdown      func()
	ConfigPath    string
	SamplePath    string
	PreservePaths func() []string
}

type Automatic struct {
	mu                                                 sync.Mutex
	ctx                                                context.Context
	options                                            AutomaticOptions
	source                                             releaseSource
	executable, target, directory, statusDir, platform string
	active                                             bool
}

type automaticPlan struct {
	Target        string       `json:"target"`
	Workspace     string       `json:"workspace"`
	StatusDir     string       `json:"status_dir"`
	Directory     string       `json:"directory"`
	Arguments     []string     `json:"arguments"`
	PreservePaths []string     `json:"preserve_paths,omitempty"`
	ParentPID     int          `json:"parent_pid"`
	LockFD        int          `json:"lock_fd"`
	Platform      string       `json:"platform"`
	Tag           string       `json:"tag"`
	Token         string       `json:"token"`
	Asset         releaseAsset `json:"asset"`
}

func installedTarget(executable, platform string) string {
	root := filepath.Dir(executable)
	// 原始碼工作區即使手動編譯成正式檔名，也不能被安裝更新覆寫。
	if isRegularFile(filepath.Join(root, "go.mod")) {
		return ""
	}
	switch {
	case strings.HasPrefix(platform, "darwin-"):
		contents := filepath.Dir(root)
		app := filepath.Dir(contents)
		if filepath.Base(executable) == "Tanpopo" && filepath.Base(root) == "MacOS" && filepath.Base(contents) == "Contents" && filepath.Ext(app) == ".app" && isRegularFile(filepath.Join(contents, "Info.plist")) {
			return app
		}
	case strings.HasPrefix(platform, "windows-"):
		if strings.EqualFold(filepath.Base(executable), "Tanpopo.exe") && isRegularFile(filepath.Join(root, "agent.sample.properties")) && isRegularFile(filepath.Join(root, "website", "settings.html")) {
			return root
		}
	case strings.HasPrefix(platform, "linux-"):
		if filepath.Base(executable) == "Tanpopo" && isRegularFile(filepath.Join(root, "run.sh")) && isRegularFile(filepath.Join(root, "BUILD_INFO.txt")) {
			return root
		}
	}
	return ""
}

func NewAutomatic(ctx context.Context, options AutomaticOptions) *Automatic {
	a := &Automatic{ctx: ctx, options: options, source: officialSource(), platform: runtime.GOOS + "-" + runtime.GOARCH}
	a.executable, _ = os.Executable()
	a.executable, _ = filepath.EvalSymlinks(a.executable)
	a.target = installedTarget(a.executable, a.platform)
	a.directory, _ = os.Getwd()
	if options.ConfigPath != "" {
		a.statusDir = filepath.Join(filepath.Dir(options.ConfigPath), "data")
	}
	return a
}

func automaticActive(state string) bool {
	switch state {
	case "checking", "downloading", "verifying", "preparing", "stopping", "installing", "restarting":
		return true
	}
	return false
}

func (a *Automatic) available() bool {
	return a.target != "" && a.statusDir != "" && a.options.Shutdown != nil
}
func (a *Automatic) statusPath() string { return filepath.Join(a.statusDir, automaticStatusFilename) }

func (a *Automatic) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.available() {
		return Status{State: "unavailable", Message: "自動更新僅適用已安裝的發行版本。"}
	}
	status, err := readStatus(a.statusPath())
	if err != nil {
		status = Status{State: "idle"}
	}
	if !a.active && automaticActive(status.State) {
		if lock, err := acquireUpdateLock(a.target, 0); err == nil {
			lock.Close()
			status.State = "failed"
			status.Message = "更新程序已停止，請檢查更新日誌後重試。"
			_ = writeStatus(a.statusPath(), status)
		}
	}
	status.Available = true
	status.AutomaticAvailable = true
	return status
}

func (a *Automatic) Start() (Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.available() {
		return Status{}, errors.New("自動更新僅適用已安裝的發行版本。")
	}
	if a.active {
		return Status{}, errUpdateInProgress
	}
	lock, err := acquireUpdateLock(a.target, 0)
	if err != nil {
		return Status{}, err
	}
	status := Status{State: "checking", Available: true, AutomaticAvailable: true, Message: "正在取得官方更新套件…"}
	if err := writeStatus(a.statusPath(), status); err != nil {
		lock.Close()
		return Status{}, err
	}
	a.active = true
	go func() {
		defer lock.Close()
		err := a.prepare(lock)
		if err != nil {
			_ = writeStatus(a.statusPath(), Status{State: "failed", Message: err.Error()})
		}
		a.mu.Lock()
		a.active = false
		a.mu.Unlock()
	}()
	return status, nil
}

func (a *Automatic) prepare(lock *os.File) error {
	release, asset, err := a.source.latest(a.ctx, a.platform, appversion.Tag())
	if err != nil {
		return err
	}
	workspaceRoot := filepath.Dir(a.target)
	if runtime.GOOS == "windows" {
		workspaceRoot = a.statusDir
	}
	if err := os.MkdirAll(workspaceRoot, 0700); err != nil {
		return err
	}
	workspace, err := os.MkdirTemp(workspaceRoot, ".tanpopo-update-")
	if err != nil {
		return fmt.Errorf("無法在安裝目錄旁建立更新暫存，請確認目錄寫入權限: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(workspace)
		}
	}()
	status := Status{State: "downloading", Message: "正在自動下載更新…", Version: release.Tag, TotalBytes: asset.Size}
	if err := writeStatus(a.statusPath(), status); err != nil {
		return err
	}
	archive := filepath.Join(workspace, asset.Name)
	err = a.source.download(a.ctx, asset, archive, func(n int64) { status.DownloadedBytes = n; _ = writeStatus(a.statusPath(), status) })
	if err != nil {
		return err
	}
	status.State = "verifying"
	status.Message = "下載完成，正在驗證更新套件…"
	if err := writeStatus(a.statusPath(), status); err != nil {
		return err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	plan := automaticPlan{Target: a.target, Workspace: workspace, StatusDir: a.statusDir, Directory: a.directory,
		Arguments: []string{"-config", a.options.ConfigPath, "-sample-config", a.options.SamplePath},
		ParentPID: os.Getpid(), Platform: a.platform, Tag: release.Tag, Token: hex.EncodeToString(nonce), Asset: asset}
	if a.options.PreservePaths != nil {
		plan.PreservePaths = a.options.PreservePaths()
	}
	if runtime.GOOS != "windows" {
		plan.LockFD = 3
	}
	planPath := filepath.Join(workspace, "plan.json")
	encoded, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if err := os.WriteFile(planPath, encoded, 0600); err != nil {
		return err
	}
	helper, err := stageUpdateHelper(a.executable, a.target, workspace, runtime.GOOS)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(a.statusDir, "app-update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(helper, "--apply-app-update", planPath)
	cmd.Dir = workspace
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if plan.LockFD != 0 {
		cmd.ExtraFiles = []*os.File{lock}
	}
	detachCommand(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("啟動獨立更新程序失敗: %w", err)
	}
	if runtime.GOOS == "windows" {
		lock.Close()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(45 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case err := <-done:
			state, _ := readStatus(a.statusPath())
			if state.State == "failed" {
				return errors.New(state.Message)
			}
			return fmt.Errorf("更新程序在關閉程式前結束: %v", err)
		case <-a.ctx.Done():
			_ = killProcessGroup(cmd.Process.Pid)
			<-done
			return a.ctx.Err()
		case <-timer.C:
			_ = killProcessGroup(cmd.Process.Pid)
			<-done
			return errors.New("更新準備逾時，原程式保持執行")
		case <-ticker.C:
			content, err := os.ReadFile(filepath.Join(workspace, "handoff.json"))
			if err != nil {
				continue
			}
			var record readyRecord
			if len(content) > 4096 || json.Unmarshal(content, &record) != nil || record.PID != cmd.Process.Pid || record.Token != plan.Token {
				continue
			}
			// Helper 已驗證套件並持有鎖，之後由主程式正常清理模型、叢集及 UI。
			keep = true
			a.options.Shutdown()
			return nil
		}
	}
}

func stageUpdateHelper(executable, target, workspace, platform string) (string, error) {
	if platform == "darwin" {
		// 已簽章的主執行檔需要原 Info.plist 與資源封印，不能脫離 App 複製執行。
		bundle := filepath.Join(workspace, "Updater.app")
		if output, err := exec.Command("/usr/bin/ditto", target, bundle).CombinedOutput(); err != nil {
			return "", fmt.Errorf("建立更新助手 App 失敗: %w %s", err, output)
		}
		return filepath.Join(bundle, "Contents", "MacOS", "Tanpopo"), nil
	}
	helper := filepath.Join(workspace, "TanpopoUpdater")
	if platform == "windows" {
		helper += ".exe"
	}
	return helper, copyRegularFile(executable, helper, 0700)
}

// RunAutomaticHelper 的入口只接受本機主程序建立的更新計畫，不能由 API 直接呼叫。
func RunAutomaticHelper(planPath string) (result error) {
	data, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	var p automaticPlan
	if len(data) > 64<<10 || json.Unmarshal(data, &p) != nil {
		return errors.New("更新計畫無效")
	}
	if p.Platform != runtime.GOOS+"-"+runtime.GOARCH || p.ParentPID <= 1 || len(p.Token) != 64 || filepath.Dir(planPath) != p.Workspace || !strings.HasPrefix(filepath.Base(p.Workspace), ".tanpopo-update-") {
		return errors.New("更新計畫與執行環境不符")
	}
	for _, path := range []string{p.Target, p.Workspace, p.StatusDir, p.Directory} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errors.New("更新計畫路徑無效")
		}
	}
	if runtime.GOOS != "windows" && filepath.Dir(p.Workspace) != filepath.Dir(p.Target) {
		return errors.New("更新暫存與安裝目錄必須位於相同檔案系統")
	}
	var lock *os.File
	for attempt := 0; attempt < 50; attempt++ {
		lock, err = acquireUpdateLock(p.Target, p.LockFD)
		if err == nil || runtime.GOOS != "windows" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	statusPath := filepath.Join(p.StatusDir, automaticStatusFilename)
	defer func() {
		if result != nil {
			_ = writeStatus(statusPath, Status{State: "failed", Message: result.Error(), Version: p.Tag})
		}
	}()
	setStatus := func(state, message string) error {
		return writeStatus(statusPath, Status{State: state, Message: message, Version: p.Tag})
	}
	if err := setStatus("verifying", "下載完成，正在驗證更新套件…"); err != nil {
		return err
	}
	source := officialSource()
	release, err := source.release(context.Background(), p.Tag)
	if err != nil {
		return err
	}
	asset, err := selectReleaseAsset(release, p.Platform)
	if err != nil {
		return err
	}
	if asset != p.Asset {
		return errors.New("官方發布內容在下載後已變更，請重新更新")
	}
	if err := source.validateAssetURL(asset, p.Tag); err != nil {
		return err
	}
	archive := filepath.Join(p.Workspace, asset.Name)
	if err := verifyDownloadedAsset(archive, asset); err != nil {
		return err
	}
	if err := setStatus("preparing", "正在準備安裝更新…"); err != nil {
		return err
	}
	payload, err := prepareAutomaticPayload(p, archive)
	if err != nil {
		return err
	}
	if err := setStatus("stopping", "即將關閉程式並安裝更新…"); err != nil {
		return err
	}
	record, _ := json.Marshal(readyRecord{PID: os.Getpid(), Token: p.Token})
	if err := os.WriteFile(filepath.Join(p.Workspace, "handoff.json"), record, 0600); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for processAlive(p.ParentPID) && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if processAlive(p.ParentPID) {
		return errors.New("原程式未能正常關閉，已取消安裝")
	}
	if err := setStatus("installing", "程式已關閉，正在安裝更新…"); err != nil {
		return restartAfterFailure(p, err)
	}
	if err := installAndRestart(p, payload, setStatus); err != nil {
		return err
	}
	if err := setStatus("completed", "更新完成，程式已重新啟動。"); err != nil {
		return err
	}
	// Windows 仍在執行的 Helper 不能刪除自身；其餘暫存於下次啟動前保留供查核。
	if runtime.GOOS != "windows" {
		_ = os.RemoveAll(p.Workspace)
	}
	return nil
}

func automaticBackupName() string {
	return time.Now().UTC().Format("20060102-150405") + "-" + strconv.Itoa(os.Getpid())
}
