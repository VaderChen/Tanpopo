package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"LlamaLoader/src/accesscontrol"
	"LlamaLoader/src/api"
	"LlamaLoader/src/appupdate"
	"LlamaLoader/src/appversion"
	"LlamaLoader/src/config"
	"LlamaLoader/src/desktopui"
	"LlamaLoader/src/download"
	"LlamaLoader/src/llamacpp"
	"LlamaLoader/src/session"
	"LlamaLoader/src/startupcommand"
)

func main() {
	agentDefault, sampleDefault, err := packagedConfigPaths()
	if err != nil {
		log.Fatal(err)
	}
	agentPath := flag.String("config", agentDefault, "服務設定檔路徑")
	samplePath := flag.String("sample-config", sampleDefault, "預設設定範本路徑")
	applyUpdatePayload := flag.String("apply-linux-update", "", "內部使用：套用 Linux ZIP 更新內容")
	updateTarget := flag.String("update-target", "", "內部使用：目前安裝目錄")
	updateWorkspace := flag.String("update-workspace", "", "內部使用：更新暫存目錄")
	updateParentPID := flag.Int("update-parent-pid", 0, "內部使用：等待結束的主服務 PID")
	flag.Parse()

	if strings.TrimSpace(*applyUpdatePayload) != "" {
		if err := appupdate.Apply(appupdate.ApplyOptions{
			PayloadDir: *applyUpdatePayload,
			TargetDir:  *updateTarget,
			Workspace:  *updateWorkspace,
			ParentPID:  *updateParentPID,
		}); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := run(*agentPath, *samplePath); err != nil {
		log.Fatal(err)
	}
}

// packagedConfigPaths 將已安裝的 macOS App 資源與可寫入的使用者資料分離。
// 開發模式仍沿用專案根目錄下的相對路徑；.app 內的簽章資源不會被修改。
func packagedConfigPaths() (agentPath, samplePath string, err error) {
	agentPath = "agent.properties"
	samplePath = "agent.sample.properties"
	executable, executableErr := os.Executable()
	if executableErr != nil {
		return agentPath, samplePath, nil
	}
	executableDirectory := filepath.Dir(executable)
	if runtime.GOOS != "darwin" || filepath.Base(executableDirectory) != "MacOS" {
		return agentPath, samplePath, nil
	}
	contentsDirectory := filepath.Dir(executableDirectory)
	if filepath.Ext(filepath.Dir(contentsDirectory)) != ".app" || filepath.Base(contentsDirectory) != "Contents" {
		return agentPath, samplePath, nil
	}
	resourcesDirectory := filepath.Join(contentsDirectory, "Resources")
	if info, statErr := os.Stat(filepath.Join(resourcesDirectory, "agent.sample.properties")); statErr != nil || !info.Mode().IsRegular() {
		return agentPath, samplePath, nil
	}
	configDirectory, configErr := os.UserConfigDir()
	if configErr != nil {
		return "", "", fmt.Errorf("取得使用者設定目錄失敗: %w", configErr)
	}
	applicationDirectory := filepath.Join(configDirectory, "Tanpopo")
	if mkdirErr := os.MkdirAll(applicationDirectory, 0700); mkdirErr != nil {
		return "", "", fmt.Errorf("建立 Tanpopo 使用者資料目錄失敗: %w", mkdirErr)
	}
	if linkErr := ensureResourceLink(
		filepath.Join(applicationDirectory, "website"),
		filepath.Join(resourcesDirectory, "website"),
	); linkErr != nil {
		return "", "", linkErr
	}
	if chdirErr := os.Chdir(applicationDirectory); chdirErr != nil {
		return "", "", fmt.Errorf("切換 Tanpopo 使用者資料目錄失敗: %w", chdirErr)
	}
	return filepath.Join(applicationDirectory, "agent.properties"),
		filepath.Join(resourcesDirectory, "agent.sample.properties"), nil
}

func ensureResourceLink(linkPath, targetPath string) error {
	info, err := os.Lstat(linkPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		currentTarget, readErr := os.Readlink(linkPath)
		if readErr == nil && filepath.Clean(currentTarget) == filepath.Clean(targetPath) {
			return nil
		}
		if removeErr := os.Remove(linkPath); removeErr != nil {
			return fmt.Errorf("更新管理介面資源連結失敗: %w", removeErr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("檢查管理介面資源連結失敗: %w", err)
	}
	if symlinkErr := os.Symlink(targetPath, linkPath); symlinkErr != nil {
		return fmt.Errorf("建立管理介面資源連結失敗: %w", symlinkErr)
	}
	return nil
}

func run(agentPath, samplePath string) error {
	if err := config.EnsureAgentConfig(agentPath, samplePath); err != nil {
		return err
	}
	agentConfig, err := config.LoadAgentConfig(agentPath)
	if err != nil {
		return err
	}
	agentConfigPath, err := filepath.Abs(agentPath)
	if err != nil {
		return err
	}
	settings, err := config.NewStore(agentConfig.SettingsPath)
	if err != nil {
		return fmt.Errorf("載入模型設定失敗: %w", err)
	}
	startupCommands, err := startupcommand.NewStore(
		agentConfig.StartupCommandsPath,
		startupcommand.DefaultFromSettings(settings.Get()),
	)
	if err != nil {
		return fmt.Errorf("載入啟動參數失敗: %w", err)
	}
	accessControl, err := accesscontrol.NewStore(agentConfig.AccessControlPath)
	if err != nil {
		return fmt.Errorf("載入模型 API 安全設定失敗: %w", err)
	}
	webPath, err := filepath.Abs(agentConfig.WebPath)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(webPath); statErr != nil || !info.IsDir() {
		return fmt.Errorf("網站目錄無法讀取: %s", webPath)
	}

	serviceContext, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	sessions := session.NewStore(
		agentConfig.DefaultAccount,
		agentConfig.DefaultPassword,
		time.Duration(agentConfig.SessionHours)*time.Hour,
		!agentConfig.DisableAuthentication,
	)
	downloads := download.NewManager(2)
	llama, err := llamacpp.NewManager(settings.Get, agentConfig.AccessControlPath, agentConfig.RuntimeStatePath)
	if err != nil {
		return fmt.Errorf("載入模型服務狀態失敗: %w", err)
	}
	handler := api.NewServer(serviceContext, webPath, agentConfigPath, settings, startupCommands, accessControl, sessions, downloads, llama).Handler()
	address := net.JoinHostPort(agentConfig.HTTPHost, strconv.Itoa(agentConfig.HTTPPort))
	httpServer := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("管理服務無法監聽 %s: %w", address, err)
	}
	if attempted, restoreErr := llama.Restore(startupCommands.Get); attempted {
		if restoreErr != nil {
			log.Printf("自動恢復模型服務失敗：%v", restoreErr)
		} else {
			log.Printf("已自動恢復上次執行中的模型服務")
		}
	}

	serverError := make(chan error, 1)
	go func() {
		log.Printf("%s 已啟動：http://%s", agentConfig.ServiceName, address)
		serverError <- httpServer.Serve(listener)
	}()

	uiDone, uiLaunched, uiErr := desktopui.Launch(serviceContext, desktopui.Options{
		URL:       localManagementURL(agentConfig.HTTPHost, agentConfig.HTTPPort),
		Title:     agentConfig.ServiceName,
		Resident:  settings.Get().ResidentMode,
		Version:   appversion.Current(),
		APIURL:    llama.Status().URL,
		GitHubURL: appversion.RepositoryURL(),
	})
	if uiErr != nil {
		log.Printf("原生 UI 無法啟動，改用 Shell 模式：%v", uiErr)
	} else if uiLaunched {
		if settings.Get().ResidentMode {
			log.Printf("已開啟原生管理視窗；常駐模式已啟用")
		} else {
			log.Printf("已開啟原生管理視窗；關閉視窗會停止服務")
		}
	} else {
		log.Printf("目前為 CLI／無圖形工作階段，維持 Shell 模式")
	}

	var listenError error

waitForShutdown:
	for {
		select {
		case <-serviceContext.Done():
			break waitForShutdown
		case err := <-serverError:
			if !errors.Is(err, http.ErrServerClosed) {
				listenError = err
			}
			break waitForShutdown
		case err, ok := <-uiDone:
			uiDone = nil
			if ok && err != nil && serviceContext.Err() == nil {
				log.Printf("原生 UI 已結束，服務繼續以 Shell 模式執行：%v", err)
				continue
			}
			if serviceContext.Err() == nil {
				log.Printf("原生管理視窗已關閉，正在停止服務")
				stopSignal()
			}
			break waitForShutdown
		}
	}
	stopSignal()

	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	_ = llama.Shutdown(shutdownContext)
	_ = downloads.Wait(shutdownContext)
	if listenError != nil {
		return listenError
	}
	return nil
}

func localManagementURL(host string, port int) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/",
	}).String()
}
