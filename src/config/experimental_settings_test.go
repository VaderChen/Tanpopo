package config

import (
	"path/filepath"
	"testing"

	"LlamaLoader/src/domain"
)

func TestExperimentalSettingsAndCalibrationPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.Get()
	settings.AutoCalibrationEnabled = true
	settings.MemoryProtectionEnabled = true
	settings.PerformanceCalibrations = []domain.PerformanceCalibration{{
		Key: "key", Runtime: domain.RuntimeLlamaServer, Model: "model.gguf",
		StartupCommandID: "command", Tuning: domain.PerformanceTuning{Threads: 8},
		Runs: []float64{10, 11, 12}, AverageTokensPerSecond: 11, MedianTokensPerSecond: 11,
	}}
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	actual := reloaded.Get()
	if !actual.AutoCalibrationEnabled || !actual.MemoryProtectionEnabled ||
		len(actual.PerformanceCalibrations) != 1 || actual.PerformanceCalibrations[0].Tuning.Threads != 8 {
		t.Fatalf("實驗性設定未完整持久化：%+v", actual)
	}
}
