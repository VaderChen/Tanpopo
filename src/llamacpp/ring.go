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
}

const MaxRingNodes = 8

type RingCapabilities struct {
	MaxNodes              int      `json:"max_ring_nodes"`
	Version               string   `json:"version"`
	Available             bool     `json:"ring_available"`
	ManagedParentStdin    bool     `json:"managed_parent_stdin"`
	GenericLinearSharding bool     `json:"generic_linear_sharding,omitempty"`
	TextModelTypes        []string `json:"text_model_types,omitempty"`
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

func (m *Manager) InspectRingModel(model string) (RingModel, error) {
	settings := m.settings()
	directory, err := resolveMLXModel(settings.MLXModelDirectory, model, "TCP Ring 模型")
	if err != nil {
		return RingModel{}, err
	}
	base, err := filepath.Abs(settings.MLXModelDirectory)
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
	return RingModel{Path: filepath.ToSlash(filepath.Clean(model)), Architecture: architecture,
		Fingerprint: hex.EncodeToString(digest[:])}, nil
}

// 架構能力由原生 Runtime 的文字模型註冊表提供，不在 Go 重複維護模型名單。
func inspectRingArchitecture(data []byte, capability RingCapabilities) (string, error) {
	var config struct {
		ModelType string `json:"model_type"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", err
	}
	if !capability.GenericLinearSharding || len(capability.TextModelTypes) == 0 {
		return "", errors.New("請更新原生 Runtime，才能檢查通用 TCP Ring 模型能力")
	}
	if config.ModelType == "" || !slices.Contains(capability.TextModelTypes, config.ModelType) {
		return "", errors.New("目前 Runtime 不支援此模型的文字分散推論，請更新 Runtime 或選擇已支援的文字模型")
	}
	return config.ModelType, nil
}

// 保留與啟動共用 Manager 的鎖，避免一般載入、同時互邀或校準搶用 GPU。
func (m *Manager) ReserveRing(owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner == "" || m.distributedOwner != "" || m.status.Running {
		return errors.New("此 Server 正在執行模型或已保留給另一組 TCP Ring")
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

func (m *Manager) StartRing(owner, model string, rank int, addresses []string, profile domain.StartupCommand) (domain.LlamaStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner == "" || m.distributedOwner != owner || rank < 0 || rank >= len(addresses) || len(addresses) < 2 || len(addresses) > MaxRingNodes {
		return m.status, errors.New("TCP Ring 保留已失效")
	}
	if profile.Runtime != domain.RuntimeMLXServer || profile.RuntimeVariant != "" || profile.DraftModel != "" {
		return m.status, errors.New("TCP Ring 需要標準 mlx-server 啟動參數，不能搭配 Draft")
	}
	for _, arg := range profile.ExtraArgs {
		if strings.HasPrefix(arg, "--distributed-") || strings.HasPrefix(arg, "--mtp-") || strings.HasPrefix(arg, "--dflash-") ||
			strings.HasPrefix(arg, "--mmproj") || strings.HasPrefix(arg, "--model-type") {
			return m.status, errors.New("TCP Ring 會自動設定節點，請移除既有分散式、多模態或 Draft 參數")
		}
	}
	if _, err := m.InspectRingModel(model); err != nil {
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
		"--distributed-config-base64", base64.StdEncoding.EncodeToString(data), "--distributed-parent-stdin")
	if rank != 0 {
		profile.ExtraArgs = append(profile.ExtraArgs, "--distributed-rank", strconv.Itoa(rank))
	}
	status, err := m.startLocked(model, "", "", false, false, false,
		profile.KVCacheQuantization != "" && profile.KVCacheQuantization != domain.KVCacheQuantizationNone,
		false, "", profile, true)
	if err == nil {
		m.status.DistributedRole = "coordinator"
		if rank != 0 {
			m.status.DistributedRole = "worker"
		}
		status = m.status
	}
	return status, err
}
