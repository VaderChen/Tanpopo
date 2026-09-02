package llamacpp

import (
	"strconv"
	"strings"

	"LlamaLoader/src/domain"
)

// performanceTuningFromArguments 記錄真正送給 Runtime 的配置（包含記憶體保護調整），
// 讓校準能直接重用已載入的模型，而非把啟動範本誤當成目前配置。
func performanceTuningFromArguments(runtimeName string, arguments []string) domain.PerformanceTuning {
	integer := func(fallback int, names ...string) int {
		value := fallback
		for index, argument := range arguments {
			for _, name := range names {
				raw := ""
				if argument == name && index+1 < len(arguments) {
					raw = arguments[index+1]
				} else if strings.HasPrefix(argument, name+"=") {
					raw = strings.TrimPrefix(argument, name+"=")
				}
				if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
					value = parsed
				}
			}
		}
		return value
	}
	if runtimeName == domain.RuntimeMLXServer {
		return domain.PerformanceTuning{PrefillStepSize: integer(512, "--prefill-step-size")}
	}
	return domain.PerformanceTuning{
		Threads:    integer(0, "--threads", "-t"),
		BatchSize:  integer(0, "--batch-size", "-b"),
		UBatchSize: integer(0, "--ubatch-size", "-ub"),
	}
}

// MarkSavedPerformanceCalibration 只有保存結果與目前執行配置一致時才標記，無須重啟。
func (m *Manager) MarkSavedPerformanceCalibration(model, commandID string, tuning domain.PerformanceTuning) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Running && m.status.Model == model && m.status.StartupCommandID == commandID && m.status.PerformanceTuning == tuning {
		m.status.PerformanceCalibrationApplied = true
	}
}
