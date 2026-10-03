package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"LlamaLoader/src/domain"
)

// RingModel 是初步握手資訊；完整權重與 Runtime 雜湊仍由原生 Runtime 核對。
type RingModel struct {
	Path         string `json:"path"`
	Architecture string `json:"architecture"`
	Fingerprint  string `json:"fingerprint"`
	Kind         string `json:"kind,omitempty"`
	Format       string `json:"format,omitempty"`
	MMProj       string `json:"mmproj,omitempty"`
}

// Launch 選項與單機啟動相同；只有 Target 相關欄位會傳至 worker。
type RingOptions struct {
	MMProj                    string `json:"mmproj,omitempty"`
	DraftModel                string `json:"draft_model,omitempty"`
	DFlashEnabled             bool   `json:"dflash_enabled,omitempty"`
	FastGGUFEnabled           bool   `json:"fast_gguf_enabled,omitempty"`
	GGUFStrategy              string `json:"gguf_strategy,omitempty"`
	TextOnly                  bool   `json:"text_only,omitempty"`
	ConversionConfirmationKey string `json:"conversion_confirmation_key,omitempty"`
}

const MaxRingNodes = 8

type RingCapabilities struct {
	MaxNodes               int      `json:"max_ring_nodes"`
	Version                string   `json:"version"`
	Available              bool     `json:"ring_available"`
	ManagedParentStdin     bool     `json:"managed_parent_stdin"`
	GenericLinearSharding  bool     `json:"generic_linear_sharding,omitempty"`
	TextModelTypes         []string `json:"text_model_types,omitempty"`
	VisionModelTypes       []string `json:"vision_model_types,omitempty"`
	GGUFModelTypes         []string `json:"gguf_model_types,omitempty"`
	GGUFSharding           bool     `json:"gguf_sharding,omitempty"`
	SpeculativeCoordinator bool     `json:"speculative_coordinator,omitempty"`
}

func (m *Manager) RingModelDirectory(model string) string {
	if strings.HasPrefix(model, mlxGGUFPathPrefix) {
		return m.settings().ModelDirectory
	}
	return m.settings().MLXModelDirectory
}

func (m *Manager) RingCapabilities(ctx context.Context) (RingCapabilities, error) {
	var result RingCapabilities
	binary, err := ResolveMLXServer()
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, binary, "--distributed-capabilities").Output()
	if err != nil {
		return result, fmt.Errorf("無法取得原生 TCP Ring 能力，請更新 mlx-server：%w", err)
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	if !result.Available || !result.ManagedParentStdin || result.Version == "" || result.MaxNodes < 2 {
		return result, errors.New("目前 mlx-server 不支援 Server 管理的 TCP Ring，請更新原生 Runtime")
	}
	return result, nil
}

func (m *Manager) InspectRingModel(model string, launch ...RingOptions) (RingModel, error) {
	var options RingOptions
	if len(launch) > 0 {
		options = launch[0]
	}
	settings := m.settings()
	selection, err := resolveMLXTargetSelection(settings, model, options.MMProj)
	directory := selection.modelArgument
	if err != nil {
		return RingModel{}, err
	}
	base, err := filepath.Abs(m.RingModelDirectory(model))
	if err != nil {
		return RingModel{}, err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return RingModel{}, err
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return RingModel{}, err
	}
	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return RingModel{}, errors.New("TCP Ring 模型不可透過符號連結離開 MLX 模型目錄")
	}

	if selection.isGGUF {
		capability, err := m.RingCapabilities(context.Background())
		if err != nil {
			return RingModel{}, err
		}
		if !capability.GGUFSharding {
			return RingModel{}, errors.New("請更新 Runtime 以支援 GGUF 分片")
		}
		architecture, err := mlxGGUFModelArchitecture(directory)
		if err != nil {
			return RingModel{}, err
		}
		if !slices.ContainsFunc(capability.GGUFModelTypes, func(value string) bool {
			return canonicalMLXGGUFArchitecture(value) == canonicalMLXGGUFArchitecture(architecture)
		}) {
			return RingModel{}, errors.New("Runtime 尚未支援此 GGUF 架構")
		}
		format, kind, projector := "gguf", "text", ""
		if isFastGGUFManifestPath(directory) {
			format = "fastgguf"
			pkg, err := readStandaloneFastGGUFPackage(directory)
			if err != nil {
				return RingModel{}, err
			}
			hasProcessor := pkg.manifest.ProcessorConfiguration != ""
			if pkg.manifest.SchemaVersion == 3 {
				for _, name := range []string{"preprocessor_config.json", "processor_config.json"} {
					if info, err := os.Stat(filepath.Join(filepath.Dir(directory), name)); err == nil && info.Mode().IsRegular() {
						hasProcessor = true
					}
				}
			}
			if hasProcessor {
				kind = "vision"
			}
		} else if selection.mmprojArgument != "" {
			if filepath.Dir(selection.mmprojArgument) != filepath.Dir(directory) {
				return RingModel{}, errors.New("叢集的 GGUF 與 mmproj 必須放在同一模型目錄")
			}
			projector = filepath.Base(selection.mmprojArgument)
			kind = "vision"
		}
		return RingModel{Path: mlxGGUFPathPrefix + filepath.ToSlash(relative), Architecture: architecture, Kind: kind, Format: format, MMProj: projector}, nil
	}
	data, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		return RingModel{}, err
	}
	capability, err := m.RingCapabilities(context.Background())
	if err != nil {
		return RingModel{}, err
	}
	architecture, err := inspectRingArchitecture(data, capability)
	if err != nil {
		return RingModel{}, err
	}
	digest := sha256.Sum256(data)
	kind := "text"
	hasProcessor := false
	for _, name := range []string{"processor_config.json", "preprocessor_config.json"} {
		if info, err := os.Stat(filepath.Join(directory, name)); err == nil && info.Mode().IsRegular() {
			hasProcessor = true
		}
	}
	if !slices.Contains(capability.TextModelTypes, architecture) || (!options.TextOnly && !options.DFlashEnabled && hasProcessor && slices.Contains(capability.VisionModelTypes, architecture)) {
		kind = "vision"
	}
	return RingModel{Path: filepath.ToSlash(filepath.Clean(strings.TrimSpace(model))), Architecture: architecture, Kind: kind,
		Fingerprint: hex.EncodeToString(digest[:])}, nil
}

