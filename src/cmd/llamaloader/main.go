package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"LlamaLoader/src/accesscontrol"
	"LlamaLoader/src/api"
	"LlamaLoader/src/config"
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
	)
	downloads := download.NewManager(2)
	llama := llamacpp.NewManager(settings.Get, agentConfig.AccessControlPath)
	handler := api.NewServer(serviceContext, webPath, settings, startupCommands, accessControl, sessions, downloads, llama).Handler()
	address := net.JoinHostPort(agentConfig.HTTPHost, strconv.Itoa(agentConfig.HTTPPort))
	httpServer := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverError := make(chan error, 1)
	go func() {
		log.Printf("%s 已啟動：http://%s", agentConfig.ServiceName, address)
		serverError <- httpServer.ListenAndServe()
	}()

	var listenError error
	select {
	case <-serviceContext.Done():
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			listenError = err
		}
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	_ = llama.Stop(shutdownContext)
	_ = downloads.Wait(shutdownContext)
	if listenError != nil {
		return listenError
	}
	return nil
}
