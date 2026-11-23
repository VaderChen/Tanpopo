package appupdate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Windows 的工作目錄和更新日誌在執行時會被鎖住。逐檔移動發行內容，
// 讓已開啟的日誌、自訂設定及可能很大的模型留在原位。
func installPortableFiles(target, payload, backup string, launch func(bool) error) error {
	type change struct {
		destination, previous string
		installed             bool
	}
	var changes []change
	err := filepath.WalkDir(payload, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == payload {
			return err
		}
		relative, err := filepath.Rel(payload, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		info, err := os.Lstat(destination)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if entry.IsDir() {
			if exists && !info.IsDir() {
				return fmt.Errorf("更新目錄與現有路徑衝突: %s", destination)
			}
			if !exists {
				return os.Mkdir(destination, 0755)
			}
			return nil
		}
		item := change{destination: destination}
		if exists {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("更新檔案與現有路徑衝突: %s", destination)
			}
			item.previous = filepath.Join(backup, relative)
			if err := os.MkdirAll(filepath.Dir(item.previous), 0700); err != nil {
				return err
			}
			if err := os.Rename(destination, item.previous); err != nil {
				return err
			}
		}
		changes = append(changes, item)
		if err := os.Rename(path, destination); err != nil {
			return err
		}
		changes[len(changes)-1].installed = true
		return nil
	})
	if err == nil {
		err = launch(true)
		if err == nil {
			return nil
		}
	}
	var restoreErr error
	for i := len(changes) - 1; i >= 0; i-- {
		item := changes[i]
		if item.installed {
			restoreErr = errors.Join(restoreErr, os.Remove(item.destination))
		}
		if item.previous != "" {
			restoreErr = errors.Join(restoreErr, os.Rename(item.previous, item.destination))
		}
	}
	if restoreErr != nil {
		return errors.Join(err, fmt.Errorf("未能完整還原，舊版檔案保存在 %s: %w", backup, restoreErr))
	}
	return errors.Join(err, launch(false))
}
