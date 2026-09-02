package llamacpp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"LlamaLoader/src/domain"
)

const gibibyte = uint64(1024 * 1024 * 1024)

type memoryProtectionResult struct {
	EffectiveContextSize int
	Actions              []string
	EstimatedBytes       uint64
	AvailableBytes       uint64
}

func (m *Manager) applyMemoryPressureProtectionLocked(
	settings domain.Settings,
	model, mmproj, draftModel string,
	dflashEnabled bool,
	command domain.StartupCommand,
) (domain.StartupCommand, string, bool, memoryProtectionResult, error) {
	result := memoryProtectionResult{EffectiveContextSize: command.ContextSize}
	if m.memorySnapshotProvider == nil {
		return command, draftModel, dflashEnabled, result, nil
	}
	snapshot := m.memorySnapshotProvider()
	if snapshot.TotalBytes == 0 || snapshot.AvailableBytes == 0 {
		return command, draftModel, dflashEnabled, result, nil
	}
	result.AvailableBytes = snapshot.AvailableBytes
	reserveBytes := max(2*gibibyte, snapshot.TotalBytes*8/100)
	if snapshot.AvailableBytes <= reserveBytes {
		return command, draftModel, dflashEnabled, result, fmt.Errorf(
			"記憶體壓力保護已阻止啟動：可用記憶體 %s，低於系統保留量 %s",
			formatMemoryBytes(snapshot.AvailableBytes), formatMemoryBytes(reserveBytes),
		)
	}
	budgetBytes := snapshot.AvailableBytes - reserveBytes
	targetBytes, companionBytes, draftBytes, err := runtimeModelFootprintBytes(
		settings, command.Runtime, model, mmproj, draftModel,
	)
	if err != nil {
		return command, draftModel, dflashEnabled, result, fmt.Errorf("記憶體壓力保護無法估算模型: %w", err)
	}
	mtpEnabled := command.Runtime == domain.RuntimeMLXServer &&
		hasAnyArgument(command.ExtraArgs, "--mtp-draft", "--mtp-block-size")
	batchSize := runtimeBatchSize(command)
	contexts := contextCandidates(command.ContextSize)
	chooseContext := func(currentDraftBytes uint64, currentBatch int) (int, uint64, bool) {
		lastEstimate := uint64(0)
		for _, contextSize := range contexts {
			lastEstimate = estimateRuntimeMemoryBytes(
				targetBytes, companionBytes, currentDraftBytes, contextSize,
				currentBatch, command.KVCacheQuantization,
			)
			if lastEstimate <= budgetBytes {
				return contextSize, lastEstimate, true
			}
		}
		return contexts[len(contexts)-1], lastEstimate, false
	}

	contextSize, estimateBytes, fits := chooseContext(draftBytes, batchSize)
	if !fits {
		lowerBatch := 256
		if command.Runtime == domain.RuntimeLlamaServer {
			command.ExtraArgs = setManagedIntegerArgument(command.ExtraArgs, "--batch-size", lowerBatch)
			command.ExtraArgs = setManagedIntegerArgument(command.ExtraArgs, "--ubatch-size", 128)
		} else {
			command.ExtraArgs = setManagedIntegerArgument(command.ExtraArgs, "--prefill-step-size", lowerBatch)
		}
		batchSize = lowerBatch
		result.Actions = append(result.Actions, "已降低 Batch／Prefill 以減少啟動尖峰記憶體")
		contextSize, estimateBytes, fits = chooseContext(draftBytes, batchSize)
	}
	if !fits && (dflashEnabled || mtpEnabled || strings.TrimSpace(draftModel) != "") {
		command.ExtraArgs = withoutSpeculativeDecodingArguments(command.ExtraArgs)
		command.DraftModel = ""
		draftModel = ""
		dflashEnabled = false
		draftBytes = 0
		result.Actions = append(result.Actions, "可用記憶體不足，已停用 MTP／DFlash")
		contextSize, estimateBytes, fits = chooseContext(0, batchSize)
	}
	result.EstimatedBytes = estimateBytes
	if !fits {
		return command, draftModel, dflashEnabled, result, fmt.Errorf(
			"記憶體壓力保護已阻止啟動：最低安全組合仍預估需要 %s，可用預算為 %s",
			formatMemoryBytes(estimateBytes), formatMemoryBytes(budgetBytes),
		)
	}
	if contextSize != command.ContextSize {
		result.Actions = append([]string{fmt.Sprintf(
			"Context 已由 %d 降為 %d", command.ContextSize, contextSize,
		)}, result.Actions...)
		command.ContextSize = contextSize
	}
	result.EffectiveContextSize = command.ContextSize
	result.EstimatedBytes = estimateBytes
	return command, draftModel, dflashEnabled, result, nil
}

