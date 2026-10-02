//go:build windows

package appupdate

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var errUpdateInProgress = errors.New("已有更新正在進行")

func acquireUpdateLock(target string, inherited int) (*os.File, error) {
	if inherited != 0 {
		return nil, errors.New("Windows 更新不接受繼承描述符")
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	root = filepath.Join(root, "Tanpopo", "update-locks")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	name := filepath.Join(root, fmt.Sprintf("%x.lock", sha256.Sum256([]byte(strings.ToLower(abs)))))
	path, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, syscall.Errno(32)) { // ERROR_SHARING_VIOLATION
		return nil, errUpdateInProgress
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), name), nil
}
