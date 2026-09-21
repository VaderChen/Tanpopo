//go:build !windows

package appupdate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateLockSurvivesManagerRestartAndDirectorySwap(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "Tanpopo")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireUpdateLock(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := os.Rename(target, target+".backup"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	restarted := &Manager{available: true, targetDir: target}
	if _, err := restarted.Start(nil); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("重啟後未阻止重疊更新：%v", err)
	}
	lock.Close()
	next, err := acquireUpdateLock(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

func TestInheritedUpdateLockHelper(t *testing.T) {
	target := os.Getenv("TANPOPO_LOCK_TEST_TARGET")
	if target == "" {
		return
	}
	lock, err := acquireUpdateLock(target, 3)
	if err != nil {
		os.Exit(2)
	}
	defer lock.Close()
	if err := os.WriteFile(target+".ready", []byte("ready"), 0600); err != nil {
		os.Exit(3)
	}
	// stdin 關閉才釋放，讓父程序測試其描述符關閉後仍受互斥保護。
	buffer := make([]byte, 1)
	_, _ = os.Stdin.Read(buffer)
	os.Exit(0)
}

func TestUpdateLockTransfersToHelper(t *testing.T) {
	target := filepath.Join(t.TempDir(), "Tanpopo")
	lock, err := acquireUpdateLock(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestInheritedUpdateLockHelper$")
	command.Env = append(os.Environ(), "TANPOPO_LOCK_TEST_TARGET="+target)
	command.ExtraFiles = []*os.File{lock}
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	defer input.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(target + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Helper 未就緒")
		}
		time.Sleep(10 * time.Millisecond)
	}
	lock.Close()
	if unexpected, err := acquireUpdateLock(target, 0); !errors.Is(err, errUpdateInProgress) {
		if unexpected != nil {
			unexpected.Close()
		}
		t.Fatalf("父程序關閉描述符後鎖已失效：%v", err)
	}
	input.Close()
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	next, err := acquireUpdateLock(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

func TestStatusRecoversAfterHelperExitAcrossManagerRestart(t *testing.T) {
	target := filepath.Join(t.TempDir(), "Tanpopo")
	manager := &Manager{available: true, targetDir: target}
	if err := writeStatus(manager.statusPath(), Status{State: "restarting"}); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireUpdateLock(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	if status := manager.Status(); status.State != "restarting" {
		t.Fatalf("更新尚未結束卻被標為失敗：%+v", status)
	}
	lock.Close()
	if status := manager.Status(); status.State != "failed" {
		t.Fatalf("Helper 結束後仍永久顯示更新中：%+v", status)
	}
}
