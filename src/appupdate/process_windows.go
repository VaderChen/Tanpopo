//go:build windows

package appupdate

import (
	"errors"
	"os/exec"
	"strconv"
	"syscall"
)

// 正式更新透過主程序 Shutdown 回呼關閉；這裡只用於清理啟動失敗的新實例。
func terminateProcess(pid int) error {
	return killProcessGroup(pid)
}

func processAlive(pid int) bool {
	h, err := syscall.OpenProcess(0x1000|syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, syscall.ERROR_ACCESS_DENIED)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	return syscall.GetExitCodeProcess(h, &code) == nil && code == 259
}

func detachCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200 | 0x00000008, HideWindow: true}
}

func killProcessGroup(pid int) error {
	if pid <= 1 {
		return errors.New("無效的程序群組")
	}
	return exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}
