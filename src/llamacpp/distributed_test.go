package llamacpp

import (
	"strings"
	"testing"

	"LlamaLoader/src/domain"
)

func TestDistributedMemoryProtectionKeepsSystemReserve(t *testing.T) {
	manager := &Manager{memorySnapshotProvider: func() MemorySnapshot {
		return MemorySnapshot{TotalBytes: 64 * gibibyte, AvailableBytes: gibibyte}
	}}
	command := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 32768,
		ExtraArgs: []string{"--distributed-config", "/cluster.json"}}
	_, _, _, _, err := manager.applyMemoryPressureProtectionLocked(domain.Settings{}, "model", "", "", false, command)
	if err == nil || !strings.Contains(err.Error(), "系統保留量") {
		t.Fatalf("分散式模式仍需保留本機系統記憶體：%v", err)
	}
}

func TestDistributedMemoryBudgetIsDelegatedWithoutChangingContext(t *testing.T) {
	manager := &Manager{memorySnapshotProvider: func() MemorySnapshot {
		return MemorySnapshot{TotalBytes: 64 * gibibyte, AvailableBytes: 40 * gibibyte}
	}}
	command := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 32768,
		ExtraArgs: []string{"--distributed-config", "/cluster.json"}}
	actual, _, _, result, err := manager.applyMemoryPressureProtectionLocked(domain.Settings{}, "model", "", "", false, command)
	if err != nil || actual.ContextSize != command.ContextSize || len(result.Actions) != 1 || result.EstimatedBytes != 0 {
		t.Fatalf("不得以完整檔案大小估計分片或宣稱未計算的預算：%+v %v", result, err)
	}
}
