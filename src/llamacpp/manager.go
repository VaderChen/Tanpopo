package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"LlamaLoader/src/domain"
	"LlamaLoader/src/download"
)

const mlxGGUFPathPrefix = "gguf:"

type SettingsProvider func() domain.Settings

// MemorySnapshot 是啟動前記憶體保護所需的最小系統資源摘要。
type MemorySnapshot struct {
	TotalBytes     uint64
	AvailableBytes uint64
}

type pendingGGUFSourceRemoval struct {
	path           string
	identity       os.FileInfo
	fastGGUFLoaded bool
}

type Manager struct {
	mu                     sync.Mutex
	settings               SettingsProvider
	accessControlPath      string
	stateStore             *runtimeStateStore
	cmd                    *exec.Cmd
	done                   chan struct{}
	stopping               bool
	status                 domain.LlamaStatus
	logs                   *logBuffer
	pendingGGUFRemoval     *pendingGGUFSourceRemoval
	memorySnapshotProvider func() MemorySnapshot
}

// SetMemorySnapshotProvider 注入由系統監控器集中採集的記憶體資料。
func (m *Manager) SetMemorySnapshotProvider(provider func() MemorySnapshot) {
	m.mu.Lock()
	m.memorySnapshotProvider = provider
	m.mu.Unlock()
}

func NewManager(settings SettingsProvider, accessControlPath, runtimeStatePath string) (*Manager, error) {
	accessControlPath = strings.TrimSpace(accessControlPath)
	if absolute, err := filepath.Abs(accessControlPath); err == nil {
		accessControlPath = absolute
	}
	stateStore, err := newRuntimeStateStore(runtimeStatePath)
	if err != nil {
		return nil, err
	}
	saved := stateStore.Get()
	manager := &Manager{
		settings:          settings,
		accessControlPath: accessControlPath,
		stateStore:        stateStore,
		logs:              newLogBuffer(128 * 1024),
		status: domain.LlamaStatus{
			DesiredRunning:          saved.DesiredRunning,
			Runtime:                 saved.Runtime,
			Model:                   saved.Model,
			MMProj:                  saved.MMProj,
			DraftModel:              saved.DraftModel,
			DraftKind:               saved.DraftKind,
			DFlashEnabled:           saved.DFlashEnabled,
			MMapEnabled:             saved.MMapEnabled,
			FastGGUF:                saved.FastGGUF,
			SkipGGUFConversionCache: saved.SkipGGUFConversionCache,
			KVCacheQuantization:     saved.KVCacheQuantization,
			StartupCommandID:        saved.StartupCommandID,
			StartupCommandName:      saved.StartupCommandName,
		},
	}
	manager.refreshURL()
	return manager, nil
}

func (m *Manager) Start(
	model, mmproj, draftModel string,
	dflashEnabled, mmapEnabled, fastGGUFEnabled, kvCacheQuantizationEnabled bool,
	skipGGUFConversionCache bool,
	conversionConfirmationKey string,
	startupCommand domain.StartupCommand,
) (domain.LlamaStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Running {
		return m.status, errors.New("模型服務已在執行中；請先停止目前模型")
	}
	settings := m.settings()
	memoryProtection := memoryProtectionResult{EffectiveContextSize: startupCommand.ContextSize}
	if settings.MemoryProtectionEnabled {
		var protectionErr error
		startupCommand, draftModel, dflashEnabled, memoryProtection, protectionErr = m.applyMemoryPressureProtectionLocked(
			settings, model, mmproj, draftModel, dflashEnabled, startupCommand,
		)
		if protectionErr != nil {
			return m.status, protectionErr
		}
	}
	mtpEnabled := startupCommand.Runtime == domain.RuntimeMLXServer &&
		hasAnyArgument(startupCommand.ExtraArgs, "--mtp-draft", "--mtp-block-size")
	embeddedMTPTarget := false
	if mtpEnabled {
		modelArgument, isGGUF, err := resolveMLXTargetModel(settings, model, "模型")
		if err != nil {
			return m.status, err
		}
		if isGGUF {
			embeddedMTPTarget = isEmbeddedMTPGGUF(modelArgument)
			if !embeddedMTPTarget {
				return m.status, errors.New("這份 GGUF 未包含 mlx-server 支援的內嵌 MTP 預測層")
			}
		}
	}
	if dflashEnabled && mtpEnabled {
		return m.status, errors.New("DFlash 與 MTP 不可同時啟用")
	}
	if (dflashEnabled || mtpEnabled) && kvCacheQuantizationEnabled {
		return m.status, errors.New("Draft 推測解碼與 KV Cache 量化不可同時啟用")
	}
	if skipGGUFConversionCache && startupCommand.Runtime != domain.RuntimeMLXServer {
		return m.status, errors.New("不建立 Fast GGUF 只適用於 mlx-server 載入 GGUF")
	}
	if kvCacheQuantizationEnabled && startupCommand.KVCacheQuantization == domain.KVCacheQuantizationNone {
		return m.status, errors.New("請先在啟動參數選擇 KV Cache Q8 或 Q4")
	}
	if !kvCacheQuantizationEnabled {
		startupCommand.KVCacheQuantization = domain.KVCacheQuantizationNone
	}
	if (dflashEnabled || mtpEnabled) && startupCommand.Runtime == domain.RuntimeMLXServer {
		startupCommand.ExtraArgs = withoutDraftModelArguments(startupCommand.ExtraArgs)
	} else {
		startupCommand.ExtraArgs = withoutDFlashArguments(startupCommand.ExtraArgs)
	}
	startupCommand.DraftModel = ""
	if embeddedMTPTarget {
		draftModel = ""
	} else if dflashEnabled || mtpEnabled {
		draftModel = strings.TrimSpace(draftModel)
		if draftModel == "" {
			mode := "DFlash"
			if mtpEnabled {
				mode = "MTP"
			}
			return m.status, fmt.Errorf("找不到可用的 %s Draft 模型，請先到「模型下載」取得配對的 Draft", mode)
		}
		startupCommand.DraftModel = draftModel
		if dflashEnabled && startupCommand.Runtime != domain.RuntimeMLXServer {
			startupCommand.ExtraArgs = append(startupCommand.ExtraArgs, "--spec-type", "draft-dflash")
		}
	}
	conversionExpected := false
	if startupCommand.Runtime == domain.RuntimeMLXServer && !skipGGUFConversionCache {
		inspectionContext, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		inspection, inspectionErr := m.inspectConversion(
			inspectionContext, settings, model, mmproj, fastGGUFEnabled, startupCommand,
		)
		cancel()
		if inspectionErr != nil {
			return m.status, fmt.Errorf("無法判斷 GGUF 是否需要轉換: %w", inspectionErr)
		}
		if inspection.RequiresConversion &&
			strings.TrimSpace(conversionConfirmationKey) != inspection.CacheKey {
			return m.status, fmt.Errorf(
				"此模型需要轉換並建立約 %d bytes 的 Fast GGUF，請先確認轉換",
				inspection.EstimatedCacheBytes,
			)
		}
		conversionExpected = inspection.RequiresConversion
	}
	var status domain.LlamaStatus
	var err error
	if startupCommand.Runtime == domain.RuntimeMLXServer {
		status, err = m.startMLXLocked(
			settings, model, mmproj, mmapEnabled, fastGGUFEnabled,
			skipGGUFConversionCache, startupCommand,
		)
	} else {
		if fastGGUFEnabled {
			return m.status, errors.New("快速GGUF模式目前只支援 mlx-server 載入 GGUF")
		}
		status, err = m.startLlamaLocked(settings, model, mmproj, mmapEnabled, startupCommand)
	}
	if err != nil {
		return status, err
	}
	if conversionExpected {
		m.status.ModelPreparation = domain.ModelPreparationConverting
		status.ModelPreparation = domain.ModelPreparationConverting
	}
	m.status.DesiredRunning = true
	m.status.DFlashEnabled = dflashEnabled
	if m.status.DraftModel == "" {
		m.status.DraftKind = ""
	} else if dflashEnabled {
		m.status.DraftKind = "dflash"
	}
	m.status.MMapEnabled = mmapEnabled
	m.status.FastGGUF = fastGGUFEnabled
	m.status.SkipGGUFConversionCache = skipGGUFConversionCache
	m.status.KVCacheQuantization = startupCommand.KVCacheQuantization
	m.status.EffectiveContextSize = memoryProtection.EffectiveContextSize
	m.status.MemoryProtectionApplied = len(memoryProtection.Actions) > 0
	m.status.MemoryProtectionActions = append([]string(nil), memoryProtection.Actions...)
	m.status.EstimatedMemoryBytes = memoryProtection.EstimatedBytes
	m.status.AvailableMemoryBytes = memoryProtection.AvailableBytes
	m.status.MMapReserveGB = 0
	if mmapEnabled {
		m.status.MMapReserveGB = startupCommand.MMapReserveGB
	}
	if err := m.persistStatusLocked(true); err != nil {
		m.status.DesiredRunning = false
		m.stopping = true
		if m.cmd != nil && m.cmd.Process != nil {
			_ = m.cmd.Process.Kill()
		}
		return m.status, fmt.Errorf("保存模型服務狀態失敗，已取消啟動: %w", err)
	}
	return m.status, nil
}

func (m *Manager) startLlamaLocked(settings domain.Settings, model, mmproj string, mmapEnabled bool, startupCommand domain.StartupCommand) (domain.LlamaStatus, error) {
	binary, err := ResolveServerBinary()
	if err != nil {
		return m.status, err
	}
	modelPath, err := resolveModelFile(settings.ModelDirectory, model, "模型")
	if err != nil {
		return m.status, err
	}
	if isGGUFDFlashDraft(modelPath) {
		return m.status, errors.New("DFlash Draft 模型不可作為 Target 模型啟動")
	}
	var mmprojPath string
	if strings.TrimSpace(mmproj) != "" {
		mmprojPath, err = resolveModelFile(settings.ModelDirectory, mmproj, "mmproj")
		if err != nil {
			return m.status, err
		}
	}
	var draftModelPath string
	if draftModel := strings.TrimSpace(startupCommand.DraftModel); draftModel != "" {
		if !isSupportedGGUFDFlashTarget(modelPath) {
			return m.status, errors.New("目前 Target GGUF 架構不支援 DFlash")
		}
		draftModelPath, err = resolveModelFile(settings.ModelDirectory, draftModel, "Draft 模型")
		if err != nil {
			return m.status, err
		}
		if !isGGUFDFlashDraft(draftModelPath) {
			return m.status, errors.New("指定的 Draft GGUF 不是 DFlash Draft 架構")
		}
		if draftModelPath == modelPath {
			return m.status, errors.New("DFlash Draft 模型不可與 Target 模型相同")
		}
	}

	args := withManagedKVCacheQuantization(
		startupCommand.ExtraArgs,
		domain.RuntimeLlamaServer,
		startupCommand.KVCacheQuantization,
	)
	args = withoutModelLoadModeArguments(args)
	managedMMapReserve := mmapEnabled && startupCommand.MMapReserveGB > 0
	if managedMMapReserve {
		args = withoutMMapMemoryArguments(args)
		args = append(args,
			"--fit", "on",
			"--fit-target", strconv.Itoa(startupCommand.MMapReserveGB*1024),
		)
	}
	loadMode := "none"
	if mmapEnabled {
		loadMode = "mmap"
	}
	args = append(args, "--load-mode", loadMode)
	args = append(args, "--model", modelPath)
	if mmprojPath != "" {
		args = append(args, "--mmproj", mmprojPath)
	}
	if draftModelPath != "" {
		args = append(args, "--model-draft", draftModelPath)
	}
	args = append(args,
		"--host", startupCommand.ServerHost,
		"--port", strconv.Itoa(startupCommand.ServerPort),
		"--ctx-size", strconv.Itoa(startupCommand.ContextSize),
		"--openloader-access-control", m.accessControlPath,
	)
	if !managedMMapReserve {
		args = append(args, "--n-gpu-layers", strconv.Itoa(startupCommand.GPULayers))
	}
	if startupCommand.Threads > 0 {
		args = append(args, "--threads", strconv.Itoa(startupCommand.Threads))
	}

	command := exec.Command(binary, args...)
	command.Stdout = m.logs
	command.Stderr = m.logs
	m.logs.Append("\n$ " + binary + " " + strings.Join(args, " ") + "\n")
	if err := command.Start(); err != nil {
		return m.status, fmt.Errorf("啟動 llama-server 失敗: %w", err)
	}
	done := make(chan struct{})
	m.cmd = command
	m.done = done
	m.stopping = false
	m.pendingGGUFRemoval = nil
	m.status = domain.LlamaStatus{
		Running:    true,
		Runtime:    domain.RuntimeLlamaServer,
		PID:        command.Process.Pid,
		Model:      filepath.ToSlash(strings.TrimSpace(model)),
		MMProj:     filepath.ToSlash(strings.TrimSpace(mmproj)),
		DraftModel: filepath.ToSlash(strings.TrimSpace(startupCommand.DraftModel)),
		DraftKind: func() string {
			if strings.TrimSpace(startupCommand.DraftModel) != "" {
				return "dflash"
			}
			return ""
		}(),
		Binary:             binary,
		StartupCommandID:   startupCommand.ID,
		StartupCommandName: startupCommand.Name,
		PerformanceTuning:  performanceTuningFromArguments(domain.RuntimeLlamaServer, args),
		ModelPreparation:   domain.ModelPreparationLoading,
		URL:                serverURL(startupCommand.ServerHost, startupCommand.ServerPort),
		StartedAt:          time.Now(),
	}
	go m.wait(command, done)
	go m.monitorReady(command, m.status.URL)
	return m.status, nil
}

