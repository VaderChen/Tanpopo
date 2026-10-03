package modelbundle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileModelsTransferOnlySelectedAssets(t *testing.T) {
	for _, fast := range []bool{false, true} {
		t.Run(map[bool]string{false: "GGUF", true: "FastGGUF"}[fast], func(t *testing.T) {
			source, target := t.TempDir(), t.TempDir()
			files := map[string]string{"target.gguf": "weights", "mmproj.gguf": "vision", "other.gguf": "unrelated", "tokenizer.json": "{}"}
			entry := "target.gguf"
			auxiliary := []string{"mmproj.gguf"}
			if fast {
				entry = "target.fgguf.json"
				auxiliary = nil
				files[entry] = `{"shards":["target.fgguf"],"configuration":"target.config.json","tokenizer":"tokenizer.json"}`
				files["target.fgguf"], files["target.config.json"] = "converted weights", "{}"
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := CreateSelection(context.Background(), source, entry, auxiliary, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range snapshot.Manifest.Files {
				if file.Path == "other.gguf" || (fast && strings.HasSuffix(file.Path, ".gguf")) {
					t.Fatalf("多傳了未選模型：%s", file.Path)
				}
			}
			model, err := Ensure(context.Background(), target, entry, snapshot.Manifest,
				func(_ context.Context, index int) (io.ReadCloser, error) { return snapshot.Open(index) }, nil)
			if err != nil || filepath.Base(model) != entry {
				t.Fatalf("下載入口不符：%s %v", model, err)
			}
			if _, err := Ensure(context.Background(), target, entry, snapshot.Manifest,
				func(context.Context, int) (io.ReadCloser, error) {
					t.Fatal("應重用下載的完整副本")
					return nil, nil
				}, nil); err != nil {
				t.Fatal(err)
			}
			changed := snapshot.Manifest
			changed.Entry = "other.gguf"
			if !fast {
				changed.Entry = "mmproj.gguf"
			}
			if changed.Validate() == nil {
				t.Fatal("不可更換入口而沿用摘要")
			}
			if fast {
				bad, _ := json.Marshal(map[string]any{"shards": []string{"../outside.fgguf"}})
				if err := os.WriteFile(filepath.Join(source, entry), bad, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := Create(context.Background(), source, entry, nil); err == nil {
					t.Fatal("接受路徑跳脫")
				}
			}
		})
	}
}

func fixture(t *testing.T, root, name, weights string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
		t.Fatal(err)
	}
	for file, data := range map[string]string{"config.json": `{"model_type":"fixture"}`, "model.safetensors": weights, "tokenizer.json": "{}"} {
		if err := os.WriteFile(filepath.Join(root, name, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotReusesIdenticalContentAcrossPathsAndReplacesDifferentWeights(t *testing.T) {
	ctx := context.Background()
	source, target := t.TempDir(), t.TempDir()
	fixture(t, source, "selected", "original weights")
	snapshot, err := Create(ctx, source, "selected", nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture(t, target, "renamed-model", "original weights")
	count := 0
	fetch := func(_ context.Context, index int) (io.ReadCloser, error) { count++; return snapshot.Open(index) }
	model, err := Ensure(ctx, target, "selected", snapshot.Manifest, fetch, nil)
	if err != nil || model != "renamed-model" || count != 0 {
		t.Fatalf("相同內容應重用：%s %d %v", model, count, err)
	}
	if err := os.RemoveAll(filepath.Join(target, "renamed-model")); err != nil {
		t.Fatal(err)
	}
	fixture(t, target, "selected", "modified weights") // 設定、權重檔大小相同，內容不同。
	var downloading bool
	model, err = Ensure(ctx, target, "selected", snapshot.Manifest, fetch, func(p Progress) { downloading = downloading || p.Phase == "downloading" })
	if err != nil || model == "selected" || !downloading || !Matches(ctx, target, model, snapshot.Manifest, nil) {
		t.Fatalf("應下載隔離副本：%s %v", model, err)
	}
	original, _ := os.ReadFile(filepath.Join(target, "selected/model.safetensors"))
	if string(original) != "modified weights" {
		t.Fatal("不應覆寫原模型")
	}
	if _, err := Ensure(ctx, target, "selected", snapshot.Manifest, func(context.Context, int) (io.ReadCloser, error) { t.Fatal("應重用快取"); return nil, nil }, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadMismatchAndCancellationNeverPublishIncompleteModel(t *testing.T) {
	source := t.TempDir()
	fixture(t, source, "selected", strings.Repeat("x", 2<<20))
	snapshot, err := Create(context.Background(), source, "selected", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cancelTransfer := range []bool{false, true} {
		t.Run(map[bool]string{false: "內容被更換", true: "取消傳輸"}[cancelTransfer], func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := Ensure(ctx, root, "selected", snapshot.Manifest, func(_ context.Context, index int) (io.ReadCloser, error) {
				if cancelTransfer {
					return snapshot.Open(index)
				}
				return io.NopCloser(strings.NewReader("invalid")), nil
			}, func(p Progress) {
				if cancelTransfer && p.Phase == "downloading" && p.BytesDone > 1<<20 {
					cancel()
				}
			})
			if err == nil {
				t.Fatal("不應接受不完整模型")
			}
			if cancelTransfer && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 0 {
				t.Fatalf("留下未驗證副本：%v", entries)
			}
		})
	}
}

func TestManifestAndModelCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "selected", "weights")
	snapshot, err := Create(context.Background(), root, "selected", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret.json", "/secret.json", "dir\\secret.json", ".cache/secret.json", "C:secret.json"} {
		m := snapshot.Manifest
		m.Files = append([]File(nil), m.Files...)
		m.Files[0].Path = name
		m.Digest = digest(m.Files)
		if m.Validate() == nil {
			t.Fatalf("接受危險路徑：%s", name)
		}
	}
	out := t.TempDir()
	fixture(t, out, "foreign", "secret")
	if err := os.Symlink(filepath.Join(out, "foreign/model.safetensors"), filepath.Join(root, "selected/outside.safetensors")); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), root, "selected", nil); err == nil {
		t.Fatal("不應讀取模型根目錄外的連結")
	}
}
