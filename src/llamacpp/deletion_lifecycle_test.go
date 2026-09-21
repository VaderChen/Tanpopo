package llamacpp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"LlamaLoader/src/domain"
)

func TestDeleteRechecksModelStateAfterWaitingForLifecycleLock(t *testing.T) {
	root := t.TempDir()
	model := filepath.Join(root, "model.gguf")
	writeMinimalGGUF(t, model, "llama", map[string]uint32{"llama.block_count": 1})
	manager := &Manager{settings: func() domain.Settings { return domain.Settings{ModelDirectory: root} }}
	for _, cache := range []bool{false, true} {
		manager.mu.Lock()
		manager.status = domain.LlamaStatus{}
		entered := make(chan struct{})
		result := make(chan error, 1)
		go func() {
			close(entered)
			if cache {
				_, _, err := manager.DeleteGGUFConversionCache(root, "model.gguf")
				result <- err
			} else {
				result <- manager.DeleteStoredModel("gguf", "model.gguf")
			}
		}()
		<-entered
		select {
		case err := <-result:
			manager.mu.Unlock()
			t.Fatalf("刪除未等待生命週期鎖：%v", err)
		case <-time.After(20 * time.Millisecond):
		}
		manager.status = domain.LlamaStatus{Running: true, Runtime: domain.RuntimeLlamaServer, Model: "./model.gguf"}
		manager.mu.Unlock()
		if err := <-result; !errors.Is(err, ErrModelInUse) {
			t.Fatalf("啟動後未重新保護模型：%v", err)
		}
		if _, err := os.Stat(model); err != nil {
			t.Fatal("使用中的模型被刪除")
		}
	}
}
