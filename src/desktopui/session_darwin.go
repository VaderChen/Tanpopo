//go:build darwin

package desktopui

import (
	"os"
	"syscall"
)

func hasGraphicalSession() bool {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	console, err := os.Stat("/dev/console")
	if err != nil {
		return false
	}
	stat, ok := console.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