func (m *Manager) startMLXLocked(
	settings domain.Settings,
	model, mmproj string,
	mmapEnabled, fastGGUFEnabled bool,
	skipGGUFConversionCache bool,
	startupCommand domain.StartupCommand,
) (domain.LlamaStatus, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return m.status, fmt.Errorf("mlx-server 僅支援 macOS Apple Silicon；目前平台為 %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	binary, err := ResolveMLXServer()
	if err != nil {
		return m.status, err
	}
	selection, err := resolveMLXTargetSelection(settings, model, mmproj)
	if err != nil {
		return m.status, err
	}
	modelArgument := selection.modelArgument
	isGGUF := selection.isGGUF
	var pendingSourceRemoval *pendingGGUFSourceRemoval
	if settings.RemoveOriginalGGUF && isGGUF && !skipGGUFConversionCache &&
		!isFastGGUFManifestPath(modelArgument) {
		pendingSourceRemoval, err = prepareGGUFSourceRemoval(
			settings.ModelDirectory,
			modelArgument,
		)
		if err != nil {
			return m.status, fmt.Errorf("無法啟用轉換後移除原 GGUF: %w", err)
		}
	}
	if skipGGUFConversionCache && !isGGUF {
		return m.status, errors.New("不建立 Fast GGUF 只適用於 mlx-server 載入 GGUF")
	}
	if fastGGUFEnabled && !isGGUF {
		return m.status, errors.New("快速GGUF模式只適用於 mlx-server 載入 GGUF")
	}
	if (isGGUF && isGGUFDFlashDraft(modelArgument)) ||
		(!isGGUF && (isMLXDFlashDraftDirectory(modelArgument) || isMLXMTPDraftDirectory(modelArgument))) {
		return m.status, errors.New("Draft 模型不可作為 Target 模型啟動")
	}
	mmprojArgument := selection.mmprojArgument
	statusMMProj := selection.statusMMProj
	var draftModelArgument string
	var draftKind string
	mtpEnabled := hasAnyArgument(startupCommand.ExtraArgs, "--mtp-draft", "--mtp-block-size")
	if mtpEnabled && isGGUF {
		if isFastGGUFManifestPath(modelArgument) {
			return m.status, errors.New("Fast GGUF fallback 目前不支援內嵌 MTP；請停用 MTP 後啟動")
		}
		if !isEmbeddedMTPGGUF(modelArgument) {
			return m.status, errors.New("這份 GGUF 未包含 mlx-server 支援的內嵌 MTP 預測層")
		}
		draftKind = "mtp"
	}
	if draftModel := strings.TrimSpace(startupCommand.DraftModel); draftModel != "" {
		if isGGUF {
			return m.status, errors.New("GGUF Target 目前不支援 MLX Draft 推測解碼；請搭配 MLX safetensors Target")
		}
		draftModelArgument, err = resolveMLXModel(settings.MLXModelDirectory, draftModel, "MLX Draft 模型")
		if err != nil {
			return m.status, err
		}
		switch {
		case isSupportedMLXMTPDraftDirectory(draftModelArgument):
			draftKind = "mtp"
			if !isSupportedMLXMTPTargetDirectory(modelArgument) {
				return m.status, errors.New("目前 Target MLX 模型架構不支援 MTP")
			}
			if err := validateMLXMTPPair(modelArgument, draftModelArgument); err != nil {
				return m.status, err
			}
		case isSupportedMLXDFlashDraftDirectory(draftModelArgument):
			draftKind = "dflash"
			if !isSupportedMLXDFlashTargetDirectory(modelArgument) {
				return m.status, errors.New("目前 Target MLX 模型架構不支援 DFlash")
			}
		default:
			return m.status, errors.New("指定的 MLX Draft 不是 Runtime 支援的 DFlash 或 MTP 模型")
		}
		if draftModelArgument == modelArgument {
			return m.status, errors.New("Draft 模型不可與 Target 模型相同")
		}
	}

	args := withManagedKVCacheQuantization(
		startupCommand.ExtraArgs,
		domain.RuntimeMLXServer,
		startupCommand.KVCacheQuantization,
	)
	args = withManagedMLXGGUFOptimization(
		args, isGGUF, fastGGUFEnabled, settings.DefaultFastGGUFStrategy,
	)
	args = withoutMLXGGUFCacheArguments(args)
	if isGGUF && skipGGUFConversionCache {
		args = append(args, "--no-gguf-cache")
	}
	if draftKind != "" {
		// mlx-server 只要看到 rotating 或量化的 target KV Cache 就會整個退回標準
		// 生成，而且只在 stderr 留一行訊息。受管 KV 量化已由 Start 的互斥檢查擋掉，
		// 這裡再清掉啟動參數裡手動加的 rotating KV 旗標，避免 Draft 推測解碼靜默失效。
		args = withoutMLXRotatingKVArguments(args)
	}
	args = withoutMLXMMapArguments(args)
	if mmapEnabled {
		args = append(args, "--mmap")
		if startupCommand.MMapReserveGB > 0 {
			args = append(args, "--mmap-reserve-gb", strconv.Itoa(startupCommand.MMapReserveGB))
		}
	} else {
		args = append(args, "--no-mmap")
	}
	args = append(args, "--model", modelArgument)
	if mmprojArgument != "" {
		args = append(args, "--mmproj", mmprojArgument)
	}
	if draftModelArgument != "" {
		if draftKind == "mtp" {
			args = append(args, "--mtp-draft", draftModelArgument)
		} else {
			// DFlash 目前只使用 language target，因此明確覆寫可能沿用的 Vision 設定。
			args = append(args,
				"--model-type", "text",
				"--dflash-draft", draftModelArgument,
			)
		}
	}
	args = append(args,
		"--host", startupCommand.ServerHost,
		"--port", strconv.Itoa(startupCommand.ServerPort),
		"--openloader-access-control", m.accessControlPath,
	)
	// 未量化的 256K rotating KV Cache 對大型模型會一次占用過多記憶體；
	// 一般 MLX 與 DFlash 使用可逐步成長的 Cache。只有參數明確啟用 KV
	// 量化時，才把啟動參數的 Context Size 套用為 rotating Cache 上限。
	if draftModelArgument == "" && startupCommand.KVCacheQuantization != domain.KVCacheQuantizationNone {
		args = append(args, "--max-kv-size", strconv.Itoa(startupCommand.ContextSize))
	}

	command := exec.Command(binary, args...)
	output := newRuntimeOutputWriter(m.logs, func(line string) {
		m.handleMLXRuntimeOutput(command, line)
	})
	command.Stdout = output
	command.Stderr = output
	m.logs.Append("\n$ " + binary + " " + strings.Join(args, " ") + "\n")
	if err := command.Start(); err != nil {
		return m.status, fmt.Errorf("啟動 mlx-server 失敗: %w", err)
	}
	done := make(chan struct{})
	m.cmd = command
	m.done = done
	m.stopping = false
	m.pendingGGUFRemoval = pendingSourceRemoval
	m.status = domain.LlamaStatus{
		Running:                 true,
		Runtime:                 domain.RuntimeMLXServer,
		PID:                     command.Process.Pid,
		Model:                   strings.TrimSpace(model),
		MMProj:                  statusMMProj,
		DraftModel:              filepath.ToSlash(strings.TrimSpace(startupCommand.DraftModel)),
		DraftKind:               draftKind,
		Binary:                  binary,
		StartupCommandID:        startupCommand.ID,
		StartupCommandName:      startupCommand.Name,
		PerformanceTuning:       performanceTuningFromArguments(domain.RuntimeMLXServer, args),
		SkipGGUFConversionCache: skipGGUFConversionCache,
		ModelPreparation: func() string {
			if isGGUF && skipGGUFConversionCache {
				return domain.ModelPreparationDirectLoading
			}
			if isGGUF {
				return domain.ModelPreparationCheckingCache
			}
			return domain.ModelPreparationLoading
		}(),
		URL:       serverURL(startupCommand.ServerHost, startupCommand.ServerPort),
		StartedAt: time.Now(),
	}
	go m.wait(command, done)
	go m.monitorReady(command, m.status.URL)
	return m.status, nil
}

type mlxTargetSelection struct {
	modelArgument  string
	isGGUF         bool
	mmprojArgument string
	statusMMProj   string
}

func resolveMLXTargetSelection(
	settings domain.Settings,
	model, mmproj string,
) (mlxTargetSelection, error) {
	modelArgument, isGGUF, err := resolveMLXTargetModel(settings, model, "模型")
	if err != nil {
		return mlxTargetSelection{}, err
	}
	selection := mlxTargetSelection{
		modelArgument: modelArgument,
		isGGUF:        isGGUF,
		statusMMProj:  strings.TrimSpace(mmproj),
	}
	if !isGGUF {
		if selection.statusMMProj != "" {
			return mlxTargetSelection{}, errors.New("mmproj 只能搭配 GGUF Target 模型")
		}
		return selection, nil
	}
	if isMMProjGGUF(modelArgument) {
		return mlxTargetSelection{}, errors.New("mmproj 不可作為 GGUF Target 模型啟動")
	}
	architecture, metadataErr := mlxGGUFModelArchitecture(modelArgument)
	if metadataErr != nil {
		return mlxTargetSelection{}, fmt.Errorf("無法讀取 GGUF 模型架構：%w", metadataErr)
	}
	if strings.TrimSpace(architecture) == "" {
		return mlxTargetSelection{}, errors.New("GGUF 缺少 general.architecture，無法交由 mlx-server 載入")
	}
	if selection.statusMMProj != "" {
		selection.mmprojArgument, err = resolveMLXGGUFFile(
			settings.ModelDirectory, mmproj, "mmproj",
		)
		return selection, err
	}
	if canonicalMLXGGUFArchitecture(architecture) == "qwen35" {
		selection.mmprojArgument, selection.statusMMProj, err = resolveCompanionMMProj(
			settings.ModelDirectory, modelArgument,
		)
		if err != nil {
			return mlxTargetSelection{}, err
		}
	}
	return selection, nil
}

func (m *Manager) InspectConversion(
	ctx context.Context,
	model, mmproj string,
	fastGGUFEnabled bool,
	startupCommand domain.StartupCommand,
) (domain.ModelConversionPreflight, error) {
	return m.inspectConversion(
		ctx, m.settings(), model, mmproj, fastGGUFEnabled, startupCommand,
	)
}

func (m *Manager) inspectConversion(
	ctx context.Context,
	settings domain.Settings,
	model, mmproj string,
	fastGGUFEnabled bool,
	startupCommand domain.StartupCommand,
) (domain.ModelConversionPreflight, error) {
	if startupCommand.Runtime != domain.RuntimeMLXServer {
		return domain.ModelConversionPreflight{Model: strings.TrimSpace(model)}, nil
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return domain.ModelConversionPreflight{}, fmt.Errorf(
			"mlx-server 僅支援 macOS Apple Silicon；目前平台為 %s/%s",
			runtime.GOOS, runtime.GOARCH,
		)
	}
	selection, err := resolveMLXTargetSelection(settings, model, mmproj)
	if err != nil {
		return domain.ModelConversionPreflight{}, err
	}
	if !selection.isGGUF {
		return domain.ModelConversionPreflight{Model: strings.TrimSpace(model)}, nil
	}
	if isFastGGUFManifestPath(selection.modelArgument) {
		return domain.ModelConversionPreflight{
			Applicable: false,
			Model:      strings.TrimSpace(model),
		}, nil
	}
	binary, err := ResolveMLXServer()
	if err != nil {
		return domain.ModelConversionPreflight{}, err
	}
	args := withManagedMLXGGUFOptimization(
		nil, true, fastGGUFEnabled, settings.DefaultFastGGUFStrategy,
	)
	args = append(args,
		"--model", selection.modelArgument,
	)
	if selection.mmprojArgument != "" {
		args = append(args, "--mmproj", selection.mmprojArgument)
	}
	args = append(args, "--inspect-gguf-cache")
	command := exec.CommandContext(ctx, binary, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return domain.ModelConversionPreflight{}, errors.New(message)
	}
	var inspection domain.ModelConversionPreflight
	if err := json.Unmarshal(bytes.TrimSpace(output), &inspection); err != nil {
		return domain.ModelConversionPreflight{}, fmt.Errorf(
			"mlx-server 轉換預檢回傳格式無效: %w", err,
		)
	}
	return inspection, nil
}

func resolveMLXTargetModel(settings domain.Settings, value, label string) (string, bool, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, mlxGGUFPathPrefix) {
		modelPath := strings.TrimSpace(strings.TrimPrefix(value, mlxGGUFPathPrefix))
		path, err := resolveMLXGGUFFile(settings.ModelDirectory, modelPath, label)
		if err == nil {
			return path, true, nil
		}
		manifestPath, fallbackErr := resolveFastGGUFFallbackManifest(
			settings.ModelDirectory,
			modelPath,
			normalizedFastGGUFProfile(settings.DefaultFastGGUFStrategy),
		)
		if fallbackErr != nil {
			return "", true, err
		}
		return manifestPath, true, nil
	}
	path, err := resolveMLXModel(settings.MLXModelDirectory, value, label)
	return path, false, err
}

