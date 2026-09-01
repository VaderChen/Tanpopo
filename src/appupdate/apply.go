package appupdate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Apply 由脫離主服務的舊版 Tanpopo 子程序執行。它先在暫存發布目錄完成
// Runtime 安裝，成功後才停止目前服務並原子切換應用程式目錄。
func Apply(options ApplyOptions) error {
	if runtime.GOOS != "linux" {
		return errors.New("ZIP 更新程序僅能在 Linux 執行")
	}
	payloadDir, err := filepath.Abs(options.PayloadDir)
	if err != nil {
		return err
	}
	targetDir, err := filepath.Abs(options.TargetDir)
	if err != nil {
		return err
	}
	workspace, err := filepath.Abs(options.Workspace)
	if err != nil {
		return err
	}
	if options.ParentPID <= 1 {
		return errors.New("更新程序缺少有效的主服務 PID")
	}
	if filepath.Dir(workspace) != filepath.Dir(targetDir) {
		return errors.New("更新暫存目錄必須與安裝目錄位於相同檔案系統")
	}
	relativePayload, err := filepath.Rel(workspace, payloadDir)
	if err != nil || relativePayload == "." || relativePayload == ".." || strings.HasPrefix(relativePayload, ".."+string(filepath.Separator)) {
		return errors.New("更新內容不在核准的暫存目錄內")
	}
	version, err := validatePayload(payloadDir)
	if err != nil {
		return err
	}
	statusPath := filepath.Join(targetDir, "data", statusFilename)
	fail := func(cause error) error {
		_ = writeStatus(statusPath, Status{
			State:   "failed",
			Message: cause.Error(),
			Version: version,
		})
		return cause
	}

	if err := writeStatus(statusPath, Status{
		State:   "preparing",
		Message: "正在安裝更新套件與 Runtime。",
		Version: version,
	}); err != nil {
		return err
	}
	installer := exec.Command(filepath.Join(payloadDir, "install.sh"))
	installer.Dir = payloadDir
	installer.Stdin = nil
	installer.Stdout = os.Stdout
	installer.Stderr = os.Stderr
	if err := installer.Run(); err != nil {
		return fail(fmt.Errorf("更新套件安裝失敗: %w", err))
	}
	if !isRegularFile(filepath.Join(payloadDir, "Tanpopo")) {
		return fail(errors.New("更新套件安裝後缺少 Tanpopo 執行檔"))
	}
	if err := writeStatus(statusPath, Status{
		State:   "restarting",
		Message: "更新已準備完成，正在重新啟動服務。",
		Version: version,
	}); err != nil {
		return err
	}

	if err := terminateProcess(options.ParentPID); err != nil {
		return fail(fmt.Errorf("無法停止目前服務: %w", err))
	}
	deadline := time.Now().Add(90 * time.Second)
	for processAlive(options.ParentPID) && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
	}
	if processAlive(options.ParentPID) {
		return fail(errors.New("目前服務未能在 90 秒內停止，已取消更新切換"))
	}
	failAfterStop := func(cause error) error {
		result := fail(cause)
		_ = launchTarget(targetDir)
		return result
	}

	for _, persistent := range []string{"agent.properties", "data"} {
		source := filepath.Join(targetDir, persistent)
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return failAfterStop(fmt.Errorf("讀取既有使用者資料失敗: %w", err))
		}
		if err := copyPath(source, filepath.Join(payloadDir, persistent)); err != nil {
			return failAfterStop(fmt.Errorf("保存既有使用者資料失敗: %w", err))
		}
	}

	backupRoot := filepath.Join(filepath.Dir(targetDir), ".Tanpopo-backups")
	if err := os.MkdirAll(backupRoot, 0700); err != nil {
		return failAfterStop(fmt.Errorf("建立更新備份目錄失敗: %w", err))
	}
	backupName := time.Now().Format("20060102-150405") + "-zip-update"
	backupDir := filepath.Join(backupRoot, backupName)
	if _, err := os.Lstat(backupDir); err == nil {
		backupDir += "-" + strconv.Itoa(os.Getpid())
	}
	if err := os.Rename(targetDir, backupDir); err != nil {
		return failAfterStop(fmt.Errorf("備份目前安裝失敗: %w", err))
	}
	if err := os.Rename(payloadDir, targetDir); err != nil {
		_ = os.Rename(backupDir, targetDir)
		return failAfterStop(fmt.Errorf("切換新版安裝失敗: %w", err))
	}

	newStatusPath := filepath.Join(targetDir, "data", statusFilename)
	if err := launchTarget(targetDir); err != nil {
		failedDir := targetDir + ".failed-" + strconv.Itoa(os.Getpid())
		_ = os.Rename(targetDir, failedDir)
		if rollbackErr := os.Rename(backupDir, targetDir); rollbackErr == nil {
			_ = writeStatus(filepath.Join(targetDir, "data", statusFilename), Status{
				State:   "failed",
				Message: "新版啟動失敗，已還原舊版：" + err.Error(),
				Version: version,
			})
			_ = launchTarget(targetDir)
		}
		return err
	}
	if err := writeStatus(newStatusPath, Status{
		State:   "completed",
		Message: "ZIP 更新完成，服務已重新啟動。",
		Version: version,
	}); err != nil {
		return err
	}
	_ = os.RemoveAll(workspace)
	return nil
}

func launchTarget(targetDir string) error {
	runScript := filepath.Join(targetDir, "run.sh")
	if !isRegularFile(runScript) {
		return errors.New("更新後缺少 run.sh")
	}
	logDir := filepath.Join(targetDir, "data")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "app-update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	command := exec.Command(runScript)
	command.Dir = targetDir
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	detachCommand(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = command.Process.Release()
	return logFile.Close()
}

func copyPath(source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("目的路徑已存在: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	return filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := destination
		if relative != "." {
			target = filepath.Join(destination, relative)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			linkTarget, err := os.Readlink(current)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		case info.Mode().IsRegular():
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			return copyRegularFile(current, target, info.Mode().Perm())
		default:
			return fmt.Errorf("不支援的使用者資料檔案類型: %s", current)
		}
	})
}

func copyRegularFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
