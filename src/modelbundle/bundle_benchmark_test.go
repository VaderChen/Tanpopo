package modelbundle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 使用真實檔案及完整 SHA-256；小檔案數與目錄數固定，讓前後版可重複比較。
func benchmarkBundle(b *testing.B) (string, Manifest) {
	b.Helper()
	root := b.TempDir()
	directory := filepath.Join(root, "model")
	if err := os.Mkdir(directory, 0700); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("asset-%02d.json", i)
		if i == 0 {
			name = "config.json"
		}
		if err := os.WriteFile(filepath.Join(directory, name), []byte(`{"model_type":"llama"}`), 0600); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("weight-%02d.safetensors", i)), make([]byte, 256*1024), 0600); err != nil {
			b.Fatal(err)
		}
	}
	snapshot, err := Create(context.Background(), root, "model", nil)
	if err != nil {
		b.Fatal(err)
	}
	return root, snapshot.Manifest
}

func BenchmarkCreateModelManifest(b *testing.B) {
	root, _ := benchmarkBundle(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Create(context.Background(), root, "model", nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatchModelManifest(b *testing.B) {
	root, manifest := benchmarkBundle(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !Matches(context.Background(), root, "model", manifest, nil) {
			b.Fatal("內容不一致")
		}
	}
}

func BenchmarkEnsureExistingModel(b *testing.B) {
	root, manifest := benchmarkBundle(b)
	for i := 0; i < 256; i++ {
		directory := filepath.Join(root, fmt.Sprintf("unrelated-%03d", i))
		if err := os.Mkdir(directory, 0700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "config.json"), []byte(`{}`), 0600); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if value, err := Ensure(context.Background(), root, "model", manifest, nil, nil); err != nil || value != "model" {
			b.Fatalf("既有模型未重用：%q %v", value, err)
		}
	}
}
