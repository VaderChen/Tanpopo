package llamacpp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"LlamaLoader/src/domain"
)

func TestEstimateRuntimeMemoryRespondsToContextAndKVQuantization(t *testing.T) {
	modelBytes := uint64(16) * gibibyte
	large := estimateRuntimeMemoryBytes(modelBytes, 0, 0, 131072, 512, domain.KVCacheQuantizationNone)
	small := estimateRuntimeMemoryBytes(modelBytes, 0, 0, 32768, 512, domain.KVCacheQuantizationNone)
	quantized := estimateRuntimeMemoryBytes(modelBytes, 0, 0, 131072, 512, domain.KVCacheQuantizationQ4)
	if !(small < large && quantized < large) {
		t.Fatalf("Context 或 KV 量化未降低預估值：large=%d small=%d quantized=%d", large, small, quantized)
	}
}

func TestMemoryPressureProtectionReducesContextBeforeLaunch(t *testing.T) {
	directory := t.TempDir()
	modelPath := filepath.Join(directory, "model.gguf")
	if err := os.WriteFile(modelPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(modelPath, int64(8*gibibyte)); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{memorySnapshotProvider: func() MemorySnapshot {
		return MemorySnapshot{TotalBytes: 32 * gibibyte, AvailableBytes: 20 * gibibyte}
	}}
	command := domain.StartupCommand{
		Runtime: domain.RuntimeLlamaServer, ContextSize: 131072,
		ExtraArgs: []string{"--batch-size", "512"},
	}
	actual, _, _, result, err := manager.applyMemoryPressureProtectionLocked(
		domain.Settings{ModelDirectory: directory}, "model.gguf", "", "", false, command,
	)
	if err != nil {
		t.Fatal(err)
	}
	if actual.ContextSize >= command.ContextSize || !strings.Contains(result.Actions[0], "Context 已由") {
		t.Fatalf("未依預估降低 Context：command=%+v result=%+v", actual, result)
	}
}

func TestContextCandidatesNeverExceedConfiguredValue(t *testing.T) {
	values := contextCandidates(20000)
	if values[0] != 20000 || values[len(values)-1] != 4096 {
		t.Fatalf("Context 降級順序不正確：%v", values)
	}
	for _, value := range values {
		if value > 20000 {
			t.Fatalf("候選 Context 超過使用者設定：%d", value)
		}
	}
}

func TestWithoutSpeculativeDecodingArguments(t *testing.T) {
	actual := withoutSpeculativeDecodingArguments([]string{
		"--mtp-draft", "draft", "--mtp-block-size=2", "--dflash-draft", "other", "--keep", "value",
	})
	if len(actual) != 2 || actual[0] != "--keep" || actual[1] != "value" {
		t.Fatalf("未完整移除推測解碼參數：%v", actual)
	}
}
