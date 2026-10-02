package appupdate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"LlamaLoader/src/updatecheck"
)

func prepareAutomaticPayload(p automaticPlan, archive string) (string, error) {
	switch runtime.GOOS {
	case "linux":
		payload, _, err := extractAndValidate(archive, filepath.Join(p.Workspace, "extracted"))
		if err != nil {
			return "", err
		}
		tag, err := payloadReleaseTag(payload)
		if err != nil {
			return "", err
		}
		if comparison, err := updatecheck.CompareVersions(tag, p.Tag); err != nil || comparison != 0 {
			return "", errors.New("ZIP 內版本與官方發布不符")
		}
		installer := updateInstaller(payload)
		if err := installer.Run(); err != nil {
			return "", fmt.Errorf("準備 Linux Runtime 失敗，原服務尚未關閉: %w", err)
		}
		if !isRegularFile(filepath.Join(payload, "Tanpopo")) {
			return "", errors.New("更新套件缺少 Tanpopo 執行檔")
		}
		return payload, nil
	case "darwin":
		return prepareMacBundle(p, archive)
	case "windows":
		return archive, nil
	default:
		return "", errors.New("目前平台不支援自動安裝")
	}
}

func prepareMacBundle(p automaticPlan, archive string) (string, error) {
	mount := filepath.Join(p.Workspace, "mounted")
	if err := os.Mkdir(mount, 0700); err != nil {
		return "", err
	}
	if output, err := exec.Command("/usr/bin/hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mount, archive).CombinedOutput(); err != nil {
		return "", fmt.Errorf("掛載更新 DMG 失敗: %w %s", err, output)
	}
	defer exec.Command("/usr/bin/hdiutil", "detach", mount).Run()
	bundle := filepath.Join(mount, "Tanpopo.app")
	if !isRegularFile(filepath.Join(bundle, "Contents", "MacOS", "Tanpopo")) {
		return "", errors.New("DMG 缺少 Tanpopo.app")
	}
	if err := verifyMacBundle(p.Target, bundle, p.Tag); err != nil {
		return "", err
	}
	payload := filepath.Join(p.Workspace, "Tanpopo.app")
	if output, err := exec.Command("/usr/bin/ditto", bundle, payload).CombinedOutput(); err != nil {
		return "", fmt.Errorf("複製新版 App 失敗: %w %s", err, output)
	}
	// 複製後再確認簽章，不能在寫入 App bundle 後沿用來源的驗證結果。
	if err := verifyMacBundle(p.Target, payload, p.Tag); err != nil {
		return "", err
	}
	return payload, nil
}

func verifyMacBundle(current, candidate, tag string) error {
	if output, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", candidate).CombinedOutput(); err != nil {
		return fmt.Errorf("新版 App 簽章驗證失敗: %w %s", err, output)
	}
	if output, err := exec.Command("/usr/sbin/spctl", "--assess", "--type", "execute", candidate).CombinedOutput(); err != nil {
		return fmt.Errorf("新版 App 未通過 Gatekeeper: %w %s", err, output)
	}
	team := func(bundle string) (string, error) {
		output, err := exec.Command("/usr/bin/codesign", "-d", "--verbose=4", bundle).CombinedOutput()
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "TeamIdentifier=") {
				value := strings.TrimPrefix(line, "TeamIdentifier=")
				if value != "" && value != "not set" {
					return value, nil
				}
			}
		}
		return "", errors.New("已安裝 App 缺少 Developer ID 團隊識別")
	}
	oldTeam, err := team(current)
	if err != nil {
		return err
	}
	newTeam, err := team(candidate)
	if err != nil {
		return err
	}
	if oldTeam != newTeam {
		return errors.New("新版 App 的簽署團隊與目前安裝不符")
	}
	value := func(bundle, key string) (string, error) {
		output, err := exec.Command("/usr/bin/plutil", "-extract", key, "raw", "-o", "-", filepath.Join(bundle, "Contents", "Info.plist")).Output()
		return strings.TrimSpace(string(output)), err
	}
	oldID, err := value(current, "CFBundleIdentifier")
	if err != nil {
		return err
	}
	newID, err := value(candidate, "CFBundleIdentifier")
	if err != nil {
		return err
	}
	if oldID == "" || oldID != newID {
		return errors.New("新版 App 的 Bundle ID 不符")
	}
	version, err := value(candidate, "CFBundleShortVersionString")
	if err != nil {
		return err
	}
	build, err := value(candidate, "CFBundleVersion")
	if err != nil {
		return err
	}
	if comparison, err := updatecheck.CompareVersions(version+"-build-"+build, tag); err != nil || comparison != 0 {
		return errors.New("新版 App 版本與官方發布不符")
	}
	return nil
}