func resolveMLXGGUFFile(directory, value, label string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), mlxGGUFPathPrefix))
	path, err := resolveModelFile(directory, value, label)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(path), ".gguf") {
		return "", fmt.Errorf("指定的%s不是 GGUF 檔案", label)
	}
	return path, nil
}

func isFastGGUFManifestPath(path string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(path)), ".fgguf.json")
}

func mlxGGUFModelArchitecture(path string) (string, error) {
	if isFastGGUFManifestPath(path) {
		return fastGGUFManifestArchitecture(path)
	}
	return readGGUFStringMetadata(path, "general.architecture")
}

func resolveMLXModel(directory, value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s不可為空", label)
	}
	path, err := download.SafeJoin(directory, value)
	if err != nil {
		return "", fmt.Errorf("%s路徑格式錯誤: %w", label, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s目錄無法讀取: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("指定的%s不是 MLX 模型目錄", label)
	}
	if !isMLXModelDirectory(path) {
		return "", fmt.Errorf("指定的%s缺少 config.json 或 safetensors 權重", label)
	}
	return path, nil
}

func resolveModelFile(directory, relativePath, label string) (string, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		return "", fmt.Errorf("%s不可為空", label)
	}
	path, err := download.SafeJoin(directory, relativePath)
	if err != nil {
		return "", fmt.Errorf("%s路徑格式錯誤: %w", label, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s檔案無法讀取: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("指定的%s不是一般檔案", label)
	}
	return path, nil
}

func (m *Manager) Stop(ctx context.Context) error {
	return m.stop(ctx, true)
}

// Shutdown 僅停止本次應用程式的子程序，保留「下次啟動時恢復」旗標。
func (m *Manager) Shutdown(ctx context.Context) error {
	return m.stop(ctx, false)
}

func (m *Manager) stop(ctx context.Context, clearDesired bool) error {
	m.mu.Lock()
	if clearDesired {
		previousDesired := m.status.DesiredRunning
		m.status.DesiredRunning = false
		if err := m.persistStatusLocked(false); err != nil {
			m.status.DesiredRunning = previousDesired
			m.mu.Unlock()
			return fmt.Errorf("保存停止狀態失敗: %w", err)
		}
	}
	if !m.status.Running || m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return nil
	}
	command := m.cmd
	done := m.done
	m.stopping = true
	m.mu.Unlock()

	if err := command.Process.Signal(os.Interrupt); err != nil {
		_ = command.Process.Kill()
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = command.Process.Kill()
		return ctx.Err()
	case <-timer.C:
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Restore 在 Tanpopo 重新啟動時，依狀態檔重新載入上次仍在執行的模型。
// 回傳 attempted=true 代表狀態檔要求恢復，即使恢復失敗亦然。
func (m *Manager) Restore(resolveCommand func(string) (domain.StartupCommand, error)) (attempted bool, err error) {
	saved := m.stateStore.Get()
	if !saved.DesiredRunning {
		return false, nil
	}
	command, err := resolveCommand(saved.StartupCommandID)
	if err == nil && command.Runtime != saved.Runtime {
		err = fmt.Errorf("啟動參數 Runtime 已由 %s 變更為 %s", saved.Runtime, command.Runtime)
	}
	if err == nil {
		_, err = m.Start(
			saved.Model,
			saved.MMProj,
			saved.DraftModel,
			saved.DFlashEnabled,
			saved.MMapEnabled,
			saved.FastGGUF,
			saved.KVCacheQuantization != domain.KVCacheQuantizationNone,
			saved.SkipGGUFConversionCache,
			"",
			command,
		)
	}
	if err == nil {
		return true, nil
	}

	m.mu.Lock()
	m.status.Running = false
	m.status.DesiredRunning = false
	m.status.PID = 0
	m.status.LastError = "自動恢復失敗：" + err.Error()
	m.status.StoppedAt = time.Now()
	persistErr := m.persistStatusLocked(false)
	m.mu.Unlock()
	if persistErr != nil {
		return true, fmt.Errorf("%v；另無法保存失敗狀態: %w", err, persistErr)
	}
	return true, err
}

func (m *Manager) persistStatusLocked(desiredRunning bool) error {
	return m.stateStore.Save(persistedRuntimeState{
		Version:                 runtimeStateVersion,
		DesiredRunning:          desiredRunning,
		Runtime:                 m.status.Runtime,
		Model:                   m.status.Model,
		MMProj:                  m.status.MMProj,
		DraftModel:              m.status.DraftModel,
		DraftKind:               m.status.DraftKind,
		DFlashEnabled:           m.status.DFlashEnabled,
		MMapEnabled:             m.status.MMapEnabled,
		FastGGUF:                m.status.FastGGUF,
		SkipGGUFConversionCache: m.status.SkipGGUFConversionCache,
		KVCacheQuantization:     m.status.KVCacheQuantization,
		StartupCommandID:        m.status.StartupCommandID,
		StartupCommandName:      m.status.StartupCommandName,
	})
}

func (m *Manager) Status() domain.LlamaStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := m.status
	result.MemoryProtectionActions = append([]string(nil), m.status.MemoryProtectionActions...)
	return result
}

// AnnotatePerformanceCalibration 將 API 層套用的持久化校準結果附加到狀態。
func (m *Manager) AnnotatePerformanceCalibration(applied bool) domain.LlamaStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.PerformanceCalibrationApplied = applied
	result := m.status
	result.MemoryProtectionActions = append([]string(nil), m.status.MemoryProtectionActions...)
	return result
}

func (m *Manager) Logs() string {
	return m.logs.String()
}

func (m *Manager) ClearLogs() {
	m.logs.Reset()
}

func (m *Manager) wait(command *exec.Cmd, done chan struct{}) {
	err := command.Wait()
	m.mu.Lock()
	if m.cmd == command {
		wasStopping := m.stopping
		m.status.Running = false
		m.status.Ready = false
		m.status.ModelPreparation = ""
		m.status.ModelPreparationDone = 0
		m.status.ModelPreparationTotal = 0
		m.status.ModelPreparationPercent = 0
		m.status.ModelPreparationKnown = false
		m.status.PID = 0
		m.status.StoppedAt = time.Now()
		if err != nil && !wasStopping {
			m.status.LastError = err.Error()
		} else {
			m.status.LastError = ""
		}
		m.cmd = nil
		m.done = nil
		m.stopping = false
		m.pendingGGUFRemoval = nil
	}
	close(done)
	m.mu.Unlock()
}

func (m *Manager) monitorReady(command *exec.Cmd, baseURL string) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 1200 * time.Millisecond}
	healthURL := strings.TrimRight(baseURL, "/") + "/health"

	for {
		m.mu.Lock()
		active := m.cmd == command && m.status.Running
		m.mu.Unlock()
		if !active {
			return
		}

		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, healthURL, nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4*1024))
				_ = response.Body.Close()
				// 舊版 Runtime 可能讓 /health 先經過模型 API 的 Access Key
				// 驗證。401／403 已足以證明服務完成監聽，不能因此讓管理介面
				// 永遠停在「載入中」；實際模型請求仍會照原安全策略驗證。
				if runtimeHealthResponseReady(response.StatusCode) {
					var removal *pendingGGUFSourceRemoval
					m.mu.Lock()
					if m.cmd == command && m.status.Running {
						m.status.Ready = true
						m.status.ModelPreparation = ""
						m.status.ModelPreparationDone = 0
						m.status.ModelPreparationTotal = 0
						m.status.ModelPreparationPercent = 0
						m.status.ModelPreparationKnown = false
						if m.pendingGGUFRemoval != nil &&
							m.pendingGGUFRemoval.fastGGUFLoaded &&
							m.settings().RemoveOriginalGGUF {
							removal = m.pendingGGUFRemoval
						}
						m.pendingGGUFRemoval = nil
					}
					m.mu.Unlock()
					if removal != nil {
						m.removeOriginalGGUF(removal)
					}
					return
				}
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func runtimeHealthResponseReady(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300 ||
		statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden
}

func (m *Manager) handleMLXRuntimeOutput(command *exec.Cmd, line string) {
	if phase, completed, total, unit, ok := parseMLXGGUFProgress(line); ok {
		m.mu.Lock()
		if m.cmd == command && m.status.Running && !m.status.Ready {
			m.status.ModelPreparationPercent = int(float64(completed) / float64(total) * 100)
			m.status.ModelPreparationKnown = true
			if unit == "steps" {
				m.status.ModelPreparationDone = 0
				m.status.ModelPreparationTotal = 0
			} else {
				m.status.ModelPreparationDone = completed
				m.status.ModelPreparationTotal = total
			}
			if !m.status.SkipGGUFConversionCache {
				m.status.ModelPreparation = phase
			}
		}
		m.mu.Unlock()
		return
	}
	if !strings.Contains(line, "TANPOPO_GGUF_CACHE ") {
		return
	}
	if (strings.Contains(line, "state=reloaded") || strings.Contains(line, "state=hit")) &&
		strings.Contains(line, "standalone=ready") {
		m.mu.Lock()
		if m.cmd == command && m.status.Running && !m.status.Ready &&
			m.pendingGGUFRemoval != nil {
			m.pendingGGUFRemoval.fastGGUFLoaded = true
		}
		m.mu.Unlock()
		return
	}
	preparation := ""
	switch {
	case strings.Contains(line, "state=checking"):
		preparation = domain.ModelPreparationCheckingCache
	case strings.Contains(line, "state=loading"), strings.Contains(line, "state=hit"):
		preparation = domain.ModelPreparationLoadingCache
	case strings.Contains(line, "state=miss"), strings.Contains(line, "state=invalid"):
		preparation = domain.ModelPreparationConverting
	case strings.Contains(line, "state=saving"):
		preparation = domain.ModelPreparationSavingCache
	case strings.Contains(line, "state=stored"), strings.Contains(line, "state=unavailable"):
		preparation = domain.ModelPreparationLoading
	case strings.Contains(line, "state=disabled"):
		preparation = domain.ModelPreparationDirectLoading
	}
	if preparation == "" {
		return
	}
	m.mu.Lock()
	if m.cmd == command && m.status.Running && !m.status.Ready {
		if m.status.SkipGGUFConversionCache {
			preparation = domain.ModelPreparationDirectLoading
		} else if m.status.ModelPreparation == domain.ModelPreparationConverting &&
			preparation == domain.ModelPreparationCheckingCache {
			// 啟動前已確認本次需要轉換；Runtime 仍會重新檢查快取以處理
			// 並行建立的競態，但不應讓使用者看到狀態倒退為「檢查中」。
			preparation = domain.ModelPreparationConverting
		}
		m.status.ModelPreparation = preparation
		if preparation == domain.ModelPreparationCheckingCache ||
			preparation == domain.ModelPreparationLoadingCache ||
			preparation == domain.ModelPreparationDirectLoading {
			m.status.ModelPreparationDone = 0
			m.status.ModelPreparationTotal = 0
			m.status.ModelPreparationPercent = 0
			m.status.ModelPreparationKnown = false
		}
	}
	m.mu.Unlock()
}

func prepareGGUFSourceRemoval(modelDirectory, sourcePath string) (*pendingGGUFSourceRemoval, error) {
	root, err := filepath.Abs(strings.TrimSpace(modelDirectory))
	if err != nil {
		return nil, fmt.Errorf("無法解析 GGUF 模型目錄: %w", err)
	}
	source, err := filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return nil, fmt.Errorf("無法解析來源 GGUF 路徑: %w", err)
	}
	if !strings.EqualFold(filepath.Ext(source), ".gguf") {
		return nil, errors.New("來源檔案不是 GGUF")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("無法驗證 GGUF 模型目錄: %w", err)
	}
	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, fmt.Errorf("無法驗證來源 GGUF: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedSource)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil, errors.New("來源 GGUF 不在受管理的模型目錄內")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return nil, fmt.Errorf("無法讀取來源 GGUF: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("來源 GGUF 必須是一般檔案，且不可為符號連結")
	}
	return &pendingGGUFSourceRemoval{path: source, identity: info}, nil
}

