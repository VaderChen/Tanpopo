//go:build windows

package appupdate

import (
	"errors"
	"os"
)

var errUpdateInProgress = errors.New("已有 ZIP 更新正在進行")

func acquireUpdateLock(_ string, _ int) (*os.File, error) {
	return nil, errors.New("ZIP 更新程序僅能在 Linux 執行")
}