func installAndRestart(p automaticPlan, payload string, status func(string, string) error) error {
	if runtime.GOOS == "windows" {
		if err := installMSI(p, payload); err != nil {
			restartErr := restartAutomatic(p, "")
			return errors.Join(err, restartErr)
		}
		_ = status("restarting", "安裝完成，正在重新啟動程式…")
		return restartAutomatic(p, p.Tag)
	}
	// Linux 使用者資料位於部署目錄內；macOS 資料已位於 Application Support。
	if runtime.GOOS == "linux" {
		paths, err := persistentPaths(p)
		if err != nil {
			return restartAfterFailure(p, err)
		}
		for _, name := range paths {
			source := filepath.Join(p.Target, name)
			if _, err := os.Lstat(source); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return restartAfterFailure(p, err)
			}
			if err := copyPath(source, filepath.Join(payload, name)); err != nil {
				return restartAfterFailure(p, err)
			}
		}
	}
	return swapAndRestart(p.Target, payload, filepath.Join(filepath.Dir(p.Target), ".Tanpopo-backups", automaticBackupName()),
		func(newVersion bool) error {
			tag := ""
			if newVersion {
				tag = p.Tag
			}
			_ = status("restarting", "安裝完成，正在重新啟動程式…")
			return restartAutomatic(p, tag)
		})
}

func persistentPaths(p automaticPlan) ([]string, error) {
	paths := []string{"agent.properties", "data"}
	for _, candidate := range p.PreservePaths {
		if candidate == "" {
			continue
		}
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(p.Directory, candidate)
		}
		relative, err := filepath.Rel(p.Target, candidate)
		if err != nil {
			return nil, err
		}
		if relative == "." {
			return nil, errors.New("使用者資料目錄不可等於程式安裝目錄")
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	var roots []string
	for _, candidate := range paths {
		covered := false
		for _, root := range roots {
			if candidate == root || strings.HasPrefix(candidate, root+string(filepath.Separator)) {
				covered = true
				break
			}
		}
		if !covered {
			roots = append(roots, candidate)
		}
	}
	return roots, nil
}

// swapAndRestart 在舊程序已停止後切換完整目錄；啟動驗證失敗就先停新版再還原。
func swapAndRestart(target, payload, backup string, launch func(bool) error) error {
	failBeforeSwap := func(err error) error { return errors.Join(err, launch(false)) }
	if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
		return failBeforeSwap(err)
	}
	if err := os.Rename(target, backup); err != nil {
		return failBeforeSwap(fmt.Errorf("備份安裝目錄失敗: %w", err))
	}
	if err := os.Rename(payload, target); err != nil {
		if restore := os.Rename(backup, target); restore != nil {
			return errors.Join(err, fmt.Errorf("還原失敗，舊版位於 %s: %w", backup, restore))
		}
		return failBeforeSwap(err)
	}
	if err := launch(true); err != nil {
		failed := payload + ".failed"
		if moveErr := os.Rename(target, failed); moveErr != nil {
			return errors.Join(err, fmt.Errorf("保留失敗新版失敗，舊版位於 %s: %w", backup, moveErr))
		}
		if restore := os.Rename(backup, target); restore != nil {
			return errors.Join(err, fmt.Errorf("還原失敗，舊版位於 %s: %w", backup, restore))
		}
		if restartErr := launch(false); restartErr != nil {
			return errors.Join(err, fmt.Errorf("檔案已還原但舊版啟動失敗: %w", restartErr))
		}
		return fmt.Errorf("新版啟動失敗，已還原並重新啟動舊版: %w", err)
	}
	return nil
}

func restartAfterFailure(p automaticPlan, cause error) error {
	return errors.Join(cause, restartAutomatic(p, ""))
}

func restartAutomatic(p automaticPlan, tag string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command(filepath.Join(p.Target, "Contents", "MacOS", "Tanpopo"), p.Arguments...)
	case "windows":
		command = exec.Command(filepath.Join(p.Target, "Tanpopo.exe"), p.Arguments...)
	default:
		command = exec.Command(filepath.Join(p.Target, "Tanpopo"), p.Arguments...)
	}
	command.Dir = p.Directory
	return launchReadyCommand(command, p.StatusDir, 15*time.Minute, tag)
}

func installMSI(p automaticPlan, archive string) error {
	// 路徑經環境變數傳入，不能插入 PowerShell 程式文字。Windows 原生 UAC 仍由系統處理。
	script := `$ErrorActionPreference='Stop'; try {
  $argsList=@('/i', ('"'+$env:TANPOPO_UPDATE_MSI+'"'), '/passive', '/norestart', ('INSTALLFOLDER="'+$env:TANPOPO_UPDATE_TARGET+'"'), '/L*v', ('"'+$env:TANPOPO_UPDATE_LOG+'"'))
  $p=Start-Process -FilePath "$env:SystemRoot\System32\msiexec.exe" -ArgumentList $argsList -Verb RunAs -Wait -PassThru
  if ($p.ExitCode -eq 0 -or $p.ExitCode -eq 3010) {exit 0}; exit $p.ExitCode
} catch {Write-Error $_; exit 1}`
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "TANPOPO_UPDATE_MSI="+archive, "TANPOPO_UPDATE_TARGET="+p.Target, "TANPOPO_UPDATE_LOG="+filepath.Join(p.StatusDir, "app-update-msi.log"))
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("Windows MSI 安裝失敗或系統授權被取消: %w", err)
	}
	return nil
}