func (m *Manager) removeOriginalGGUF(removal *pendingGGUFSourceRemoval) {
	current, err := os.Lstat(removal.path)
	if err != nil {
		m.logs.Append("\nFast GGUF 已完成，但無法確認原始 GGUF，未移除: " + err.Error() + "\n")
		return
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(removal.identity, current) ||
		current.Size() != removal.identity.Size() ||
		!current.ModTime().Equal(removal.identity.ModTime()) {
		m.logs.Append("\nFast GGUF 已完成，但原始 GGUF 在轉換期間發生變更，未移除。\n")
		return
	}
	if _, err := standaloneFastGGUFManifestForSource(removal.path, ""); err != nil {
		m.logs.Append("\nFast GGUF 已完成，但獨立啟動資產不完整，未移除原始 GGUF: " + err.Error() + "\n")
		return
	}
	if err := os.Remove(removal.path); err != nil {
		m.logs.Append("\nFast GGUF 已完成，但移除原始 GGUF 失敗: " + err.Error() + "\n")
		return
	}
	m.logs.Append("\nFast GGUF 已完成，已移除原始 GGUF: " + removal.path + "\n")
}

func parseMLXGGUFProgress(line string) (string, int64, int64, string, bool) {
	marker := ""
	markerIndex := -1
	for _, candidate := range []string{"TANPOPO_MODEL_PROGRESS ", "TANPOPO_GGUF_PROGRESS "} {
		if index := strings.Index(line, candidate); index >= 0 {
			marker = candidate
			markerIndex = index
			break
		}
	}
	if markerIndex < 0 {
		return "", 0, 0, "", false
	}
	phase := ""
	unit := "bytes"
	var completed, total int64
	for _, field := range strings.Fields(line[markerIndex+len(marker):]) {
		key, value, found := strings.Cut(field, "=")
		if !found {
			continue
		}
		switch key {
		case "phase":
			phase = value
		case "completed":
			completed, _ = strconv.ParseInt(value, 10, 64)
		case "total":
			total, _ = strconv.ParseInt(value, 10, 64)
		case "unit":
			unit = value
		}
	}
	if phase != domain.ModelPreparationConverting &&
		phase != domain.ModelPreparationSavingCache &&
		phase != domain.ModelPreparationLoadingCache &&
		phase != domain.ModelPreparationLoading {
		return "", 0, 0, "", false
	}
	if completed < 0 || total <= 0 || (unit != "bytes" && unit != "steps") {
		return "", 0, 0, "", false
	}
	if completed > total {
		completed = total
	}
	return phase, completed, total, unit, true
}

func (m *Manager) refreshURL() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshURLLocked()
}

func (m *Manager) refreshURLLocked() {
	settings := m.settings()
	m.status.URL = serverURL(settings.ServerHost, settings.ServerPort)
}

func serverURL(host string, port int) string {
	displayHost := host
	if host == "0.0.0.0" || host == "::" || host == "" {
		displayHost = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(displayHost, strconv.Itoa(port))
}

func ResolveServerBinary() (string, error) {
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	platform := runtimePlatform()
	candidates := make([]string, 0, 16)
	for _, root := range runtimeRoots() {
		candidates = append(candidates,
			filepath.Join(root, "llama-server", "prebuilt", platform, "bin", name),
			filepath.Join(root, "llama-runtime", "prebuilt", platform, "bin", name),
			filepath.Join(root, "llama.cpp", "current", "bin", name),
			filepath.Join(root, "llama.cpp", "build", "bin", name),
		)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, "services", "llama.cpp", "current", "bin", name),
			filepath.Join(home, "services", "llama.cpp", "bin", name),
			filepath.Join(home, "llama.cpp", "build", "bin", name),
		)
	}
	if binary, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, binary)
	}
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		candidateInfo, statErr := os.Stat(path)
		if statErr == nil && candidateInfo.Mode().IsRegular() && isExecutable(candidateInfo) {
			return path, nil
		}
	}
	return "", errors.New("找不到應用程式內附或已安裝的 llama-server；請先執行部署包 install.sh，開發環境則建立 llama-runtime 預編譯檔")
}

func ResolveMLXServer() (string, error) {
	platform := runtimePlatform()
	candidates := make([]string, 0, 10)
	for _, root := range runtimeRoots() {
		candidates = append(candidates,
			filepath.Join(root, "mlx-server", "prebuilt", platform, "bin", "mlx-server"),
			filepath.Join(root, "mlx-runtime", "prebuilt", platform, "bin", "mlx-server"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, "services", "mlx-server", "current", "bin", "mlx-server"),
		)
	}
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		candidateInfo, statErr := os.Stat(path)
		if statErr != nil || !candidateInfo.Mode().IsRegular() || !isExecutable(candidateInfo) {
			continue
		}
		metalLibrary := filepath.Join(
			filepath.Dir(path),
			"mlx-swift_Cmlx.bundle", "Contents", "Resources", "default.metallib",
		)
		if metalInfo, metalErr := os.Stat(metalLibrary); metalErr != nil || !metalInfo.Mode().IsRegular() {
			continue
		}
		return path, nil
	}
	return "", errors.New("找不到應用程式內附或已安裝的 mlx-server Runtime；請先執行部署包 install.sh，開發環境則執行 scripts/build-mlx-server-runtime.sh")
}

func runtimePlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func runtimeRoots() []string {
	result := make([]string, 0, 4)
	seen := make(map[string]bool)
	appendRoot := func(path string) {
		absolute, err := filepath.Abs(path)
		if err != nil || seen[absolute] {
			return
		}
		seen[absolute] = true
		result = append(result, absolute)
	}
	if executable, err := os.Executable(); err == nil {
		executableDirectory := filepath.Dir(executable)
		appendRoot(executableDirectory)
		if filepath.Base(executableDirectory) == "bin" {
			appendRoot(filepath.Dir(executableDirectory))
		}
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		appendRoot(workingDirectory)
	}
	return result
}

func ListModels(directory string) ([]domain.ModelFile, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, errors.New("請先設定模型目錄")
	}
	base, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return nil, err
	}
	result := make([]domain.ModelFile, 0)
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// 下載與轉換流程可能在模型根目錄建立隱藏暫存目錄。這些檔案不該
			// 被列成另一份可選模型，否則同名項目會失去原目錄內的 mmproj 配對。
			if path != base && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		profile, profileErr := readGGUFModelProfile(path)
		// mmproj 是視覺投影檔，本身沒有語言模型欄位，但 UI 需要它出現在同一份
		// 清單裡才能挑選，因此不套用語言模型判斷。
		if profileErr != nil || (!profile.isLanguageModel() && !isMMProjGGUF(entry.Name())) {
			return nil
		}
		architecture := normalizeModelArchitecture(profile.Architecture)
		isDraft := architecture == "dflash"
		result = append(result, domain.ModelFile{
			Path:            filepath.ToSlash(relative),
			Format:          "gguf",
			Size:            info.Size(),
			ModifiedAt:      info.ModTime(),
			Architecture:    architecture,
			DFlashSupported: !isDraft && isSupportedDFlashTargetArchitecture(architecture),
			DFlashDraft:     isDraft,
			DFlashVariant:   ggufDFlashVariant(entry.Name(), isDraft),
			MTPSupported:    !isDraft && profile.NextNPredictLayers > 0 && canonicalMLXGGUFArchitecture(architecture) == "qwen35",
			MTPEmbedded:     !isDraft && profile.NextNPredictLayers > 0 && canonicalMLXGGUFArchitecture(architecture) == "qwen35",
		})
		if len(result) >= 5000 {
			return io.EOF
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Path) < strings.ToLower(result[j].Path) })
	return result, nil
}

func ListMLXModels(directory string) ([]domain.ModelFile, error) {
	return listMLXModels(directory, false)
}

// ListMLXRuntimeModels 合併原生 MLX safetensors 與語言模型 GGUF；Runtime
// 尚未明確回報支援的主模型也會回傳並標記為未測試，讓使用者仍可嘗試載入。
// GGUF 使用明確前綴，避免兩個模型根目錄出現同名項目時解析錯誤。
func ListMLXRuntimeModels(mlxDirectory, ggufDirectory string) ([]domain.ModelFile, error) {
	if strings.TrimSpace(mlxDirectory) == "" && strings.TrimSpace(ggufDirectory) == "" {
		return nil, errors.New("請先設定 MLX 或 GGUF 模型目錄")
	}
	result := make([]domain.ModelFile, 0)
	if strings.TrimSpace(mlxDirectory) != "" {
		mlxModels, err := ListMLXModels(mlxDirectory)
		if err != nil {
			return nil, err
		}
		result = append(result, mlxModels...)
	}
	if strings.TrimSpace(ggufDirectory) != "" {
		ggufModels, err := ListModels(ggufDirectory)
		if err != nil {
			return nil, err
		}
		fallbackModels, err := listStandaloneFastGGUFFallbackModels(
			ggufDirectory,
			ggufModels,
		)
		if err != nil {
			return nil, err
		}
		ggufModels = append(ggufModels, fallbackModels...)
		for _, model := range ggufModels {
			if model.DFlashDraft {
				continue
			}
			model.RuntimeUntested = !isMMProjGGUF(model.Path) &&
				!isSupportedMLXGGUFArchitecture(model.Architecture)
			model.Path = mlxGGUFPathPrefix + model.Path
			model.Format = "gguf"
			// GGUF Target 目前不接 DFlash Draft；保留原生 MLX Target 的既有支援。
			model.DFlashSupported = false
			result = append(result, model)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Format != result[j].Format {
			return result[i].Format < result[j].Format
		}
		return strings.ToLower(result[i].Path) < strings.ToLower(result[j].Path)
	})
	return result, nil
}

const maximumConversionManifestBytes = 4 * 1024 * 1024

type conversionCacheManifest struct {
	SchemaVersion           int      `json:"schemaVersion"`
	Key                     string   `json:"key"`
	SourceNames             []string `json:"sourceNames"`
	SourcePaths             []string `json:"sourcePaths"`
	Shards                  []string `json:"shards"`
	Profile                 string   `json:"profile"`
	GroupSize               int      `json:"groupSize"`
	WeightCount             int      `json:"weightCount"`
	Configuration           string   `json:"configuration"`
	Tokenizer               string   `json:"tokenizer"`
	TokenizerConfiguration  string   `json:"tokenizerConfiguration"`
	ProcessorConfiguration  string   `json:"processorConfiguration"`
	GenerationConfiguration string   `json:"generationConfiguration"`
}

type ggufConversionCacheEntry struct {
	path       string
	shardPaths []string
	assetPaths []string
	directory  bool
	sourceName string
	sourcePath string
	bytes      int64
}

// AttachGGUFConversionCacheInfo 將已完成且結構完整的轉換快取資訊附加到
// GGUF 模型清單。新 manifest 以來源完整路徑精確配對；舊 manifest 只有
// 檔名時，僅在目前 GGUF 清單中檔名唯一才採用，避免同名模型誤判。
func (m *Manager) AttachGGUFConversionCacheInfo(
	models []domain.ModelFile,
	ggufDirectory string,
) []domain.ModelFile {
	entries, err := m.ggufConversionCacheEntries(models, ggufDirectory)
	if err != nil || len(entries) == 0 {
		return models
	}
	nameCounts := ggufModelFilenameCounts(models)
	for index := range models {
		if strings.ToLower(models[index].Format) != "gguf" {
			continue
		}
		modelPath := strings.TrimPrefix(filepath.ToSlash(models[index].Path), mlxGGUFPathPrefix)
		target, err := canonicalGGUFModelPath(ggufDirectory, modelPath)
		if err != nil {
			continue
		}
		filename := filepath.Base(filepath.FromSlash(modelPath))
		for _, entry := range entries {
			if !ggufConversionCacheMatches(entry, target, filename, nameCounts[strings.ToLower(filename)] == 1) {
				continue
			}
			models[index].ConversionCached = true
			models[index].ConversionCacheBytes += entry.bytes
			models[index].ConversionCacheCount++
		}
	}
	return models
}

// DeleteGGUFConversionCache 只刪除指定原始 GGUF 對應的完整轉換快取項目，
// 不會移除原始 GGUF、mmproj、Draft 或模型目錄中的其他檔案。不同轉換策略
// 可能各有一份快取，因此一次刪除所有可安全辨識的對應項目。
func (m *Manager) DeleteGGUFConversionCache(
	ggufDirectory string,
	modelPath string,
) (deletedBytes int64, deletedCount int, err error) {
	modelPath = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(modelPath)), mlxGGUFPathPrefix)
	if modelPath == "" {
		return 0, 0, errors.New("模型路徑不可為空")
	}
	models, err := ListModels(ggufDirectory)
	if err != nil {
		return 0, 0, err
	}
	var found bool
	filenameCounts := make(map[string]int)
	for _, model := range models {
		if model.DFlashDraft || isMMProjGGUF(model.Path) {
			continue
		}
		filename := strings.ToLower(filepath.Base(filepath.FromSlash(model.Path)))
		filenameCounts[filename]++
		if filepath.ToSlash(model.Path) == modelPath {
			found = true
		}
	}
	if !found {
		return 0, 0, errors.New("找不到模型，請重新整理清單後再試")
	}
	target, err := canonicalGGUFModelPath(ggufDirectory, modelPath)
	if err != nil {
		return 0, 0, err
	}
	entries, err := m.ggufConversionCacheEntriesForRoots([]string{filepath.Dir(target)})
	if err != nil {
		return 0, 0, err
	}
	filename := filepath.Base(filepath.FromSlash(modelPath))
	uniqueName := filenameCounts[strings.ToLower(filename)] == 1
	for _, entry := range entries {
		if !ggufConversionCacheMatches(entry, target, filename, uniqueName) {
			continue
		}
		if err := deleteGGUFConversionCacheEntry(entry); err != nil {
			return deletedBytes, deletedCount, fmt.Errorf("移除 Fast GGUF 失敗: %w", err)
		}
		deletedBytes += entry.bytes
		deletedCount++
	}
	if deletedCount == 0 {
		return 0, 0, errors.New("找不到可移除的 Fast GGUF")
	}
	return deletedBytes, deletedCount, nil
}