// 架構能力由原生 Runtime 的文字／影像模型註冊表提供，不在 Go 重複維護模型名單。
func inspectRingArchitecture(data []byte, capability RingCapabilities) (string, error) {
	var config struct {
		ModelType string `json:"model_type"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", err
	}
	if !capability.GenericLinearSharding || len(capability.TextModelTypes)+len(capability.VisionModelTypes) == 0 {
		return "", errors.New("請更新原生 Runtime，才能檢查通用 TCP Ring 模型能力")
	}
	if config.ModelType == "" || (!slices.Contains(capability.TextModelTypes, config.ModelType) && !slices.Contains(capability.VisionModelTypes, config.ModelType)) {
		return "", errors.New("目前 Runtime 尚未註冊此模型架構；請更新 Runtime 或使用已註冊的模型")
	}
	return config.ModelType, nil
}

// 保留與啟動共用 Manager 的鎖，避免一般載入、同時互邀或校準搶用 GPU。
func (m *Manager) ReserveRing(owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner == "" || m.distributedOwner != "" {
		return errors.New("此 Server 已保留給另一組 TCP Ring")
	}
	m.distributedOwner = owner
	return nil
}

func (m *Manager) ReleaseRing(owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.distributedOwner == owner {
		m.distributedOwner = ""
	}
}

func ValidateRingProfile(profile domain.StartupCommand) error {
	if profile.Runtime != domain.RuntimeMLXServer || profile.RuntimeVariant != "" {
		return errors.New("TCP Ring 需要 mlx-server 啟動參數")
	}
	for _, arg := range profile.ExtraArgs {
		if strings.HasPrefix(arg, "--distributed-") || strings.HasPrefix(arg, "--mtp-draft") || strings.HasPrefix(arg, "--dflash-draft") ||
			strings.HasPrefix(arg, "--mmproj") || strings.HasPrefix(arg, "--model-type") {
			return errors.New("TCP Ring 自動設定模型與節點，請由模型選擇器指定 Draft，移除手動分散式及路徑參數")
		}
	}
	return nil
}

func (m *Manager) StartRing(owner, model string, rank int, addresses []string, profile domain.StartupCommand, launch ...RingOptions) (domain.LlamaStatus, error) {
	var options RingOptions
	if len(launch) > 0 {
		options = launch[0]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner == "" || m.distributedOwner != owner || rank < 0 || rank >= len(addresses) || len(addresses) < 2 || len(addresses) > MaxRingNodes {
		return m.status, errors.New("TCP Ring 保留已失效")
	}
	if err := ValidateRingProfile(profile); err != nil {
		return m.status, err
	}
	descriptor, err := m.InspectRingModel(model, options)
	if err != nil {
		return m.status, err
	}
	nodes := make([]map[string]string, len(addresses))
	seen := make(map[string]bool)
	for i, address := range addresses {
		host, port, err := net.SplitHostPort(address)
		n, _ := strconv.Atoi(port)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() || n < 1024 || n > 65535 {
			return m.status, errors.New("TCP Ring 位址必須是有效 IPv4 與非特權埠")
		}
		if seen[address] {
			return m.status, errors.New("TCP Ring 的節點端點不能重複")
		}
		seen[address] = true
		nodes[i] = map[string]string{"launch": "manual", "ringAddress": address}
	}
	data, err := json.Marshal(map[string]any{"version": 1, "backend": "ring", "nodes": nodes})
	if err != nil {
		return m.status, err
	}
	profile.ExtraArgs = append(append([]string(nil), profile.ExtraArgs...),
		"--distributed-config-base64", base64.StdEncoding.EncodeToString(data), "--distributed-parent-stdin", "--model-type", descriptor.Kind)
	if rank != 0 {
		profile.ExtraArgs = append(profile.ExtraArgs, "--distributed-rank", strconv.Itoa(rank))
	}
	if options.GGUFStrategy != "" {
		profile.ClusterGGUFStrategy = options.GGUFStrategy
	}
	status, err := m.startLocked(model, options.MMProj, options.DraftModel, options.DFlashEnabled, false, options.FastGGUFEnabled,
		profile.KVCacheQuantization != "" && profile.KVCacheQuantization != domain.KVCacheQuantizationNone,
		false, options.ConversionConfirmationKey, profile, true)
	if err == nil {
		m.status.DistributedRole = "coordinator"
		if rank != 0 {
			m.status.DistributedRole = "worker"
		}
		status = m.status
	}
	return status, err
}
