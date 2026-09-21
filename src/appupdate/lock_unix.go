//go:build !windows

package appupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var errUpdateInProgress = errors.New("已有 ZIP 更新正在進行")

// 鎖檔位於安裝目錄外，切換或還原目錄不會改變鎖的 inode。
// Helper 繼承相同 open file description；只關閉自己的描述符，不能主動 LOCK_UN。
// 鎖檔不可刪除，否則另一程序可能在同一路徑建立新 inode 繞過互斥。
func acquireUpdateLock(targetDir string, inheritedFD int) (*os.File, error) {
	absolute, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	path := filepath.Join(parent, "."+filepath.Base(absolute)+".update.lock")
	var file *os.File
	if inheritedFD != 0 {
		if inheritedFD < 3 {
			return nil, errors.New("更新鎖描述符無效")
		}
		file = os.NewFile(uintptr(inheritedFD), path)
		if file == nil {
			return nil, errors.New("更新鎖描述符無效")
		}
		info, statErr := file.Stat()
		expected, pathErr := os.Lstat(path)
		if statErr != nil || pathErr != nil || !info.Mode().IsRegular() || !expected.Mode().IsRegular() || !os.SameFile(info, expected) {
			file.Close()
			return nil, errors.New("繼承的更新鎖與安裝目錄不符")
		}
		syscall.CloseOnExec(inheritedFD)
	} else {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return nil, fmt.Errorf("開啟更新鎖失敗: %w", err)
		}
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errUpdateInProgress
		}
		return nil, fmt.Errorf("取得更新鎖失敗: %w", err)
	}
	return file, nil
}