func (m *Manager) ggufConversionCacheEntries(
	models []domain.ModelFile,
	ggufDirectory string,
) ([]ggufConversionCacheEntry, error) {
	roots := make([]string, 0)
	seen := make(map[string]struct{})
	for _, model := range models {
		if !strings.EqualFold(model.Format, "gguf") {
			continue
		}
		modelPath := strings.TrimPrefix(filepath.ToSlash(model.Path), mlxGGUFPathPrefix)
		target, err := canonicalGGUFModelPath(ggufDirectory, modelPath)
		if err != nil {
			continue
		}
		root := filepath.Dir(target)
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return m.ggufConversionCacheEntriesForRoots(roots)
}

func (m *Manager) ggufConversionCacheEntriesForRoots(
	roots []string,
) ([]ggufConversionCacheEntry, error) {
	result := make([]ggufConversionCacheEntry, 0)
	seenManifests := make(map[string]struct{})
	for _, root := range roots {
		entries, err := adjacentGGUFConversionCacheEntries(root)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if _, exists := seenManifests[entry.path]; exists {
				continue
			}
			seenManifests[entry.path] = struct{}{}
			result = append(result, entry)
		}
	}
	legacy, err := m.legacyGGUFConversionCacheEntries()
	if err != nil {
		return nil, err
	}
	return append(result, legacy...), nil
}

func (m *Manager) legacyGGUFConversionCacheEntries() ([]ggufConversionCacheEntry, error) {
	root := filepath.Join(filepath.Dir(m.accessControlPath), "converted_gguf_cache")
	directories, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("讀取 Fast GGUF 失敗: %w", err)
	}
	result := make([]ggufConversionCacheEntry, 0, len(directories))
	for _, directory := range directories {
		if !directory.IsDir() || directory.Type()&os.ModeSymlink != 0 || strings.HasPrefix(directory.Name(), ".") {
			continue
		}
		entryPath := filepath.Join(root, directory.Name())
		manifestPath := filepath.Join(entryPath, "manifest.json")
		manifestInfo, err := os.Lstat(manifestPath)
		if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Size() <= 0 || manifestInfo.Size() > maximumConversionManifestBytes {
			continue
		}
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var manifest conversionCacheManifest
		if json.Unmarshal(manifestData, &manifest) != nil ||
			(manifest.SchemaVersion != 2 && manifest.SchemaVersion != 3) ||
			manifest.Key != directory.Name() || len(manifest.SourceNames) == 0 || len(manifest.Shards) == 0 {
			continue
		}
		storedBytes := manifestInfo.Size()
		valid := true
		for _, shard := range manifest.Shards {
			if shard == "" || filepath.Base(shard) != shard ||
				(!strings.HasSuffix(shard, ".safetensors") && !strings.HasSuffix(shard, ".fgguf")) {
				valid = false
				break
			}
			shardInfo, err := os.Lstat(filepath.Join(entryPath, shard))
			if err != nil || !shardInfo.Mode().IsRegular() || shardInfo.Size() <= 0 {
				valid = false
				break
			}
			storedBytes += shardInfo.Size()
		}
		if !valid {
			continue
		}
		sourcePath := ""
		if len(manifest.SourcePaths) > 0 {
			sourcePath = canonicalExistingPath(manifest.SourcePaths[0])
		}
		result = append(result, ggufConversionCacheEntry{
			path:       entryPath,
			shardPaths: nil,
			assetPaths: nil,
			directory:  true,
			sourceName: filepath.Base(filepath.FromSlash(manifest.SourceNames[0])),
			sourcePath: sourcePath,
			bytes:      storedBytes,
		})
	}
	return result, nil
}

func adjacentGGUFConversionCacheEntries(root string) ([]ggufConversionCacheEntry, error) {
	files, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("讀取 Fast GGUF 失敗: %w", err)
	}
	result := make([]ggufConversionCacheEntry, 0)
	for _, file := range files {
		if file.IsDir() || file.Type()&os.ModeSymlink != 0 ||
			!strings.HasSuffix(file.Name(), ".fgguf.json") {
			continue
		}
		manifestPath := filepath.Join(root, file.Name())
		manifestInfo, err := os.Lstat(manifestPath)
		if err != nil || !manifestInfo.Mode().IsRegular() ||
			manifestInfo.Size() <= 0 || manifestInfo.Size() > maximumConversionManifestBytes {
			continue
		}
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var manifest conversionCacheManifest
		if json.Unmarshal(manifestData, &manifest) != nil ||
			(manifest.SchemaVersion != 3 && manifest.SchemaVersion != 4) ||
			manifest.Key == "" || len(manifest.SourceNames) == 0 || len(manifest.Shards) == 0 {
			continue
		}
		storedBytes := manifestInfo.Size()
		shardPaths := make([]string, 0, len(manifest.Shards))
		assetPaths := make([]string, 0, 5)
		valid := true
		for _, shard := range manifest.Shards {
			if shard == "" || filepath.Base(shard) != shard || !strings.HasSuffix(shard, ".fgguf") {
				valid = false
				break
			}
			shardPath := filepath.Join(root, shard)
			shardInfo, err := os.Lstat(shardPath)
			if err != nil || !shardInfo.Mode().IsRegular() || shardInfo.Size() <= 0 {
				valid = false
				break
			}
			storedBytes += shardInfo.Size()
			shardPaths = append(shardPaths, shardPath)
		}
		if !valid {
			continue
		}
		if manifest.SchemaVersion == 4 {
			requiredAssets := []string{
				manifest.Configuration,
				manifest.Tokenizer,
				manifest.TokenizerConfiguration,
			}
			for _, asset := range append(requiredAssets, manifest.ProcessorConfiguration, manifest.GenerationConfiguration) {
				if strings.TrimSpace(asset) == "" {
					continue
				}
				assetInfo, assetErr := regularAdjacentFastGGUFFile(root, asset, "")
				if assetErr != nil {
					valid = false
					break
				}
				storedBytes += assetInfo.Size()
				assetPaths = append(assetPaths, filepath.Join(root, asset))
			}
			if !valid || len(assetPaths) < len(requiredAssets) {
				continue
			}
		}
		sourcePath := ""
		if len(manifest.SourcePaths) > 0 {
			sourcePath = canonicalExistingPath(manifest.SourcePaths[0])
		}
		result = append(result, ggufConversionCacheEntry{
			path:       manifestPath,
			shardPaths: shardPaths,
			assetPaths: assetPaths,
			directory:  false,
			sourceName: filepath.Base(filepath.FromSlash(manifest.SourceNames[0])),
			sourcePath: sourcePath,
			bytes:      storedBytes,
		})
	}
	return result, nil
}

type standaloneFastGGUFPackage struct {
	manifestPath string
	manifest     conversionCacheManifest
	configPath   string
	bytes        int64
	modifiedAt   time.Time
}

// listStandaloneFastGGUFFallbackModels 只在來源 GGUF 已不存在時加入 Fast GGUF。
// manifest、所有 shard 與啟動資產都通過驗證才會成為可選模型，避免列表出現
// 「看得到但無法啟動」的殘缺轉換檔。
func listStandaloneFastGGUFFallbackModels(
	ggufDirectory string,
	existing []domain.ModelFile,
) ([]domain.ModelFile, error) {
	root, err := filepath.Abs(strings.TrimSpace(ggufDirectory))
	if err != nil {
		return nil, err
	}
	existingPaths := make(map[string]struct{}, len(existing))
	for _, model := range existing {
		existingPaths[filepath.ToSlash(model.Path)] = struct{}{}
	}

	selected := make(map[string]standaloneFastGGUFPackage)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 ||
			!strings.HasSuffix(strings.ToLower(entry.Name()), ".fgguf.json") {
			return nil
		}
		pkg, err := readStandaloneFastGGUFPackage(path)
		if err != nil {
			return nil
		}
		sourcePath, relative, err := fastGGUFSourcePath(root, pkg)
		if err != nil || isMMProjGGUF(sourcePath) {
			return nil
		}
		if info, statErr := os.Stat(sourcePath); statErr == nil && info.Mode().IsRegular() {
			return nil
		}
		if _, exists := existingPaths[relative]; exists {
			return nil
		}
		current, exists := selected[relative]
		if !exists || pkg.modifiedAt.After(current.modifiedAt) {
			selected[relative] = pkg
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	result := make([]domain.ModelFile, 0, len(selected))
	for relative, pkg := range selected {
		architecture, _ := fastGGUFConfigurationArchitecture(pkg.configPath)
		result = append(result, domain.ModelFile{
			Path:             relative,
			Format:           "gguf",
			Size:             pkg.bytes,
			ModifiedAt:       pkg.modifiedAt,
			Architecture:     architecture,
			RuntimeUntested:  !isSupportedMLXGGUFArchitecture(architecture),
			ConversionCached: true,
			FastGGUFFallback: true,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Path) < strings.ToLower(result[j].Path)
	})
	return result, nil
}

func readStandaloneFastGGUFPackage(manifestPath string) (standaloneFastGGUFPackage, error) {
	info, err := os.Lstat(manifestPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Size() <= 0 || info.Size() > maximumConversionManifestBytes {
		return standaloneFastGGUFPackage{}, errors.New("Fast GGUF manifest 無效")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return standaloneFastGGUFPackage{}, err
	}
	var manifest conversionCacheManifest
	if err := json.Unmarshal(data, &manifest); err != nil ||
		(manifest.SchemaVersion != 3 && manifest.SchemaVersion != 4) ||
		manifest.Key == "" || len(manifest.SourceNames) == 0 ||
		len(manifest.Shards) == 0 || manifest.WeightCount <= 0 ||
		(manifest.GroupSize != 32 && manifest.GroupSize != 64) {
		return standaloneFastGGUFPackage{}, errors.New("Fast GGUF manifest 內容不完整")
	}
	root := filepath.Dir(manifestPath)
	storedBytes := info.Size()
	modifiedAt := info.ModTime()
	for _, shard := range manifest.Shards {
		shardInfo, err := regularAdjacentFastGGUFFile(root, shard, ".fgguf")
		if err != nil {
			return standaloneFastGGUFPackage{}, err
		}
		storedBytes += shardInfo.Size()
		if shardInfo.ModTime().After(modifiedAt) {
			modifiedAt = shardInfo.ModTime()
		}
	}

	configuration := "config.json"
	tokenizer := "tokenizer.json"
	tokenizerConfiguration := "tokenizer_config.json"
	optionalAssets := []string{"generation_config.json"}
	if manifest.SchemaVersion == 4 {
		configuration = manifest.Configuration
		tokenizer = manifest.Tokenizer
		tokenizerConfiguration = manifest.TokenizerConfiguration
		optionalAssets = []string{
			manifest.ProcessorConfiguration,
			manifest.GenerationConfiguration,
		}
	}
	for _, asset := range []string{configuration, tokenizer, tokenizerConfiguration} {
		assetInfo, err := regularAdjacentFastGGUFFile(root, asset, ".json")
		if err != nil {
			return standaloneFastGGUFPackage{}, err
		}
		storedBytes += assetInfo.Size()
		if assetInfo.ModTime().After(modifiedAt) {
			modifiedAt = assetInfo.ModTime()
		}
	}
	for _, asset := range optionalAssets {
		if strings.TrimSpace(asset) == "" {
			continue
		}
		assetInfo, err := regularAdjacentFastGGUFFile(root, asset, "")
		if err != nil {
			continue
		}
		storedBytes += assetInfo.Size()
	}
	configPath := filepath.Join(root, configuration)
	if _, err := fastGGUFConfigurationArchitecture(configPath); err != nil {
		return standaloneFastGGUFPackage{}, err
	}
	return standaloneFastGGUFPackage{
		manifestPath: manifestPath,
		manifest:     manifest,
		configPath:   configPath,
		bytes:        storedBytes,
		modifiedAt:   modifiedAt,
	}, nil
}

func regularAdjacentFastGGUFFile(root, filename, requiredSuffix string) (os.FileInfo, error) {
	if filename == "" || filepath.Base(filename) != filename ||
		(requiredSuffix != "" && !strings.HasSuffix(strings.ToLower(filename), requiredSuffix)) {
		return nil, errors.New("Fast GGUF 檔名無效")
	}
	info, err := os.Lstat(filepath.Join(root, filename))
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Size() <= 0 {
		return nil, errors.New("Fast GGUF 檔案不完整")
	}
	return info, nil
}

func fastGGUFSourcePath(root string, pkg standaloneFastGGUFPackage) (string, string, error) {
	sourceName := filepath.Base(filepath.FromSlash(pkg.manifest.SourceNames[0]))
	if !strings.EqualFold(filepath.Ext(sourceName), ".gguf") {
		return "", "", errors.New("Fast GGUF 來源名稱無效")
	}
	candidate := filepath.Join(filepath.Dir(pkg.manifestPath), sourceName)
	if len(pkg.manifest.SourcePaths) > 0 && filepath.IsAbs(pkg.manifest.SourcePaths[0]) {
		candidate = filepath.Clean(pkg.manifest.SourcePaths[0])
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", "", errors.New("Fast GGUF 來源不在模型目錄內")
	}
	return candidate, filepath.ToSlash(relative), nil
}

func fastGGUFConfigurationArchitecture(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	var configuration struct {
		ModelType string `json:"model_type"`
	}
	if err := json.Unmarshal(data, &configuration); err != nil {
		return "", err
	}
	architecture := strings.TrimSpace(configuration.ModelType)
	architecture = normalizeModelArchitecture(architecture)
	if architecture == "" {
		return "", errors.New("Fast GGUF 設定缺少 model_type")
	}
	return architecture, nil
}

func fastGGUFManifestArchitecture(manifestPath string) (string, error) {
	pkg, err := readStandaloneFastGGUFPackage(manifestPath)
	if err != nil {
		return "", err
	}
	return fastGGUFConfigurationArchitecture(pkg.configPath)
}

func resolveFastGGUFFallbackManifest(
	ggufDirectory, modelPath, preferredProfile string,
) (string, error) {
	sourcePath, err := download.SafeJoin(ggufDirectory, modelPath)
	if err != nil {
		return "", fmt.Errorf("模型路徑格式錯誤: %w", err)
	}
	return standaloneFastGGUFManifestForSource(sourcePath, preferredProfile)
}

func standaloneFastGGUFManifestForSource(sourcePath, preferredProfile string) (string, error) {
	sourcePath, err := filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Dir(sourcePath))
	if err != nil {
		return "", err
	}
	type candidate struct {
		path     string
		profile  string
		modified time.Time
	}
	candidates := make([]candidate, 0)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			!strings.HasSuffix(strings.ToLower(entry.Name()), ".fgguf.json") {
			continue
		}
		pkg, err := readStandaloneFastGGUFPackage(filepath.Join(filepath.Dir(sourcePath), entry.Name()))
		if err != nil {
			continue
		}
		nameMatches := strings.EqualFold(
			filepath.Base(sourcePath),
			filepath.Base(filepath.FromSlash(pkg.manifest.SourceNames[0])),
		)
		pathMatches := len(pkg.manifest.SourcePaths) > 0 &&
			canonicalExistingPath(pkg.manifest.SourcePaths[0]) == canonicalExistingPath(sourcePath)
		if !pathMatches && !nameMatches {
			continue
		}
		candidates = append(candidates, candidate{
			path: pkg.manifestPath, profile: pkg.manifest.Profile, modified: pkg.modifiedAt,
		})
	}
	if len(candidates) == 0 {
		return "", errors.New("找不到可獨立啟動的 Fast GGUF")
	}
	sort.Slice(candidates, func(i, j int) bool {
		iPreferred := preferredProfile != "" && candidates[i].profile == preferredProfile
		jPreferred := preferredProfile != "" && candidates[j].profile == preferredProfile
		if iPreferred != jPreferred {
			return iPreferred
		}
		return candidates[i].modified.After(candidates[j].modified)
	})
	return candidates[0].path, nil
}

