package llamacpp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStandaloneFastGGUFFallbackAppearsWithoutSource(t *testing.T) {
	root := t.TempDir()
	modelDir := filepath.Join(root, "repository")
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(modelDir, "model.gguf")
	manifestPath := filepath.Join(modelDir, "model.tanpopo-test.fgguf.json")
	manifest := conversionCacheManifest{
		SchemaVersion:          4,
		Key:                    "test-key",
		SourceNames:            []string{"model.gguf"},
		SourcePaths:            []string{sourcePath},
		Shards:                 []string{"model.tanpopo-test-00001-of-00001.fgguf"},
		Profile:                "mode3",
		GroupSize:              32,
		WeightCount:            1,
		Configuration:          "model.tanpopo-test.config.json",
		Tokenizer:              "model.tanpopo-test.tokenizer.json",
		TokenizerConfiguration: "model.tanpopo-test.tokenizer_config.json",
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		filepath.Base(manifestPath):     manifestData,
		manifest.Shards[0]:              []byte("fgguf"),
		manifest.Configuration:          []byte(`{"model_type":"gemma4"}`),
		manifest.Tokenizer:              []byte(`{"model":{}}`),
		manifest.TokenizerConfiguration: []byte(`{"tokenizer_class":"test"}`),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(modelDir, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	models, err := ListMLXRuntimeModels("", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("expected one fallback model, got %d: %#v", len(models), models)
	}
	if models[0].Path != "gguf:repository/model.gguf" || !models[0].FastGGUFFallback {
		t.Fatalf("unexpected fallback model: %#v", models[0])
	}

	resolved, err := resolveFastGGUFFallbackManifest(root, "repository/model.gguf", "mode3")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != manifestPath {
		t.Fatalf("resolved %q, want %q", resolved, manifestPath)
	}
}

func TestStandaloneFastGGUFFallbackRequiresRuntimeAssets(t *testing.T) {
	root := t.TempDir()
	manifest := conversionCacheManifest{
		SchemaVersion: 3,
		Key:           "test-key",
		SourceNames:   []string{"model.gguf"},
		SourcePaths:   []string{filepath.Join(root, "model.gguf")},
		Shards:        []string{"model.fgguf"},
		Profile:       "mode1",
		GroupSize:     64,
		WeightCount:   1,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "model.fgguf.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "model.fgguf"), []byte("fgguf"), 0644); err != nil {
		t.Fatal(err)
	}
	models, err := ListMLXRuntimeModels("", root)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("incomplete Fast GGUF must not be listed: %#v", models)
	}
}
