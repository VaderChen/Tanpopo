package api

import (
	"reflect"
	"testing"

	"LlamaLoader/src/domain"
	"LlamaLoader/src/systemmetrics"
)

func TestPerformanceCalibrationIdentitySeparatesModelsAndCommands(t *testing.T) {
	info := systemmetrics.SystemInfo{
		Platform: "linux", Architecture: "amd64", CPUModel: "test", LogicalCores: 16,
		GPUModel: "gpu", MemoryBytes: 64 * 1024 * 1024 * 1024,
	}
	command := domain.StartupCommand{
		ID: "default", Runtime: domain.RuntimeLlamaServer, ContextSize: 8192,
		ExtraArgs: []string{"--batch-size", "512"},
	}
	first, _, _ := performanceCalibrationIdentity(info, "a.gguf", command)
	second, _, _ := performanceCalibrationIdentity(info, "b.gguf", command)
	if first == second {
		t.Fatal("不同模型不應共用校準 key")
	}
	command.ContextSize = 16384
	third, _, _ := performanceCalibrationIdentity(info, "a.gguf", command)
	if first == third {
		t.Fatal("不同啟動參數不應共用校準 key")
	}
}

func TestApplyPerformanceTuningReplacesManagedArguments(t *testing.T) {
	command := domain.StartupCommand{
		Runtime:   domain.RuntimeLlamaServer,
		ExtraArgs: []string{"--batch-size=2048", "--ubatch-size", "512", "--other", "value"},
	}
	actual := applyPerformanceTuning(command, domain.PerformanceTuning{
		Threads: 8, BatchSize: 512, UBatchSize: 128,
	})
	expected := []string{"--other", "value", "--batch-size", "512", "--ubatch-size", "128"}
	if actual.Threads != 8 || !reflect.DeepEqual(actual.ExtraArgs, expected) {
		t.Fatalf("校準參數未正確取代：%+v", actual)
	}
}

func TestPerformanceCalibrationCandidatesAlwaysReturnThree(t *testing.T) {
	info := systemmetrics.SystemInfo{PhysicalCores: 8, LogicalCores: 16}
	for _, command := range []domain.StartupCommand{
		{Runtime: domain.RuntimeLlamaServer, Threads: 8, ExtraArgs: []string{"--batch-size", "512", "--ubatch-size", "128"}},
		{Runtime: domain.RuntimeMLXServer, ExtraArgs: []string{"--prefill-step-size", "512"}},
	} {
		if candidates := performanceCalibrationCandidates(info, command); len(candidates) != 3 {
			t.Fatalf("%s 應產生 3 組候選設定，實際為 %d", command.Runtime, len(candidates))
		}
	}
}
