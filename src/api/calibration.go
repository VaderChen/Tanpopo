package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"LlamaLoader/src/domain"
	"LlamaLoader/src/systemmetrics"
)

type calibrationCandidate struct {
	Label  string                   `json:"label"`
	Tuning domain.PerformanceTuning `json:"tuning"`
}

type calibrationPlan struct {
	Enabled    bool                           `json:"enabled"`
	Key        string                         `json:"key"`
	Calibrated bool                           `json:"calibrated"`
	Profile    *domain.PerformanceCalibration `json:"profile,omitempty"`
	Candidates []calibrationCandidate         `json:"candidates"`
}

func (s *Server) handleRuntimeCalibrationPlan(w http.ResponseWriter, r *http.Request) {
	model := filepath.ToSlash(strings.TrimSpace(r.URL.Query().Get("model")))
	commandID := strings.TrimSpace(r.URL.Query().Get("startup_command_id"))
	if model == "" || commandID == "" {
		writeError(w, http.StatusBadRequest, errors.New("model 與 startup_command_id 不可為空"))
		return
	}
	command, err := s.startupCommands.Get(commandID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings := s.settings.Get()
	key, hardwareFingerprint, commandFingerprint := performanceCalibrationIdentity(
		s.metrics.Info(), model, command,
	)
	profile, calibrated := findPerformanceCalibration(settings.PerformanceCalibrations, key)
	if calibrated {
		profile.HardwareFingerprint = hardwareFingerprint
		profile.StartupCommandFingerprint = commandFingerprint
	}
	candidates := performanceCalibrationCandidates(s.metrics.Info(), command)
	status := s.llama.Status()
	if status.Running && status.Model == model && status.StartupCommandID == commandID {
		candidates = uniqueCalibrationCandidates(append([]calibrationCandidate{
			{Label: "目前已載入配置", Tuning: status.PerformanceTuning},
		}, candidates...))
	}
	writeJSON(w, http.StatusOK, calibrationPlan{
		Enabled:    settings.AutoCalibrationEnabled,
		Key:        key,
		Calibrated: calibrated,
		Profile:    profile,
		Candidates: candidates,
	})
}

func (s *Server) handleRuntimeCalibrationSave(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Model            string                   `json:"model"`
		StartupCommandID string                   `json:"startup_command_id"`
		Tuning           domain.PerformanceTuning `json:"tuning"`
		Runs             []float64                `json:"runs"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	request.Model = filepath.ToSlash(strings.TrimSpace(request.Model))
	request.StartupCommandID = strings.TrimSpace(request.StartupCommandID)
	if request.Model == "" || request.StartupCommandID == "" {
		writeError(w, http.StatusBadRequest, errors.New("model 與 startup_command_id 不可為空"))
		return
	}
	command, err := s.startupCommands.Get(request.StartupCommandID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validatePerformanceTuning(command.Runtime, request.Tuning); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(request.Runs) != 3 {
		writeError(w, http.StatusBadRequest, errors.New("自動效能校準必須包含 3 次測試結果"))
		return
	}
	total := 0.0
	for _, speed := range request.Runs {
		if speed <= 0 || speed > 1_000_000 || math.IsNaN(speed) || math.IsInf(speed, 0) {
			writeError(w, http.StatusBadRequest, errors.New("校準速度資料無效"))
			return
		}
		total += speed
	}
	sortedRuns := append([]float64(nil), request.Runs...)
	sort.Float64s(sortedRuns)
	key, hardwareFingerprint, commandFingerprint := performanceCalibrationIdentity(
		s.metrics.Info(), request.Model, command,
	)
	profile := domain.PerformanceCalibration{
		Key:                       key,
		HardwareFingerprint:       hardwareFingerprint,
		Runtime:                   command.Runtime,
		Model:                     request.Model,
		StartupCommandID:          command.ID,
		StartupCommandFingerprint: commandFingerprint,
		Tuning:                    request.Tuning,
		AverageTokensPerSecond:    total / float64(len(request.Runs)),
		MedianTokensPerSecond:     sortedRuns[len(sortedRuns)/2],
		Runs:                      append([]float64(nil), request.Runs...),
		UpdatedAt:                 time.Now(),
	}
	settings := s.settings.Get()
	if !settings.AutoCalibrationEnabled {
		writeError(w, http.StatusConflict, errors.New("自動效能校準目前未啟用"))
		return
	}
	updated := make([]domain.PerformanceCalibration, 0, len(settings.PerformanceCalibrations)+1)
	updated = append(updated, profile)
	for _, existing := range settings.PerformanceCalibrations {
		if existing.Key != key {
			updated = append(updated, existing)
		}
	}
	settings.PerformanceCalibrations = updated
	if err := s.settings.Save(settings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.llama.MarkSavedPerformanceCalibration(request.Model, request.StartupCommandID, request.Tuning)
	writeJSON(w, http.StatusOK, profile)
}

func performanceCalibrationIdentity(
	info systemmetrics.SystemInfo,
	model string,
	command domain.StartupCommand,
) (key, hardwareFingerprint, commandFingerprint string) {
	hardwareFingerprint = stableSHA256(struct {
		Platform      string `json:"platform"`
		Architecture  string `json:"architecture"`
		CPUModel      string `json:"cpu_model"`
		PhysicalCores int    `json:"physical_cores"`
		LogicalCores  int    `json:"logical_cores"`
		GPUModel      string `json:"gpu_model"`
		MemoryBytes   uint64 `json:"memory_bytes"`
	}{
		Platform: info.Platform, Architecture: info.Architecture, CPUModel: info.CPUModel,
		PhysicalCores: info.PhysicalCores, LogicalCores: info.LogicalCores,
		GPUModel: info.GPUModel, MemoryBytes: info.MemoryBytes,
	})
	commandFingerprint = stableSHA256(struct {
		Runtime             string   `json:"runtime"`
		ServerHost          string   `json:"server_host"`
		ServerPort          int      `json:"server_port"`
		ContextSize         int      `json:"context_size"`
		GPULayers           int      `json:"gpu_layers"`
		Threads             int      `json:"threads"`
		MMapReserveGB       int      `json:"mmap_reserve_gb"`
		KVCacheQuantization string   `json:"kv_cache_quantization"`
		ExtraArgs           []string `json:"extra_args"`
	}{
		Runtime: command.Runtime, ServerHost: command.ServerHost, ServerPort: command.ServerPort,
		ContextSize: command.ContextSize, GPULayers: command.GPULayers, Threads: command.Threads,
		MMapReserveGB: command.MMapReserveGB, KVCacheQuantization: command.KVCacheQuantization,
		ExtraArgs: append([]string(nil), command.ExtraArgs...),
	})
	key = stableSHA256(struct {
		Hardware string `json:"hardware"`
		Runtime  string `json:"runtime"`
		Model    string `json:"model"`
		Command  string `json:"command"`
	}{hardwareFingerprint, command.Runtime, filepath.ToSlash(strings.TrimSpace(model)), commandFingerprint})
	return key, hardwareFingerprint, commandFingerprint
}

func stableSHA256(value any) string {
	content, _ := json.Marshal(value)
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func findPerformanceCalibration(values []domain.PerformanceCalibration, key string) (*domain.PerformanceCalibration, bool) {
	for _, value := range values {
		if value.Key == key {
			copyValue := value
			copyValue.Runs = append([]float64(nil), value.Runs...)
			return &copyValue, true
		}
	}
	return nil, false
}

func performanceCalibrationCandidates(info systemmetrics.SystemInfo, command domain.StartupCommand) []calibrationCandidate {
	if command.Runtime == domain.RuntimeMLXServer {
		current := managedArgumentInteger(command.ExtraArgs, "--prefill-step-size")
		if current <= 0 {
			current = 512
		}
		return uniqueCalibrationCandidates([]calibrationCandidate{
			{Label: "目前設定", Tuning: domain.PerformanceTuning{PrefillStepSize: current}},
			{Label: "Prefill 256", Tuning: domain.PerformanceTuning{PrefillStepSize: 256}},
			{Label: "Prefill 1024", Tuning: domain.PerformanceTuning{PrefillStepSize: 1024}},
			{Label: "Prefill 512", Tuning: domain.PerformanceTuning{PrefillStepSize: 512}},
		})[:3]
	}
	physicalCores := info.PhysicalCores
	if physicalCores <= 0 {
		physicalCores = max(1, runtime.NumCPU()/2)
	}
	logicalCores := info.LogicalCores
	if logicalCores <= 0 {
		logicalCores = max(1, runtime.NumCPU())
	}
	currentBatch := managedArgumentInteger(command.ExtraArgs, "--batch-size")
	currentUBatch := managedArgumentInteger(command.ExtraArgs, "--ubatch-size")
	return uniqueCalibrationCandidates([]calibrationCandidate{
		{Label: "目前設定", Tuning: domain.PerformanceTuning{Threads: command.Threads, BatchSize: currentBatch, UBatchSize: currentUBatch}},
		{Label: "實體核心／Batch 512", Tuning: domain.PerformanceTuning{Threads: physicalCores, BatchSize: 512, UBatchSize: 128}},
		{Label: "邏輯核心／Batch 1024", Tuning: domain.PerformanceTuning{Threads: logicalCores, BatchSize: 1024, UBatchSize: 256}},
		{Label: "實體核心／Batch 1536", Tuning: domain.PerformanceTuning{Threads: physicalCores, BatchSize: 1536, UBatchSize: 384}},
	})[:3]
}

func uniqueCalibrationCandidates(values []calibrationCandidate) []calibrationCandidate {
	result := make([]calibrationCandidate, 0, 3)
	seen := make(map[string]bool)
	for _, value := range values {
		key := stableSHA256(value.Tuning)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
		if len(result) == 3 {
			break
		}
	}
	return result
}

func validatePerformanceTuning(runtimeName string, tuning domain.PerformanceTuning) error {
	if tuning.Threads < 0 || tuning.Threads > 1024 || tuning.BatchSize < 0 || tuning.BatchSize > 8192 ||
		tuning.UBatchSize < 0 || tuning.UBatchSize > 8192 || tuning.PrefillStepSize < 0 || tuning.PrefillStepSize > 8192 ||
		(tuning.BatchSize > 0 && tuning.UBatchSize > tuning.BatchSize) {
		return errors.New("效能校準參數超出安全範圍")
	}
	if runtimeName == domain.RuntimeMLXServer &&
		(tuning.Threads != 0 || tuning.BatchSize != 0 || tuning.UBatchSize != 0) {
		return errors.New("mlx-server 校準只支援 Prefill Step Size")
	}
	if runtimeName == domain.RuntimeLlamaServer && tuning.PrefillStepSize != 0 {
		return errors.New("llama-server 校準不支援 Prefill Step Size")
	}
	return nil
}

func applyPerformanceTuning(command domain.StartupCommand, tuning domain.PerformanceTuning) domain.StartupCommand {
	if tuning.Threads > 0 {
		command.Threads = tuning.Threads
	}
	if command.Runtime == domain.RuntimeLlamaServer {
		command.ExtraArgs = withoutManagedArgument(command.ExtraArgs, "--batch-size", "--ubatch-size")
		if tuning.BatchSize > 0 {
			command.ExtraArgs = append(command.ExtraArgs, "--batch-size", strconv.Itoa(tuning.BatchSize))
		}
		if tuning.UBatchSize > 0 {
			command.ExtraArgs = append(command.ExtraArgs, "--ubatch-size", strconv.Itoa(tuning.UBatchSize))
		}
	} else if command.Runtime == domain.RuntimeMLXServer {
		command.ExtraArgs = withoutManagedArgument(command.ExtraArgs, "--prefill-step-size")
		if tuning.PrefillStepSize > 0 {
			command.ExtraArgs = append(command.ExtraArgs, "--prefill-step-size", strconv.Itoa(tuning.PrefillStepSize))
		}
	}
	return command
}

func managedArgumentInteger(args []string, name string) int {
	for index := 0; index < len(args); index++ {
		argument := strings.TrimSpace(args[index])
		if strings.HasPrefix(argument, name+"=") {
			value, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(argument, name+"=")))
			return value
		}
		if argument == name && index+1 < len(args) {
			value, _ := strconv.Atoi(strings.TrimSpace(args[index+1]))
			return value
		}
	}
	return 0
}

func withoutManagedArgument(args []string, names ...string) []string {
	managed := make(map[string]bool, len(names))
	for _, name := range names {
		managed[name] = true
	}
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := strings.TrimSpace(args[index])
		remove := false
		for name := range managed {
			if argument == name {
				remove = true
				if index+1 < len(args) {
					index++
				}
				break
			}
			if strings.HasPrefix(argument, name+"=") {
				remove = true
				break
			}
		}
		if !remove && argument != "" {
			result = append(result, argument)
		}
	}
	return result
}
