package download

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"LlamaLoader/src/domain"
)

func TestGGUFArtifactClassification(t *testing.T) {
	for name, want := range map[string]bool{
		"Model-Q4.gguf":                       true,
		"Mainline_Support/Model-Q4.gguf_file": true,
		"Model.GGUF_FILE":                     true,
		"imatrix.gguf":                        false,
		"imatrix/Model.gguf":                  false,
		"Model-imatrix.gguf":                  false,
		"mmproj-Model.gguf_file":              false,
		"MTP/Model.gguf_file":                 false,
		"../Model.gguf_file":                  false,
		"Model.gguf_file.exe":                 false,
	} {
		if got := isPrimaryGGUFFilename(name); got != want {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
}

// 驗證遠端別名、分片與附屬檔走完整下載流程；HTTP URL 不可被本機改名污染。
func TestGGUFAliasDownloadWithSplitAndProjector(t *testing.T) {
	names := []string{
		"Mainline_Support/Model-Q4-00001-of-00002.gguf_file",
		"Mainline_Support/Model-Q4-00002-of-00002.gguf_file",
		"mmproj-Model-BF16.gguf_file",
	}
	info := repositoryInfo{}
	for _, name := range append(append([]string{}, names...), "imatrix.gguf") {
		info.Siblings = append(info.Siblings, struct {
			Filename string `json:"rfilename"`
		}{name})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/models/fixture/model/revision/main" {
			_ = json.NewEncoder(w).Encode(info)
			return
		}
		for _, name := range names {
			if r.URL.Path == "/fixture/model/resolve/main/"+name {
				_, _ = w.Write([]byte("GGUF fixture: " + name))
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	manager := NewManager(2)
	request := Request{Runtime: domain.RuntimeLlamaServer, Repository: "fixture/model", Filename: names[0], Revision: "main", Endpoint: server.URL, ModelDirectory: t.TempDir()}
	files, err := manager.ListGGUFFiles(context.Background(), request)
	if err != nil || len(files) != 2 || files[0] != names[0] {
		t.Fatalf("主模型選單錯誤：%v, %v", files, err)
	}
	batch, err := manager.StartWithCompanions(context.Background(), request)
	if err != nil || len(batch.Warnings) > 0 || len(batch.Jobs) != 3 {
		t.Fatalf("下載計畫錯誤：%+v, %v", batch, err)
	}
	if err := manager.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, job := range batch.Jobs {
		if filepath.Ext(job.Destination) != ".gguf" {
			t.Fatalf("未正規化本機副檔名：%s", job.Destination)
		}
		data, err := os.ReadFile(job.Destination)
		if err != nil || !strings.HasSuffix(string(data), ".gguf_file") {
			t.Fatalf("未下載原始遠端檔案：%s, %s, %v", job.Destination, data, err)
		}
	}
}
