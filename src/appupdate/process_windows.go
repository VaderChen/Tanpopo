//go:build windows

package appupdate

import (
	"errors"
	"os/exec"
)

// ZIP 自動更新只會在正式 Linux 安裝中啟用。Windows 實作保留編譯契約，
// 避免未啟用的 Linux 更新功能阻斷跨平台發行建置。
func terminateProcess(_ int) error {
	return errors.New("ZIP 更新程序僅能在 Linux 執行")
}

func processAlive(_ int) bool {
	return false
}

func detachCommand(_ *exec.Cmd) {}
