package netpass

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func fixtureManager(t *testing.T, ctx context.Context) *Manager {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("此子程序 fixture 使用 POSIX Shell")
	}
	root := t.TempDir()
	t.Chdir(root)
	binary := filepath.Join(root, "netpass-client", "NetPassClient")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return &Manager{ctx: ctx, runtimeDir: filepath.Join(root, "runtime"), config: Config{Endpoint: defaultEndpoint, APIKey: "fixture-only"}}
}

func TestStartAfterServiceCancellationIsRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager := fixtureManager(t, ctx)
	defer manager.Stop()
	if _, err := manager.Start(); !errors.Is(err, context.Canceled) {
		t.Fatalf("已停止的服務仍可啟動 NetPass：%v", err)
	}
}

func TestStopWaitsForProcessAndConcurrentRestartPreservesConfig(t *testing.T) {
	manager := fixtureManager(t, context.Background())
	defer manager.Stop()
	for range 12 {
		if _, err := manager.Start(); err != nil {
			t.Fatal(err)
		}
		var wait sync.WaitGroup
		wait.Go(func() {
			if err := manager.Stop(); err != nil {
				t.Error(err)
			}
		})
		wait.Go(func() {
			if _, err := manager.Start(); err != nil {
				t.Error(err)
			}
		})
		wait.Wait()
		if manager.Status().Running {
			if _, err := os.Stat(filepath.Join(manager.runtimeDir, "config.json")); err != nil {
				t.Fatalf("新程序設定被舊 Stop 刪除：%v", err)
			}
		}
		if err := manager.Stop(); err != nil {
			t.Fatal(err)
		}
		manager.mu.Lock()
		done := manager.done
		command := manager.command
		manager.mu.Unlock()
		if command != nil {
			t.Fatal("Stop 返回後仍有程序")
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Stop 未等待程序清理")
		}
	}
}
