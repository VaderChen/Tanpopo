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
		domain.FastGGUFStrategyMode1,
	)
	want := []string{
		"--temperature", "0.2",
		"--gguf-profile", "auto",
		"--gguf-group-size", "auto",
		"--gguf-recurrent-promotion", "off",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("預設 GGUF 策略不符：got %v want %v", got, want)
	}
}

func TestWithManagedMLXGGUFOptimizationUsesDefaultFastStrategy(t *testing.T) {
	got := withManagedMLXGGUFOptimization(nil, true, true, domain.FastGGUFStrategyMode1)
	want := []string{
		"--gguf-profile", "mode1",
		"--gguf-group-size", "auto",
		"--gguf-recurrent-promotion", "controls",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("預設快速 GGUF 策略不符：got %v want %v", got, want)
	}
}

// Mode 2 是保守路徑；舊的 default 值必須沿用同一行為，
// 而已捨棄的 beta1／beta2 一律併入預設的 Mode 1。
func TestWithManagedMLXGGUFOptimizationResolvesStrategies(t *testing.T) {
	tests := []struct {
		name     string
		strategy string
		profile  string
	}{
		{name: "Mode 1", strategy: domain.FastGGUFStrategyMode1, profile: "mode1"},
		{name: "Mode 2", strategy: domain.FastGGUFStrategyMode2, profile: "mode2"},
		{name: "舊 default 對應 Mode 2", strategy: domain.FastGGUFStrategyLegacyDefault, profile: "mode2"},
		{name: "舊 beta1 併入 Mode 1", strategy: domain.FastGGUFStrategyLegacyBeta1, profile: "mode1"},
		{name: "舊 beta2 併入 Mode 1", strategy: domain.FastGGUFStrategyLegacyBeta2, profile: "mode1"},
		{name: "Mode 3", strategy: domain.FastGGUFStrategyMode3, profile: "mode3"},
		{name: "未知值退回 Mode 1", strategy: "unknown", profile: "mode1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := withManagedMLXGGUFOptimization(nil, true, true, test.strategy)
			want := []string{
				"--gguf-profile", test.profile,
				"--gguf-group-size", "auto",
				"--gguf-recurrent-promotion", "controls",
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s 策略不符：got %v want %v", test.name, got, want)
			}
		})
	}
}

func TestWithManagedMLXGGUFOptimizationStripsFlagsForNativeMLX(t *testing.T) {
	got := withManagedMLXGGUFOptimization(
		[]string{
			"--gguf-profile=mode1",
			"--gguf-group-size", "64",
			"--gguf-recurrent-promotion=controls",
			"--thinking",
		},
		false,
		false,
		domain.FastGGUFStrategyMode1,
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

func TestResolveMLXTargetSelectionAllowsTextOnlyQwen35WithoutMMProj(t *testing.T) {
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

	selection, err := resolveMLXTargetSelection(
		domain.Settings{ModelDirectory: modelRoot},
		"gguf:Qwen3.5-4B-GGUF/Qwen3.5-4B-Q4_0.gguf",
		"",
	)
	if err != nil {
		t.Fatalf("純文字 Qwen3.5 不應因缺少 mmproj 被拒絕：%v", err)
	}
	if selection.mmprojArgument != "" || selection.statusMMProj != "" {
		t.Fatalf("缺少 mmproj 時應維持純文字模式：%+v", selection)
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
