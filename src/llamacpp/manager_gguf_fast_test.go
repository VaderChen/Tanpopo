package llamacpp

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"LlamaLoader/src/domain"
)

func TestWithManagedMLXGGUFOptimizationUsesQualityDefaults(t *testing.T) {
	got := withManagedMLXGGUFOptimization(
		[]string{"--temperature", "0.2", "--gguf-profile", "auto", "--gguf-group-size=64"},
		true,
		false,
	)
	want := []string{
		"--temperature", "0.2",
		"--gguf-profile", "auto",
		"--gguf-group-size", "32",
		"--gguf-recurrent-promotion", "off",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("預設 GGUF 策略不符：got %v want %v", got, want)
	}
}

func TestWithManagedMLXGGUFOptimizationUsesFastProfile(t *testing.T) {
	got := withManagedMLXGGUFOptimization(nil, true, true)
	want := []string{
		"--gguf-profile", "speed",
		"--gguf-group-size", "auto",
		"--gguf-recurrent-promotion", "controls",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("快速 GGUF 策略不符：got %v want %v", got, want)
	}
}

func TestWithManagedMLXGGUFOptimizationStripsFlagsForNativeMLX(t *testing.T) {
	got := withManagedMLXGGUFOptimization(
		[]string{
			"--gguf-profile=speed",
			"--gguf-group-size", "64",
			"--gguf-recurrent-promotion=controls",
			"--thinking",
		},
		false,
		false,
	)
	want := []string{"--thinking"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("原生 MLX 殘留 GGUF 參數：got %v want %v", got, want)
	}
}

func TestWithoutMLXGGUFCacheArguments(t *testing.T) {
	got := withoutMLXGGUFCacheArguments([]string{
		"--gguf-cache-dir", "/tmp/old",
		"--gguf-cache-dir=/tmp/older",
		"--thinking",
	})
	want := []string{"--thinking"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GGUF 快取參數未正確收斂：got %v want %v", got, want)
	}
}

func TestResolveMLXTargetSelectionAttachesQwen35CompanionMMProj(t *testing.T) {
	modelRoot := t.TempDir()
	modelDirectory := filepath.Join(modelRoot, "Qwen3.5-4B-GGUF")
	if err := os.MkdirAll(modelDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	writeMinimalGGUF(
		t,
		filepath.Join(modelDirectory, "Qwen3.5-4B-Q4_0.gguf"),
		"qwen35",
		map[string]uint32{"qwen35.block_count": 32},
	)
	writeMinimalGGUF(t, filepath.Join(modelDirectory, "mmproj-F16.gguf"), "clip", nil)

	selection, err := resolveMLXTargetSelection(
		domain.Settings{ModelDirectory: modelRoot},
		"gguf:Qwen3.5-4B-GGUF/Qwen3.5-4B-Q4_0.gguf",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(modelDirectory, "mmproj-F16.gguf")
	if selection.mmprojArgument != wantPath {
		t.Fatalf("未掛載同目錄 mmproj：got %q want %q", selection.mmprojArgument, wantPath)
	}
	if selection.statusMMProj != "gguf:Qwen3.5-4B-GGUF/mmproj-F16.gguf" {
		t.Fatalf("mmproj 狀態路徑錯誤：%q", selection.statusMMProj)
	}
}

func TestRuntimeOutputWriterReassemblesMarkerLines(t *testing.T) {
	var destination bytes.Buffer
	lines := make([]string, 0, 2)
	writer := newRuntimeOutputWriter(&destination, func(line string) {
		lines = append(lines, line)
	})
	if _, err := writer.Write([]byte("TANPOPO_GGUF_CACHE state=mis")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("s\nnext line\n")); err != nil {
		t.Fatal(err)
	}
	wantLines := []string{"TANPOPO_GGUF_CACHE state=miss", "next line"}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Fatalf("Runtime 輸出行重組錯誤：got %v want %v", lines, wantLines)
	}
	if destination.String() != "TANPOPO_GGUF_CACHE state=miss\nnext line\n" {
		t.Fatalf("Runtime 原始日誌內容不符：%q", destination.String())
	}
}