func deleteGGUFConversionCacheEntry(entry ggufConversionCacheEntry) error {
	if entry.directory {
		return os.RemoveAll(entry.path)
	}
	for _, shardPath := range entry.shardPaths {
		if err := os.Remove(shardPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, assetPath := range entry.assetPaths {
		if err := os.Remove(assetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(entry.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func ggufModelFilenameCounts(models []domain.ModelFile) map[string]int {
	counts := make(map[string]int)
	for _, model := range models {
		if strings.ToLower(model.Format) != "gguf" {
			continue
		}
		modelPath := strings.TrimPrefix(filepath.ToSlash(model.Path), mlxGGUFPathPrefix)
		filename := strings.ToLower(filepath.Base(filepath.FromSlash(modelPath)))
		counts[filename]++
	}
	return counts
}

func canonicalGGUFModelPath(root, modelPath string) (string, error) {
	target, err := download.SafeJoin(root, modelPath)
	if err != nil {
		return "", fmt.Errorf("模型路徑格式錯誤: %w", err)
	}
	return canonicalExistingPath(target), nil
}

func canonicalExistingPath(path string) string {
	path = filepath.Clean(path)
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func ggufConversionCacheMatches(
	entry ggufConversionCacheEntry,
	target string,
	filename string,
	uniqueName bool,
) bool {
	if entry.sourcePath != "" {
		return entry.sourcePath == target
	}
	return uniqueName && strings.EqualFold(entry.sourceName, filename)
}

// DeleteStoredModel 只刪除模型掃描器實際列出的 Target，並以 SafeJoin 限制
// 目標必須位於對應模型根目錄內。MLX 模型以完整目錄為單位刪除；位於
// 子目錄的 GGUF 會刪除模型根目錄下的第一層完整資料夾，連同 mmproj 與
// 其他附屬檔案一起移除。直接放在共用根目錄的 GGUF 只刪除該檔案，避免
// 誤刪整個模型根目錄。mmproj 與 DFlash Draft 不屬於此清單。
func DeleteStoredModel(mlxDirectory, ggufDirectory, format, modelPath string) error {
	format = strings.ToLower(strings.TrimSpace(format))
	modelPath = filepath.ToSlash(strings.TrimSpace(modelPath))
	modelPath = strings.TrimPrefix(modelPath, mlxGGUFPathPrefix)
	if modelPath == "" {
		return errors.New("模型路徑不可為空")
	}

	var (
		root       string
		models     []domain.ModelFile
		err        error
		removeTree bool
	)
	switch format {
	case "mlx":
		root = mlxDirectory
		removeTree = true
		models, err = ListMLXModels(root)
	case "gguf":
		root = ggufDirectory
		models, err = ListModels(root)
	case "":
		return errors.New("模型格式不可為空")
	default:
		return fmt.Errorf("不支援的模型格式：%s", format)
	}
	if err != nil {
		return err
	}

	found := false
	for _, model := range models {
		if format == "gguf" && (model.DFlashDraft || isMMProjGGUF(model.Path)) {
			continue
		}
		if filepath.ToSlash(model.Path) == modelPath {
			found = true
			break
		}
	}
	if !found {
		return errors.New("找不到可刪除的模型，請重新整理清單後再試")
	}

	target, err := download.SafeJoin(root, modelPath)
	if err != nil {
		return fmt.Errorf("模型路徑格式錯誤: %w", err)
	}
	if removeTree {
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("刪除 MLX 模型失敗: %w", err)
		}
		return nil
	}
	if separator := strings.Index(modelPath, "/"); separator > 0 {
		modelDirectory, err := download.SafeJoin(root, modelPath[:separator])
		if err != nil {
			return fmt.Errorf("GGUF 模型目錄格式錯誤: %w", err)
		}
		if err := os.RemoveAll(modelDirectory); err != nil {
			return fmt.Errorf("刪除 GGUF 模型目錄失敗: %w", err)
		}
		return nil
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("刪除 GGUF 模型失敗: %w", err)
	}
	return nil
}

func isSupportedMLXGGUFArchitecture(architecture string) bool {
	canonical := canonicalMLXGGUFArchitecture(architecture)
	if support := mlxRuntimeSupport(); support.available {
		return support.ggufArchitectures[canonical]
	}
	// 問不到 Runtime（例如尚未建置）時退回原本的保守清單，寧可少列也不誤列。
	switch canonical {
	case "qwen35", "qwen3", "qwen2", "llama":
		return true
	default:
		return false
	}
}

func isEmbeddedMTPGGUF(path string) bool {
	profile, err := readGGUFModelProfile(path)
	return err == nil && profile.NextNPredictLayers > 0 &&
		canonicalMLXGGUFArchitecture(profile.Architecture) == "qwen35"
}

// mlxRuntimeSupportInfo 是 mlx-server 回報的可載入模型型別。
type mlxRuntimeSupportInfo struct {
	available         bool
	modelTypes        map[string]bool
	ggufArchitectures map[string]bool
}

var (
	mlxRuntimeSupportOnce  sync.Once
	mlxRuntimeSupportValue mlxRuntimeSupportInfo
)

// mlxRuntimeSupport 向 mlx-server 查詢它實際註冊了哪些模型型別。
//
// 讓 Runtime 自己回報，模型列表就不需要另外維護一份會與 MLXLLM／MLXVLM 註冊表
// 脫節的靜態清單。查詢只做一次並快取。
func mlxRuntimeSupport() mlxRuntimeSupportInfo {
	mlxRuntimeSupportOnce.Do(func() {
		binary, err := ResolveMLXServer()
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, err := exec.CommandContext(ctx, binary, "--supported-model-types").Output()
		if err != nil {
			return
		}
		var payload struct {
			LLM  []string `json:"llm"`
			VLM  []string `json:"vlm"`
			GGUF []string `json:"gguf"`
		}
		if err := json.Unmarshal(output, &payload); err != nil {
			return
		}
		modelTypes := make(map[string]bool, len(payload.LLM)+len(payload.VLM))
		for _, value := range append(payload.LLM, payload.VLM...) {
			modelTypes[normalizeModelArchitecture(value)] = true
		}
		ggufArchitectures := make(map[string]bool, len(payload.GGUF))
		for _, value := range payload.GGUF {
			ggufArchitectures[canonicalMLXGGUFArchitecture(value)] = true
		}
		if len(modelTypes) == 0 || len(ggufArchitectures) == 0 {
			return
		}
		mlxRuntimeSupportValue = mlxRuntimeSupportInfo{
			available:         true,
			modelTypes:        modelTypes,
			ggufArchitectures: ggufArchitectures,
		}
	})
	return mlxRuntimeSupportValue
}

// isRunnableMLXModelType 判斷 MLX safetensors 目錄是不是 mlx-server 能載入的
// LLM／VLM。音樂、影片等生成模型同樣是 safetensors 加 config.json，但 model_type
// 不在註冊表裡，用這個判斷把它們排除在模型列表之外。
// isCompositeModelComponent 判斷這個目錄是不是影像／音樂等組合式模型的零件。
//
// 這類模型會把 text_encoder、vae、transformer 拆成子目錄，各自帶 config.json；
// 其中的文字塔雖然是合法的語言模型，卻不該當成可獨立啟動的模型出現在列表。
// 判斷方式是往上找到掃描根目錄為止，只要有任何一層本身就是模型或 pipeline 目錄，
// 就視為零件。
func isCompositeModelComponent(base, modelDirectory string) bool {
	for parent := filepath.Dir(modelDirectory); ; parent = filepath.Dir(parent) {
		if parent == base || !strings.HasPrefix(parent, base) {
			return false
		}
		for _, marker := range []string{"model_index.json", "config.json"} {
			if info, err := os.Stat(filepath.Join(parent, marker)); err == nil &&
				info.Mode().IsRegular() {
				return true
			}
		}
		if next := filepath.Dir(parent); next == parent {
			return false
		}
	}
}

// hasGenerativeHead 判斷設定裡的 architectures 是不是帶語言模型輸出頭。
//
// 嵌入與基礎模型會標成 `Qwen3Model` 這類沒有輸出頭的名稱，沒辦法拿來生成文字。
// architectures 缺席時不做判斷，維持原本行為。
func hasGenerativeHead(architectures []string) bool {
	if len(architectures) == 0 {
		return true
	}
	for _, architecture := range architectures {
		name := strings.TrimSpace(architecture)
		if strings.HasSuffix(name, "ForCausalLM") ||
			strings.HasSuffix(name, "ForConditionalGeneration") ||
			strings.HasSuffix(name, "LMHeadModel") {
			return true
		}
	}
	return false
}

// isLikelyConversationalMLXModel 限制「尚未測試」群組只收錄語言生成模型。
//
// 未知 Runtime 型別不能再依賴註冊表判斷，因此以 Hugging Face 的生成架構命名
// 與多模態模型的 text_config 結構辨識。這可保留未來的新 LLM／VLM，同時排除
// 音樂、影像 pipeline、Embedding 與沒有語言輸出頭的基礎模型。
func isLikelyConversationalMLXModel(configuration mlxModelConfiguration) bool {
	for _, architecture := range configuration.Architectures {
		name := strings.TrimSpace(architecture)
		if strings.HasSuffix(name, "ForCausalLM") || strings.HasSuffix(name, "LMHeadModel") {
			return true
		}
		if strings.HasSuffix(name, "ForConditionalGeneration") && len(configuration.TextConfig) > 0 {
			return true
		}
	}
	return false
}

func isRunnableMLXModelType(modelType string) bool {
	support := mlxRuntimeSupport()
	if !support.available {
		// 問不到 Runtime 時不過濾，維持原本行為，避免整份清單變空。
		return true
	}
	return support.modelTypes[normalizeModelArchitecture(modelType)]
}

func canonicalMLXGGUFArchitecture(architecture string) string {
	return strings.NewReplacer("_", "", "-", "", ".", "").Replace(
		normalizeModelArchitecture(architecture),
	)
}

// resolveCompanionMMProj 會自動掛載同一模型目錄中唯一的 mmproj；若沒有
// projector，GGUF 仍可作為純文字 LLM 啟動。只有候選不唯一時才要求使用者
// 明確選擇，避免靜默掛載錯誤投影權重。
func resolveCompanionMMProj(modelRoot, targetPath string) (string, string, error) {
	entries, err := os.ReadDir(filepath.Dir(targetPath))
	if err != nil {
		return "", "", fmt.Errorf("讀取 GGUF 模型目錄失敗: %w", err)
	}
	candidates := make([]string, 0, 1)
	for _, entry := range entries {
		if entry.IsDir() || !isMMProjGGUF(entry.Name()) ||
			!strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			continue
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(targetPath), entry.Name()))
	}
	if len(candidates) == 0 {
		return "", "", nil
	}
	if len(candidates) > 1 {
		return "", "", errors.New("找到多個 mmproj，請在執行狀態頁選擇與 GGUF 模型配對的檔案")
	}
	relative, err := filepath.Rel(modelRoot, candidates[0])
	if err != nil {
		return "", "", fmt.Errorf("解析 mmproj 路徑失敗: %w", err)
	}
	return candidates[0], mlxGGUFPathPrefix + filepath.ToSlash(relative), nil
}

func isMMProjGGUF(path string) bool {
	return strings.Contains(strings.ToLower(filepath.Base(path)), "mmproj")
}

// ListMLXDraftModels 只列出原生 Swift Runtime 已支援的 DFlash 與 MTP Draft。
// Target 與 Draft 使用相同模型根目錄，但透過 role 分流避免 Draft 被誤選為主模型。
func ListMLXDraftModels(directory string) ([]domain.ModelFile, error) {
	return listMLXModels(directory, true)
}

// ListMLXDFlashModels 保留舊呼叫端相容性；回傳值已包含所有 MLX Draft 類型。
func ListMLXDFlashModels(directory string) ([]domain.ModelFile, error) {
	return ListMLXDraftModels(directory)
}

func normalizeModelArchitecture(architecture string) string {
	return strings.ToLower(strings.TrimSpace(architecture))
}

func isSupportedDFlashTargetArchitecture(architecture string) bool {
	switch normalizeModelArchitecture(architecture) {
	case "qwen3", "qwen3moe", "qwen3_moe", "qwen35", "qwen35moe", "qwen3_5", "qwen3_5_moe":
		return true
	default:
		return false
	}
}

func isGGUFDFlashDraft(path string) bool {
	architecture, err := readGGUFStringMetadata(path, "general.architecture")
	return err == nil && normalizeModelArchitecture(architecture) == "dflash"
}

func isSupportedGGUFDFlashTarget(path string) bool {
	architecture, err := readGGUFStringMetadata(path, "general.architecture")
	return err == nil && isSupportedDFlashTargetArchitecture(architecture)
}

func ggufDFlashVariant(filename string, isDraft bool) string {
	if !isDraft {
		return ""
	}
	if strings.Contains(strings.ToLower(filename), "dflash2") {
		return "dflash2"
	}
	return "dflash1"
}

func listMLXModels(directory string, draftsOnly bool) ([]domain.ModelFile, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, errors.New("請先設定 MLX 模型目錄")
	}
	base, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return nil, err
	}

	result := make([]domain.ModelFile, 0)
	err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != base && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "config.json" || path == filepath.Join(base, "config.json") {
			return nil
		}
		modelDirectory := filepath.Dir(path)
		if !isMLXModelDirectory(modelDirectory) {
			return nil
		}
		configuration, configurationErr := readMLXModelConfiguration(modelDirectory)
		if configurationErr != nil {
			return nil
		}
		if isCompositeModelComponent(base, modelDirectory) {
			return nil
		}
		architecture := normalizeModelArchitecture(configuration.ModelType)
		isDFlashDraft := isMLXDFlashDraftConfiguration(configuration)
		isMTPDraft := isMLXMTPDraftConfiguration(configuration)
		isDraft := isDFlashDraft || isMTPDraft
		runtimeUntested := !isRunnableMLXModelType(configuration.ModelType)
		if draftsOnly {
			if !isSupportedMLXDFlashDraftConfiguration(configuration) &&
				!isSupportedMLXMTPDraftConfiguration(configuration) {
				return filepath.SkipDir
			}
		} else if isDraft {
			return filepath.SkipDir
		} else if !hasGenerativeHead(configuration.Architectures) ||
			(runtimeUntested && !isLikelyConversationalMLXModel(configuration)) {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(base, modelDirectory)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(modelDirectory)
		if err != nil {
			return err
		}
		var size int64
		var modified time.Time
		for _, modelEntry := range entries {
			if modelEntry.IsDir() {
				continue
			}
			info, infoErr := modelEntry.Info()
			if infoErr != nil || !info.Mode().IsRegular() {
				continue
			}
			size += info.Size()
			if info.ModTime().After(modified) {
				modified = info.ModTime()
			}
		}
		result = append(result, domain.ModelFile{
			Path:            filepath.ToSlash(relative),
			Format:          "mlx",
			Size:            size,
			ModifiedAt:      modified,
			Architecture:    architecture,
			RuntimeUntested: runtimeUntested,
			DFlashSupported: !runtimeUntested && !isDraft && isSupportedDFlashTargetArchitecture(architecture),
			DFlashDraft:     isDFlashDraft,
			DFlashVariant:   mlxDFlashVariant(configuration),
			MTPSupported:    !runtimeUntested && !isDraft && isSupportedMLXMTPConfiguration(configuration),
			MTPDraft:        isMTPDraft,
			MTPBlockSize:    configuration.BlockSize,
			DraftKind: func() string {
				if isMTPDraft {
					return "mtp"
				}
				if isDFlashDraft {
					return "dflash"
				}
				return ""
			}(),
		})
		if len(result) >= 5000 {
			return io.EOF
		}
		return filepath.SkipDir
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Path) < strings.ToLower(result[j].Path) })
	return result, nil
}

