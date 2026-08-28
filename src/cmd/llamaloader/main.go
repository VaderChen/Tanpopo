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
	"strconv"
	"strings"
	"syscall"
	"time"

	"LlamaLoader/src/accesscontrol"
	"LlamaLoader/src/api"
	"LlamaLoader/src/config"
	"LlamaLoader/src/desktopui"
	"LlamaLoader/src/download"
	"LlamaLoader/src/llamacpp"
	"LlamaLoader/src/session"
	"LlamaLoader/src/startupcommand"
)

func main() {
	agentPath := flag.String("config", "agent.properties", "服務設定檔路徑")
	samplePath := flag.String("sample-config", "agent.sample.properties", "預設設定範本路徑")
	flag.Parse()

	if err := run(*agentPath, *samplePath); err != nil {
		log.Fatal(err)
	}
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
		URL:      localManagementURL(agentConfig.HTTPHost, agentConfig.HTTPPort),
		Title:    agentConfig.ServiceName,
		Resident: settings.Get().ResidentMode,
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