func runtimeModelFootprintBytes(
	settings domain.Settings,
	runtimeName, model, mmproj, draftModel string,
) (targetBytes, companionBytes, draftBytes uint64, err error) {
	if runtimeName == domain.RuntimeMLXServer {
		path, _, resolveErr := resolveMLXTargetModel(settings, model, "模型")
		if resolveErr != nil {
			return 0, 0, 0, resolveErr
		}
		targetBytes, err = pathFootprintBytes(path)
		if err != nil {
			return 0, 0, 0, err
		}
		if value := strings.TrimSpace(mmproj); value != "" {
			path, resolveErr = resolveModelFile(settings.ModelDirectory, value, "mmproj")
			if resolveErr != nil {
				return 0, 0, 0, resolveErr
			}
			companionBytes, err = pathFootprintBytes(path)
			if err != nil {
				return 0, 0, 0, err
			}
		}
		if value := strings.TrimSpace(draftModel); value != "" {
			path, resolveErr = resolveMLXModel(settings.MLXModelDirectory, value, "Draft 模型")
			if resolveErr != nil {
				return 0, 0, 0, resolveErr
			}
			draftBytes, err = pathFootprintBytes(path)
		}
		return targetBytes, companionBytes, draftBytes, err
	}

	path, resolveErr := resolveModelFile(settings.ModelDirectory, model, "模型")
	if resolveErr != nil {
		return 0, 0, 0, resolveErr
	}
	targetBytes, err = pathFootprintBytes(path)
	if err != nil {
		return 0, 0, 0, err
	}
	if value := strings.TrimSpace(mmproj); value != "" {
		path, resolveErr = resolveModelFile(settings.ModelDirectory, value, "mmproj")
		if resolveErr != nil {
			return 0, 0, 0, resolveErr
		}
		companionBytes, err = pathFootprintBytes(path)
		if err != nil {
			return 0, 0, 0, err
		}
	}
	if value := strings.TrimSpace(draftModel); value != "" {
		path, resolveErr = resolveModelFile(settings.ModelDirectory, value, "Draft 模型")
		if resolveErr != nil {
			return 0, 0, 0, resolveErr
		}
		draftBytes, err = pathFootprintBytes(path)
	}
	return targetBytes, companionBytes, draftBytes, err
}

func pathFootprintBytes(path string) (uint64, error) {
	if isFastGGUFManifestPath(path) {
		fastGGUFPackage, err := readStandaloneFastGGUFPackage(path)
		if err != nil {
			return 0, err
		}
		if fastGGUFPackage.bytes <= 0 {
			return 0, errors.New("Fast GGUF 大小無效")
		}
		return uint64(fastGGUFPackage.bytes), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		if info.Size() < 0 {
			return 0, errors.New("模型檔案大小無效")
		}
		return uint64(info.Size()), nil
	}
	var total uint64
	err = filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		fileInfo, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if fileInfo.Mode().IsRegular() && fileInfo.Size() > 0 {
			total += uint64(fileInfo.Size())
		}
		return nil
	})
	return total, err
}

func estimateRuntimeMemoryBytes(
	targetBytes, companionBytes, draftBytes uint64,
	contextSize, batchSize int,
	kvQuantization string,
) uint64 {
	modelBytes := targetBytes + companionBytes + draftBytes
	baseBytes := modelBytes + modelBytes/10 + 512*1024*1024
	bytesPerToken := targetBytes / 60_000
	bytesPerToken = max(64*1024, min(1024*1024, bytesPerToken))
	switch kvQuantization {
	case domain.KVCacheQuantizationQ4:
		bytesPerToken = bytesPerToken * 35 / 100
	case domain.KVCacheQuantizationQ8:
		bytesPerToken = bytesPerToken * 60 / 100
	}
	kvBytes := bytesPerToken * uint64(max(128, contextSize))
	batchBytes := modelBytes * uint64(max(128, batchSize)) / 8192
	batchBytes = min(batchBytes, 8*gibibyte)
	return baseBytes + kvBytes + batchBytes
}

func contextCandidates(configured int) []int {
	values := []int{configured, 131072, 65536, 32768, 16384, 8192, 4096}
	result := make([]int, 0, len(values))
	seen := make(map[int]bool)
	for _, value := range values {
		if value <= 0 || value > configured || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	if len(result) == 0 {
		return []int{max(128, configured)}
	}
	return result
}

func runtimeBatchSize(command domain.StartupCommand) int {
	name := "--batch-size"
	if command.Runtime == domain.RuntimeMLXServer {
		name = "--prefill-step-size"
	}
	for index := 0; index < len(command.ExtraArgs); index++ {
		argument := strings.TrimSpace(command.ExtraArgs[index])
		if strings.HasPrefix(argument, name+"=") {
			value, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(argument, name+"=")))
			if value > 0 {
				return value
			}
		}
		if argument == name && index+1 < len(command.ExtraArgs) {
			value, _ := strconv.Atoi(strings.TrimSpace(command.ExtraArgs[index+1]))
			if value > 0 {
				return value
			}
		}
	}
	return 512
}

func setManagedIntegerArgument(arguments []string, name string, value int) []string {
	filtered := make([]string, 0, len(arguments)+2)
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		if argument == name {
			if index+1 < len(arguments) {
				index++
			}
			continue
		}
		if strings.HasPrefix(argument, name+"=") {
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return append(filtered, name, strconv.Itoa(value))
}

func withoutSpeculativeDecodingArguments(arguments []string) []string {
	arguments = withoutDFlashArguments(arguments)
	arguments = withoutDraftModelArguments(arguments)
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		if argument == "--mtp-block-size" {
			if index+1 < len(arguments) {
				index++
			}
			continue
		}
		if strings.HasPrefix(argument, "--mtp-block-size=") {
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

func formatMemoryBytes(value uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(value)/float64(gibibyte))
}