type mlxModelConfiguration struct {
	Architectures         []string        `json:"architectures"`
	ModelType             string          `json:"model_type"`
	TextConfig            json.RawMessage `json:"text_config"`
	HiddenSize            int             `json:"hidden_size"`
	BackboneHiddenSize    int             `json:"backbone_hidden_size"`
	HiddenLayers          int             `json:"num_hidden_layers"`
	VocabularySize        int             `json:"vocab_size"`
	AttentionHeads        int             `json:"num_attention_heads"`
	KVHeads               int             `json:"num_key_value_heads"`
	FullAttentionInterval int             `json:"full_attention_interval"`
	BlockSize             int             `json:"block_size"`
	LayerTypes            []string        `json:"layer_types"`
	SlidingWindow         int             `json:"sliding_window"`
	DFlash                struct {
		ConvolutionKernelSize int `json:"conv_kernel_size"`
		ConvolutionGroupSize  int `json:"conv_group_size"`
		SelectorRank          int `json:"selector_rank"`
		SelectorTopK          int `json:"selector_top_k"`
	} `json:"dflash_config"`
}

func (configuration mlxModelConfiguration) effectiveTextConfiguration() mlxModelConfiguration {
	if len(configuration.TextConfig) == 0 || string(configuration.TextConfig) == "null" {
		return configuration
	}
	var text mlxModelConfiguration
	if json.Unmarshal(configuration.TextConfig, &text) != nil {
		return configuration
	}
	if text.ModelType == "" {
		text.ModelType = configuration.ModelType
	}
	if text.BlockSize == 0 {
		text.BlockSize = configuration.BlockSize
	}
	return text
}

func isMLXMTPDraftConfiguration(configuration mlxModelConfiguration) bool {
	switch normalizeModelArchitecture(configuration.ModelType) {
	case "qwen3_5_mtp", "gemma4_assistant":
		return true
	default:
		return false
	}
}

func isSupportedMLXMTPDraftConfiguration(configuration mlxModelConfiguration) bool {
	if !isMLXMTPDraftConfiguration(configuration) {
		return false
	}
	text := configuration.effectiveTextConfiguration()
	return text.HiddenSize > 0 && text.HiddenLayers > 0 && text.VocabularySize > 0
}

func isSupportedMLXMTPConfiguration(configuration mlxModelConfiguration) bool {
	if isMLXMTPDraftConfiguration(configuration) || isMLXDFlashDraftConfiguration(configuration) {
		return false
	}
	switch normalizeModelArchitecture(configuration.ModelType) {
	case "qwen3_5", "qwen3_5_moe", "qwen3_5_text", "qwen3_5_moe_text", "gemma4", "gemma4_text":
		return true
	default:
		return false
	}
}

func isMLXMTPDraftDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	return err == nil && isMLXMTPDraftConfiguration(configuration)
}

func isSupportedMLXMTPDraftDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	return err == nil && isSupportedMLXMTPDraftConfiguration(configuration)
}

func isSupportedMLXMTPTargetDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	return err == nil && isSupportedMLXMTPConfiguration(configuration)
}

func validateMLXMTPPair(targetDirectory, draftDirectory string) error {
	target, err := readMLXModelConfiguration(targetDirectory)
	if err != nil {
		return fmt.Errorf("讀取 MTP Target 設定失敗: %w", err)
	}
	draft, err := readMLXModelConfiguration(draftDirectory)
	if err != nil {
		return fmt.Errorf("讀取 MTP Draft 設定失敗: %w", err)
	}
	if !isSupportedMLXMTPConfiguration(target) || !isSupportedMLXMTPDraftConfiguration(draft) {
		return errors.New("MTP Target 或 Draft 架構不受支援")
	}
	target = target.effectiveTextConfiguration()
	draftType := normalizeModelArchitecture(draft.ModelType)
	draftText := draft.effectiveTextConfiguration()
	targetType := normalizeModelArchitecture(target.ModelType)
	if targetType == "" {
		targetType = normalizeModelArchitecture(target.effectiveTextConfiguration().ModelType)
	}

	if draftType == "gemma4_assistant" {
		if targetType != "gemma4" && targetType != "gemma4_text" {
			return fmt.Errorf("MTP Draft 架構 %s 不支援 Target 架構 %s", draftType, targetType)
		}
		if target.VocabularySize <= 0 || draftText.VocabularySize <= 0 ||
			target.VocabularySize != draftText.VocabularySize {
			return fmt.Errorf(
				"MTP Draft 與 Target 不相容：vocab_size target=%d draft=%d",
				target.VocabularySize, draftText.VocabularySize,
			)
		}
		if draft.BackboneHiddenSize > 0 &&
			(target.HiddenSize <= 0 || target.HiddenSize != draft.BackboneHiddenSize) {
			return fmt.Errorf(
				"MTP Draft 與 Target 不相容：backbone_hidden_size target=%d draft=%d",
				target.HiddenSize, draft.BackboneHiddenSize,
			)
		}
		return nil
	}

	if draftType != "qwen3_5_mtp" ||
		(targetType != "qwen3_5" && targetType != "qwen3_5_moe" &&
			targetType != "qwen3_5_text" && targetType != "qwen3_5_moe_text") {
		return fmt.Errorf("MTP Draft 架構 %s 不支援 Target 架構 %s", draftType, targetType)
	}
	draft = draftText
	checks := []struct {
		name          string
		target, draft int
	}{
		{"hidden_size", target.HiddenSize, draft.HiddenSize},
		{"vocab_size", target.VocabularySize, draft.VocabularySize},
		{"num_hidden_layers", target.HiddenLayers, draft.HiddenLayers},
		{"num_attention_heads", target.AttentionHeads, draft.AttentionHeads},
		{"num_key_value_heads", target.KVHeads, draft.KVHeads},
		{"full_attention_interval", target.FullAttentionInterval, draft.FullAttentionInterval},
	}
	for _, check := range checks {
		if check.target <= 0 || check.draft <= 0 || check.target != check.draft {
			return fmt.Errorf(
				"MTP Draft 與 Target 不相容：%s target=%d draft=%d",
				check.name, check.target, check.draft,
			)
		}
	}
	return nil
}

func readMLXModelConfiguration(directory string) (mlxModelConfiguration, error) {
	content, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		return mlxModelConfiguration{}, err
	}
	var configuration mlxModelConfiguration
	if err := json.Unmarshal(content, &configuration); err != nil {
		return mlxModelConfiguration{}, err
	}
	return configuration, nil
}

func isMLXDFlashDraftDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	return err == nil && isMLXDFlashDraftConfiguration(configuration)
}

func isMLXDFlashDraftConfiguration(configuration mlxModelConfiguration) bool {
	for _, architecture := range configuration.Architectures {
		if architecture == "DFlashDraftModel" || architecture == "DFlash2DraftModel" {
			return true
		}
	}
	return false
}

func isSupportedMLXDFlashDraftDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	return err == nil && isSupportedMLXDFlashDraftConfiguration(configuration)
}

func isSupportedMLXDFlashTargetDirectory(directory string) bool {
	configuration, err := readMLXModelConfiguration(directory)
	return err == nil && !isMLXDFlashDraftConfiguration(configuration) && isSupportedDFlashTargetArchitecture(configuration.ModelType)
}

