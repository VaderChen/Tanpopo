package appupdate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const readyPathEnv = "TANPOPO_UPDATE_READY_PATH"
const readyTokenEnv = "TANPOPO_UPDATE_READY_TOKEN"

type readyRecord struct {
	PID   int    `json:"pid"`
	Token string `json:"token"`
}

// ReportReady 由新實例在完成初始化、綁定埠及實際回應 health 後呼叫。
func ReportReady(managementURL string) error {
	path, token := os.Getenv(readyPathEnv), os.Getenv(readyTokenEnv)
	if path == "" && token == "" {
		return nil
	}
	if path == "" || token == "" {
		return errors.New("更新啟動確認設定不完整")
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Get(managementURL + "api/health")
	if err != nil {
		return fmt.Errorf("新版健康檢查失敗: %w", err)
	}
	defer response.Body.Close()
	var health struct {
		Status string `json:"status"`
	}
	if response.StatusCode != http.StatusOK {
		return errors.New("新版健康檢查未成功")
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health); err != nil || health.Status != "ok" {
		return errors.New("新版健康檢查內容無效")
	}
	data, err := json.Marshal(readyRecord{PID: os.Getpid(), Token: token})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".ready-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func launchTarget(targetDir string) error {
	return launchTargetWithTimeout(targetDir, 45*time.Second)
}

func launchTargetWithTimeout(targetDir string, timeout time.Duration) error {
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
	defer logFile.Close()
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	token := hex.EncodeToString(nonce)
	readyPath := filepath.Join(logDir, ".update-ready-"+token+".json")
	defer os.Remove(readyPath)
	command := exec.Command(runScript)
	command.Dir = targetDir
	command.Env = append(os.Environ(), readyPathEnv+"="+readyPath, readyTokenEnv+"="+token)
	command.Stdout, command.Stderr = logFile, logFile
	detachCommand(command)
	if err := command.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	failed := func(cause error) error {
		// 即使啟動中的新版已產生 Runtime，也先結束整個新實例，才允許切回舊版。
		_ = terminateProcess(command.Process.Pid)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
		}
		_ = killProcessGroup(command.Process.Pid)
		return cause
	}
	for {
		select {
		case err := <-exited:
			_ = killProcessGroup(command.Process.Pid)
			return fmt.Errorf("新版在啟動確認前結束: %v", err)
		case <-timer.C:
			return failed(errors.New("新版未在期限內通過健康檢查，已停止新版"))
		case <-ticker.C:
			data, err := os.ReadFile(readyPath)
			if err != nil {
				continue
			}
			var ready readyRecord
			if len(data) > 4096 || json.Unmarshal(data, &ready) != nil || ready.PID != command.Process.Pid || ready.Token != token {
				continue
			}
			// 保留一小段觀察期，捕捉回報 ready 後立即崩潰的初始化錯誤。
			select {
			case err := <-exited:
				_ = killProcessGroup(command.Process.Pid)
				return fmt.Errorf("新版健康檢查後立即結束: %v", err)
			case <-time.After(200 * time.Millisecond):
				return nil
			}
		}
	}
}
