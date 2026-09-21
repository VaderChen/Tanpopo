package llamacpp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"LlamaLoader/src/config"
	"LlamaLoader/src/domain"
)

func TestRegressionDeletePreservesSiblingModels(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"family/model-a/a.gguf", "family/model-b/b.gguf"} {
		p := filepath.Join(root, n)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		writeMinimalGGUF(t, p, "llama", map[string]uint32{"llama.block_count": 1})
	}
	if err := DeleteStoredModel("", root, "gguf", "family/model-a/a.gguf"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "family/model-b/b.gguf")); err != nil {
		t.Fatalf("刪除 A 時，另一個模型 B 也被刪除：%v", err)
	}
}

func TestRegressionMLXUnquantizedContextIsForwarded(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("MLX 僅適用 macOS Apple Silicon")
	}
	root := t.TempDir()
	t.Chdir(root)
	bin := filepath.Join(root, "mlx-server", "prebuilt", runtimePlatform(), "bin")
	resources := filepath.Join(bin, "mlx-swift_Cmlx.bundle", "Contents", "Resources")
	if err := os.MkdirAll(resources, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, "default.metallib"), []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mlx-server"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	modelRoot := filepath.Join(root, "models")
	model := filepath.Join(modelRoot, "fixture")
	if err := os.MkdirAll(model, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(model, "config.json"), []byte("{\"model_type\":\"llama\",\"architectures\":[\"LlamaForCausalLM\"],\"max_position_embeddings\":131072}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(model, "model.safetensors"), []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	s := config.DefaultSettings()
	s.MLXModelDirectory = modelRoot
	m, err := NewManager(func() domain.Settings { return s }, filepath.Join(root, "access.json"), filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ServerHost: "127.0.0.1", ServerPort: 54321, ContextSize: 512, KVCacheQuantization: domain.KVCacheQuantizationNone}
	m.mu.Lock()
	_, err = m.startMLXLocked(s, "fixture", "", false, false, false, c)
	var args []string
	if err == nil {
		args = append(args, m.cmd.Args...)
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer m.Shutdown(ctx)
	if !strings.Contains(strings.Join(args, " "), "--context-size 512") || hasAnyArgument(args, "--max-kv-size") {
		t.Fatalf("ContextSize=512 未出現在 MLX 啟動參數：%v", args)
	}
}

func TestRegressionMemoryGuardUsesEffectiveKVMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("此程序 fixture 使用 POSIX Shell")
	}
	root := t.TempDir()
	t.Chdir(root)
	bin := filepath.Join(root, "llama-server", "prebuilt", runtimePlatform(), "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "llama-server"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(root, "model.gguf")
	if err := os.WriteFile(model, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(model, int64(8*gibibyte)); err != nil {
		t.Fatal(err)
	}
	s := config.DefaultSettings()
	s.ModelDirectory = root
	s.MemoryProtectionEnabled = true
	m, err := NewManager(func() domain.Settings { return s }, filepath.Join(root, "access.json"), filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.SetMemorySnapshotProvider(func() MemorySnapshot { return MemorySnapshot{TotalBytes: 32 * gibibyte, AvailableBytes: 20 * gibibyte} })
	c := domain.StartupCommand{ID: "review", Name: "review", Runtime: domain.RuntimeLlamaServer, ServerHost: "127.0.0.1", ServerPort: 54321, ContextSize: 131072, KVCacheQuantization: domain.KVCacheQuantizationQ4}
	status, err := m.Start("model.gguf", "", "", false, false, false, false, false, "", c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer m.Shutdown(ctx)
	expected := estimateRuntimeMemoryBytes(8*gibibyte, 0, 0, status.EffectiveContextSize, 512, domain.KVCacheQuantizationNone)
	if expected != status.EstimatedMemoryBytes {
		t.Fatalf("KV 已關閉，卻以 Q4 估算：context=%d, estimated=%d, actualModeEstimate=%d, available=%d", status.EffectiveContextSize, status.EstimatedMemoryBytes, expected, status.AvailableMemoryBytes)
	}
}
