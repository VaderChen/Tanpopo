package llamacpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"LlamaLoader/src/domain"
)

// RuntimeCapability 是選項顯示與實際啟動共用的能力快照。
// 可用代表裝置列舉與介面檢查通過，不代表任意模型均能放入 GPU 記憶體。
type RuntimeCapability struct {
	Variant     string `json:"variant"`
	Runtime     string `json:"runtime"`
	Label       string `json:"label"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason,omitempty"`
	Backend     string `json:"backend"`
	Mode        string `json:"mode,omitempty"`
	Device      string `json:"device,omitempty"`
	DeviceName  string `json:"device_name,omitempty"`
	Source      string `json:"source,omitempty"`
	Commit      string `json:"commit,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Binary      string `json:"-"`
}

type runtimeManifest struct {
	Schema   int      `json:"schema"`
	Source   string   `json:"source"`
	Commit   string   `json:"commit"`
	Platform string   `json:"platform"`
	Backend  string   `json:"backend"`
	Modes    []string `json:"modes"`
}

var runtimeCapabilityCache struct {
	sync.Mutex
	checked time.Time
	value   []RuntimeCapability
}

var amdVulkanDevicePattern = regexp.MustCompile(`(?mi)^\s*(Vulkan[0-9]+):\s*(.+)$`)
var amdDeviceNamePattern = regexp.MustCompile(`(?i)\b(AMD|Radeon|RADV)\b`)

// 僅採 GPU 本身的架構／產品識別，不由 AMD CPU 或一般 RDNA 3.5 推定 Strix Halo。
var strixHaloDevicePattern = regexp.MustCompile(`(?i)\b(gfx1151|strix[ _-]+halo|Radeon\s+(?:Graphics\s+)?80[456]0S)\b`)
var runtimeCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

// RuntimeCapabilities 只短暫快取探測結果，安裝新成品後重新整理即可重新偵測。
func RuntimeCapabilities() []RuntimeCapability {
	runtimeCapabilityCache.Lock()
	defer runtimeCapabilityCache.Unlock()
	if time.Since(runtimeCapabilityCache.checked) >= 15*time.Second {
		runtimeCapabilityCache.value = detectRuntimeVariants()
		runtimeCapabilityCache.checked = time.Now()
	}
	return append([]RuntimeCapability(nil), runtimeCapabilityCache.value...)
}

// 在套用校準前更新成品識別，避免剛替換執行檔時沿用 15 秒內的舊快照。
func RefreshRuntimeCapabilities() {
	runtimeCapabilityCache.Lock()
	defer runtimeCapabilityCache.Unlock()
	runtimeCapabilityCache.value = detectRuntimeVariants()
	runtimeCapabilityCache.checked = time.Now()
}

func detectRuntimeVariants() []RuntimeCapability {
	return []RuntimeCapability{
		detectAMDRuntime("auto"),
	}
}

func detectAMDRuntime(mode string) RuntimeCapability {
	result := RuntimeCapability{
		Variant: domain.RuntimeVariantAMDVulkan, Runtime: domain.RuntimeLlamaServer,
		Label: "LLaMA Server（AMD Vulkan）", Backend: "vulkan",
	}
	if mode != "auto" && mode != "vulkan" && mode != "strix-halo" {
		result.Reason = "不支援的 AMD 執行模式"
		return result
	}
	if (runtime.GOOS != "linux" && runtime.GOOS != "windows") || runtime.GOARCH != "amd64" {
		result.Reason = "AMD Vulkan Runtime 目前僅支援 Linux／Windows x64"
		return result
	}
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	result.Reason = "尚未安裝相容的 AMD Vulkan Runtime"
	for _, root := range runtimeRoots() {
		directory := filepath.Join(root, "llama-runtime", "variants", domain.RuntimeVariantAMDVulkan, runtimePlatform())
		binary := filepath.Join(directory, "bin", name)
		info, err := os.Stat(binary)
		if err != nil || !info.Mode().IsRegular() || !isExecutable(info) {
			continue
		}
		// 第一個已安裝候選即為選定成品；壞掉時不可偷偷改用另一份舊版本。
		result.Binary = binary
		file, err := os.Open(filepath.Join(directory, "runtime.json"))
		if err != nil {
			result.Reason = "AMD Runtime 缺少來源與版本資訊"
			return result
		}
		var manifest runtimeManifest
		decoder := json.NewDecoder(io.LimitReader(file, 64*1024))
		err = decoder.Decode(&manifest)
		_ = file.Close()
		if err != nil || manifest.Schema != 2 || manifest.Source != "halo-box/strix-llama.cpp" ||
			manifest.Platform != runtimePlatform() || manifest.Backend != "vulkan" || !runtimeCommitPattern.MatchString(manifest.Commit) {
			result.Reason = "AMD Runtime 的來源、版本、平台或後端資訊不相容"
			return result
		}
		if !containsMode(manifest.Modes, "vulkan") || !containsMode(manifest.Modes, "strix-halo") {
			result.Reason = "AMD Runtime 必須同時包含 Vulkan 與 Strix Halo 互斥模式"
			return result
		}
		result.Source, result.Commit = manifest.Source, manifest.Commit
		help, err := probeRuntime(binary, "--help")
		if err != nil {
			result.Reason = "AMD Runtime 無法執行，請檢查系統相依套件"
			return result
		}
		for _, flag := range []string{"--openloader-access-control", "--load-mode", "--parallel", "--device", "--list-devices"} {
			if !strings.Contains(help, flag) {
				result.Reason = "AMD Runtime 缺少必要介面：" + flag
				return result
			}
		}
		devices, err := probeRuntime(binary, "--list-devices")
		if err != nil {
			result.Reason = "AMD Runtime 無法列舉 Vulkan 裝置，請檢查顯示卡驅動"
			return result
		}
		for _, match := range amdVulkanDevicePattern.FindAllStringSubmatch(devices, -1) {
			isStrix := strixHaloDevicePattern.MatchString(match[2])
			isAMD := amdDeviceNamePattern.MatchString(match[2]) || isStrix
			if isAMD && (mode != "strix-halo" || isStrix) {
				result.Device = match[1]
				result.DeviceName = match[2]
				result.Mode = "vulkan"
				if isStrix && mode != "vulkan" {
					result.Mode = "strix-halo"
					result.Label = "LLaMA Server（Strix Halo）"
				}
				break
			}
		}
		if result.Device == "" {
			result.Reason = "未偵測到符合所選模式的 AMD Vulkan GPU"
			return result
		}
		file, err = os.Open(binary)
		if err != nil {
			result.Reason = "無法讀取 AMD Runtime 執行檔"
			return result
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		_ = file.Close()
		if err != nil {
			result.Reason = "無法驗證 AMD Runtime 執行檔"
			return result
		}
		result.Fingerprint = manifest.Source + ":" + manifest.Commit + ":vulkan:" + result.Device + ":" + result.Mode + ":" + hex.EncodeToString(hash.Sum(nil))
		result.Available, result.Reason = true, ""
		return result
	}
	return result
}

func containsMode(modes []string, mode string) bool {
	for _, candidate := range modes {
		if candidate == mode {
			return true
		}
	}
	return false
}

// AMDModeFromArguments 是管理層參數，不傳給 llama-server；同時指定兩個模式視為錯誤。
func AMDModeFromArguments(arguments []string) (string, error) {
	mode, found := "auto", false
	for index := 0; index < len(arguments); index++ {
		name, value, inline := strings.Cut(arguments[index], "=")
		if name != "--tanpopo-amd-mode" {
			continue
		}
		if found {
			return "", errors.New("AMD 模式只能指定一次")
		}
		found = true
		if !inline {
			index++
			if index >= len(arguments) {
				return "", errors.New("AMD 模式缺少值")
			}
			value = arguments[index]
		}
		if value != "auto" && value != "vulkan" && value != "strix-halo" {
			return "", errors.New("AMD 模式僅支援 auto、vulkan 或 strix-halo")
		}
		mode = value
	}
	return mode, nil
}

// Strix 模式須移除變數，而不是設定為 0：上游用 getenv 是否存在判斷。
func amdRuntimeEnvironment(mode string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if !strings.EqualFold(name, "GGML_VK_MMV_NO_SPLIT") {
			environment = append(environment, item)
		}
	}
	if mode == "vulkan" {
		environment = append(environment, "GGML_VK_MMV_NO_SPLIT=1")
	}
	return environment
}

// 有上限的輸出收集器，避免錯誤執行檔無限輸出耗盡記憶體。
type runtimeProbeOutput struct{ data []byte }

func (w *runtimeProbeOutput) Write(data []byte) (int, error) {
	count := len(data)
	remaining := 1024*1024 - len(w.data)
	if remaining > 0 {
		w.data = append(w.data, data[:min(remaining, count)]...)
	}
	return count, nil
}

func probeRuntime(binary, argument string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, argument)
	command.WaitDelay = time.Second
	output := &runtimeProbeOutput{}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	return string(output.data), err
}

// ValidateRuntimeVariantConfiguration 檢查設定格式，不依賴當下硬體可用性。
func ValidateRuntimeVariantConfiguration(command domain.StartupCommand) error {
	_, err := AMDModeFromArguments(command.ExtraArgs)
	if err != nil {
		return err
	}
	if command.RuntimeVariant == "" {
		for _, argument := range command.ExtraArgs {
			name, _, _ := strings.Cut(argument, "=")
			if name == "--tanpopo-amd-mode" {
				return errors.New("AMD 模式參數僅供 AMD Runtime 使用")
			}
		}
		return nil
	}
	if command.Runtime != domain.RuntimeLlamaServer || !domain.IsAMDRuntimeVariant(command.RuntimeVariant) {
		return errors.New("不支援的 Runtime 版本")
	}
	return nil
}

func ValidateRuntimeVariant(command domain.StartupCommand) error {
	if err := ValidateRuntimeVariantConfiguration(command); err != nil {
		return err
	}
	if command.RuntimeVariant == "" {
		return nil
	}
	mode, _ := AMDModeFromArguments(command.ExtraArgs)
	for _, capability := range RuntimeCapabilities() {
		if capability.Variant == command.RuntimeVariant {
			if !capability.Available {
				return errors.New(capability.Reason)
			}
			if mode == "strix-halo" && capability.Mode != "strix-halo" {
				// 明確指定模式時重新列舉，允許混合 GPU 主機選到非第一張的 Strix 裝置。
				selected := detectAMDRuntime(mode)
				if !selected.Available {
					return errors.New(selected.Reason)
				}
			}
			return nil
		}
	}
	return errors.New("找不到指定的 Runtime 版本")
}

func RuntimeVariantFingerprint(variant string) string {
	if variant == "" {
		return ""
	}
	for _, capability := range RuntimeCapabilities() {
		if capability.Variant == variant && capability.Available {
			return capability.Fingerprint
		}
	}
	return "unavailable:" + variant
}

func withoutNamedValueArguments(arguments []string, names ...string) []string {
	result := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		name, _, inline := strings.Cut(arguments[index], "=")
		managed := false
		for _, candidate := range names {
			if name == candidate {
				managed = true
				break
			}
		}
		if managed {
			if !inline && index+1 < len(arguments) {
				index++
			}
			continue
		}
		result = append(result, arguments[index])
	}
	return result
}
