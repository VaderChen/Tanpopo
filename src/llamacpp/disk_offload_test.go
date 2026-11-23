package llamacpp

import (
	"reflect"
	"strings"
	"testing"

	"LlamaLoader/src/domain"
)

func TestDiskOffloadOptionsValidateBeforeMemoryBypass(t *testing.T) {
	cases := []struct {
		runtime string
		args    []string
		enabled bool
		cache   int
		invalid bool
	}{
		{domain.RuntimeMLXServer, []string{"--disk-cache-mb", "1024"}, true, 1024, false},
		{domain.RuntimeMLXServer, []string{"--disk-cache-mb", "0"}, false, 0, false},
		{domain.RuntimeMLXServer, []string{"--disk-cache-mb", "32"}, false, 0, true},
		{domain.RuntimeMLXServer, []string{"--disk-cache-mb", "1024", "--disk-cache-mb", "0"}, false, 0, false},
		{domain.RuntimeMLXServer, []string{"--disk-cache-mb=1024"}, false, 0, true},
		{domain.RuntimeMLXServer, []string{"--disk-cache-mb", "-1"}, false, 0, true},
		{domain.RuntimeMLXServer, []string{"--disk-cache-mb"}, false, 0, true},
		{domain.RuntimeMLXServer, []string{"--disk-cache-mb", "1024", "--distributed-config", "nodes.json"}, false, 0, true},
		{domain.RuntimeLlamaServer, []string{"--tanpopo-disk-offload"}, true, 0, false},
		{domain.RuntimeLlamaServer, []string{"--disk-cache-mb", "1024"}, false, 0, true},
	}
	for _, tc := range cases {
		enabled, cache, err := diskOffloadMode(domain.StartupCommand{Runtime: tc.runtime, ExtraArgs: tc.args})
		if (err != nil) != tc.invalid || enabled != tc.enabled || (!tc.invalid && cache != tc.cache) {
			t.Fatalf("%v: enabled=%v cache=%d err=%v", tc.args, enabled, cache, err)
		}
	}
}
func TestLlamaDiskOffloadStripsAppFlagAndForcesLazyReads(t *testing.T) {
	got := withLlamaDiskOffloadArguments([]string{"--tanpopo-disk-offload", "--tensor-read-lazy=off", "--parallel", "1", "--no-warmup"})
	want := []string{"--parallel", "1", "--tensor-read-lazy", "on", "--no-warmup"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
func TestDiskOffloadChecksCacheBudgetWithoutRequiringResidentModel(t *testing.T) {
	manager := &Manager{memorySnapshotProvider: func() MemorySnapshot { return MemorySnapshot{TotalBytes: 16 * gibibyte, AvailableBytes: 8 * gibibyte} }}
	command := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 8192, ExtraArgs: []string{"--disk-cache-mb", "1024"}}
	_, _, _, result, err := manager.applyMemoryPressureProtectionLocked(domain.Settings{}, "oversized-model", "", "", false, command)
	if err != nil || len(result.Actions) == 0 || !strings.Contains(result.Actions[0], "磁碟") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	command.ExtraArgs = []string{"--disk-cache-mb", "8192"}
	if _, _, _, _, err = manager.applyMemoryPressureProtectionLocked(domain.Settings{}, "oversized-model", "", "", false, command); err == nil {
		t.Fatal("超過記憶體的快取應拒絕啟動")
	}
}
