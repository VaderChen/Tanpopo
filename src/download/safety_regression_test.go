package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegressionDownloadRejectsParentSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4")
		if r.Method != "HEAD" {
			_, _ = w.Write([]byte("test"))
		}
	}))
	defer srv.Close()
	m := NewManager(1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := m.Start(ctx, Request{Repository: "owner/model", Filename: "model.gguf", ModelDirectory: root, LocalDirectory: "linked", Endpoint: srv.URL})
	if err != nil {
		return
	}
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "model.gguf")); err == nil {
		t.Fatal("下載成功寫入模型根目錄以外的符號連結目的地")
	}
}