func isSupportedMLXDFlashDraftConfiguration(configuration mlxModelConfiguration) bool {
	if !isSupportedDFlashTargetArchitecture(configuration.ModelType) {
		return false
	}
	foundVariants := 0
	isDFlash2 := false
	for _, architecture := range configuration.Architectures {
		if architecture == "DFlash2DraftModel" {
			foundVariants++
			isDFlash2 = true
		}
		if architecture == "DFlashDraftModel" {
			foundVariants++
		}
	}
	if foundVariants != 1 || len(configuration.LayerTypes) == 0 {
		return false
	}
	for _, layerType := range configuration.LayerTypes {
		if layerType != "full_attention" && layerType != "sliding_attention" {
			return false
		}
		if layerType == "sliding_attention" && configuration.SlidingWindow <= 1 {
			return false
		}
	}
	if isDFlash2 {
		config := configuration.DFlash
		if config.ConvolutionKernelSize < 2 || config.ConvolutionGroupSize <= 0 ||
			configuration.HiddenSize <= 0 || configuration.HiddenSize%config.ConvolutionGroupSize != 0 ||
			config.SelectorRank <= 0 || config.SelectorTopK <= 1 {
			return false
		}
	}
	return true
}

func mlxDFlashVariant(configuration mlxModelConfiguration) string {
	for _, architecture := range configuration.Architectures {
		if architecture == "DFlash2DraftModel" {
			return "dflash2"
		}
		if architecture == "DFlashDraftModel" {
			return "dflash1"
		}
	}
	return ""
}

// withoutModelLoadModeArguments 讓執行狀態頁的 MMap Switch 成為唯一來源。
// llama.cpp 已以 --load-mode 取代舊的 mmap、mlock 與 direct-io 旗標；若
// Profile 仍保存舊參數，啟動時一律移除，避免最後出現順序相依的衝突。
func withoutModelLoadModeArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		switch argument {
		case "--load-mode", "-lm":
			if index+1 < len(arguments) {
				index++
			}
			continue
		case "--mmap", "--no-mmap", "--mlock",
			"--direct-io", "--no-direct-io", "-dio", "-ndio":
			continue
		}
		if strings.HasPrefix(argument, "--load-mode=") || strings.HasPrefix(argument, "-lm=") {
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

// withoutMMapMemoryArguments 讓啟動參數中的 MMap 記憶體保留選單成為
// --fit、--fit-target 與 GPU Layers 的唯一來源。啟用保留目標時，GPU Layers
// 必須維持自動，llama-server 才能依記憶體餘裕調整權重配置。
func withoutMMapMemoryArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		switch argument {
		case "--fit", "-fit":
			if index+1 < len(arguments) {
				next := strings.ToLower(strings.TrimSpace(arguments[index+1]))
				if next == "on" || next == "off" {
					index++
				}
			}
			continue
		case "--fit-target", "-fitt", "--gpu-layers", "--n-gpu-layers", "-ngl":
			if index+1 < len(arguments) {
				index++
			}
			continue
		}
		if strings.HasPrefix(argument, "--fit=") || strings.HasPrefix(argument, "-fit=") ||
			strings.HasPrefix(argument, "--fit-target=") || strings.HasPrefix(argument, "-fitt=") ||
			strings.HasPrefix(argument, "--gpu-layers=") || strings.HasPrefix(argument, "--n-gpu-layers=") ||
			strings.HasPrefix(argument, "-ngl=") {
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

// withoutMLXMMapArguments 讓進階設定中的 MMap Switch 與 Profile 的保留目標
// 成為 mlx-server MMap 的唯一來源，避免額外參數以順序覆蓋畫面選擇。
func withoutMLXMMapArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		switch argument {
		case "--mmap", "--no-mmap":
			continue
		case "--mmap-reserve-gb":
			if index+1 < len(arguments) {
				index++
			}
			continue
		}
		if strings.HasPrefix(argument, "--mmap-reserve-gb=") {
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

// withManagedMLXGGUFOptimization 讓執行狀態頁的快速 GGUF 開關與系統設定的
// 策略選項共同管理 GGUF 量化參數。關閉快速模式時走一般 auto 路徑；開啟時，
// 預設使用 speed + auto，Beta 1／Beta 2 則依序使用 speed-passthrough 搭配
// Group 32／64。策略判定只依設定值與 tensor metadata，不依模型名稱。
// 原生 MLX 模型會移除殘留的 GGUF 參數；思考模式維持 Runtime／模型預設。
func withManagedMLXGGUFOptimization(
	arguments []string,
	isGGUF, fastMode bool,
	strategy string,
) []string {
	filtered := withoutMLXGGUFOptimizationArguments(arguments)
	if !isGGUF {
		return filtered
	}
	if !fastMode {
		return append(filtered,
			"--gguf-profile", "auto",
			"--gguf-group-size", "auto",
			"--gguf-recurrent-promotion", "off",
		)
	}

	// 預設 Mode 1：K-quant 沿用來源 4-bit block。實測量化位元寬度不影響輸出
	// 品質，因此以速度為預設取向；Mode 2 保留為保守路徑。
	profile := normalizedFastGGUFProfile(strategy)
	groupSize := "auto"
	return append(filtered,
		"--gguf-profile", profile,
		"--gguf-group-size", groupSize,
		"--gguf-recurrent-promotion", "controls",
	)
}

func normalizedFastGGUFProfile(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case domain.FastGGUFStrategyMode2, domain.FastGGUFStrategyLegacyDefault:
		return "mode2"
	case domain.FastGGUFStrategyMode3:
		return "mode3"
	default:
		return "mode1"
	}
}

func withoutMLXGGUFOptimizationArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		switch argument {
		case "--gguf-profile", "--gguf-group-size", "--gguf-recurrent-promotion":
			if index+1 < len(arguments) {
				index++
			}
			continue
		}
		if strings.HasPrefix(argument, "--gguf-profile=") ||
			strings.HasPrefix(argument, "--gguf-group-size=") ||
			strings.HasPrefix(argument, "--gguf-recurrent-promotion=") {
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

func withoutMLXGGUFCacheArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		if argument == "--no-gguf-cache" {
			continue
		}
		if argument == "--gguf-cache-dir" {
			if index+1 < len(arguments) {
				index++
			}
			continue
		}
		if strings.HasPrefix(argument, "--gguf-cache-dir=") {
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

// withManagedKVCacheQuantization 依 Runtime 產生對應的 Q8／Q4 參數。
// 先移除 Profile 額外參數中的舊設定，確保 Switch 與量化選單是唯一來源。
func withManagedKVCacheQuantization(arguments []string, runtimeName, quantization string) []string {
	filtered := withoutManagedKVCacheArguments(arguments)
	if quantization == domain.KVCacheQuantizationNone {
		return filtered
	}

	if runtimeName == domain.RuntimeMLXServer {
		bits := "8"
		if quantization == domain.KVCacheQuantizationQ4 {
			bits = "4"
		}
		return append(filtered,
			"--kv-bits", bits,
			"--kv-group-size", "64",
			"--quantized-kv-start", "2048",
		)
	}

	cacheType := "q8_0"
	if quantization == domain.KVCacheQuantizationQ4 {
		cacheType = "q4_0"
	}
	return append(filtered,
		"--cache-type-k", cacheType,
		"--cache-type-v", cacheType,
		"--flash-attn", "on",
	)
}

func withoutManagedKVCacheArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		name := argument
		if separator := strings.IndexByte(name, '='); separator >= 0 {
			name = name[:separator]
		}
		if !isManagedKVCacheArgument(name) {
			filtered = append(filtered, arguments[index])
			continue
		}
		if !strings.Contains(argument, "=") && index+1 < len(arguments) {
			index++
		}
	}
	return filtered
}

func isManagedKVCacheArgument(argument string) bool {
	switch strings.TrimSpace(argument) {
	case "--cache-type-k", "-ctk", "--cache-type-v", "-ctv",
		"--kv-bits", "--kv-group-size", "--kv-scheme", "--quantized-kv-start":
		return true
	default:
		return false
	}
}

func withoutDFlashArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		switch argument {
		case "--model-draft", "--dflash-draft", "--dflash-block-size":
			if index+1 < len(arguments) {
				index++
			}
			continue
		case "--spec-type":
			if index+1 >= len(arguments) {
				continue
			}
			index++
			if value := withoutDFlashSpecType(arguments[index]); value != "" {
				filtered = append(filtered, "--spec-type", value)
			}
			continue
		}
		if strings.HasPrefix(argument, "--model-draft=") ||
			strings.HasPrefix(argument, "--dflash-draft=") ||
			strings.HasPrefix(argument, "--dflash-block-size=") {
			continue
		}
		if strings.HasPrefix(argument, "--spec-type=") {
			if value := withoutDFlashSpecType(strings.TrimPrefix(argument, "--spec-type=")); value != "" {
				filtered = append(filtered, "--spec-type="+value)
			}
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

func hasAnyArgument(arguments []string, expected ...string) bool {
	for _, rawArgument := range arguments {
		argument := strings.TrimSpace(rawArgument)
		for _, candidate := range expected {
			if argument == candidate || strings.HasPrefix(argument, candidate+"=") {
				return true
			}
		}
	}
	return false
}

// withoutMLXRotatingKVArguments 移除會讓 mlx-server 建立 rotating target KV
// Cache 的旗標。DFlash 不支援 rotating cache，留著只會讓推測解碼靜默降級。
func withoutMLXRotatingKVArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		name := argument
		if separator := strings.IndexByte(name, '='); separator >= 0 {
			name = name[:separator]
		}
		if name != "--max-kv-size" && name != "--ctx-size" {
			filtered = append(filtered, arguments[index])
			continue
		}
		if name == argument && index+1 < len(arguments) {
			index++
		}
	}
	return filtered
}

func withoutDraftModelArguments(arguments []string) []string {
	filtered := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := strings.TrimSpace(arguments[index])
		if argument == "--model-draft" || argument == "--dflash-draft" || argument == "--mtp-draft" {
			if index+1 < len(arguments) {
				index++
			}
			continue
		}
		if strings.HasPrefix(argument, "--model-draft=") || strings.HasPrefix(argument, "--dflash-draft=") ||
			strings.HasPrefix(argument, "--mtp-draft=") {
			continue
		}
		filtered = append(filtered, arguments[index])
	}
	return filtered
}

func withoutDFlashSpecType(value string) string {
	values := strings.Split(value, ",")
	filtered := values[:0]
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item != "" && !strings.EqualFold(item, "draft-dflash") {
			filtered = append(filtered, item)
		}
	}
	return strings.Join(filtered, ",")
}

func isMLXModelDirectory(directory string) bool {
	configInfo, err := os.Stat(filepath.Join(directory, "config.json"))
	if err != nil || !configInfo.Mode().IsRegular() {
		return false
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false
	}
	hasSafetensors := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".safetensors") {
			hasSafetensors = true
		}
	}
	if !hasSafetensors {
		return false
	}
	indexPath := filepath.Join(directory, "model.safetensors.index.json")
	content, err := os.ReadFile(indexPath)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	var index struct {
		WeightMap map[string]string `json:"weight_map"`
	}
	if err := json.Unmarshal(content, &index); err != nil || len(index.WeightMap) == 0 {
		return false
	}
	for _, filename := range index.WeightMap {
		path, joinErr := download.SafeJoin(directory, filepath.ToSlash(filename))
		if joinErr != nil {
			return false
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func isExecutable(info os.FileInfo) bool {
	return runtime.GOOS == "windows" || info.Mode().Perm()&0111 != 0
}

type logBuffer struct {
	mu    sync.Mutex
	max   int
	bytes bytes.Buffer
}

type runtimeOutputWriter struct {
	destination io.Writer
	onLine      func(string)
	mu          sync.Mutex
	pending     string
}

func newRuntimeOutputWriter(destination io.Writer, onLine func(string)) *runtimeOutputWriter {
	return &runtimeOutputWriter{destination: destination, onLine: onLine}
}

func (w *runtimeOutputWriter) Write(value []byte) (int, error) {
	count, err := w.destination.Write(value)
	if count <= 0 || w.onLine == nil {
		return count, err
	}

	w.mu.Lock()
	w.pending += string(value[:count])
	lines := make([]string, 0, 2)
	for {
		newline := strings.IndexByte(w.pending, '\n')
		if newline < 0 {
			break
		}
		lines = append(lines, strings.TrimSuffix(w.pending[:newline], "\r"))
		w.pending = w.pending[newline+1:]
	}
	if len(w.pending) > 64*1024 {
		w.pending = w.pending[len(w.pending)-64*1024:]
	}
	w.mu.Unlock()

	for _, line := range lines {
		w.onLine(line)
	}
	return count, err
}

func newLogBuffer(max int) *logBuffer {
	return &logBuffer{max: max}
}

func (b *logBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	count, err := b.bytes.Write(value)
	b.trimLocked()
	return count, err
}

func (b *logBuffer) Append(value string) {
	_, _ = b.Write([]byte(value))
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bytes.String()
}

func (b *logBuffer) Reset() {
	b.mu.Lock()
	b.bytes.Reset()
	b.mu.Unlock()
}

func (b *logBuffer) trimLocked() {
	if b.bytes.Len() <= b.max {
		return
	}
	content := b.bytes.Bytes()
	start := len(content) - b.max
	if nextLine := bytes.IndexByte(content[start:], '\n'); nextLine >= 0 {
		start += nextLine + 1
	}
	trimmed := append([]byte(nil), content[start:]...)
	b.bytes.Reset()
	_, _ = b.bytes.Write(trimmed)
}
