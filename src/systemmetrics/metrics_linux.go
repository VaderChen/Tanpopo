//go:build linux

package systemmetrics

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func collectPlatform(ctx context.Context) Snapshot {
	return Snapshot{
		CPU:    collectLinuxCPU(ctx),
		GPU:    collectLinuxGPU(ctx),
		Memory: collectLinuxMemory(),
	}
}

func collectPlatformInfo(ctx context.Context) SystemInfo {
	hostname, _ := os.Hostname()
	osName, osVersion := readLinuxOSRelease()
	cpuModel, physicalCores := readLinuxCPUInfo()
	totalMemory := readLinuxTotalMemory()
	return SystemInfo{
		OSName:        osName,
		OSVersion:     osVersion,
		KernelVersion: readTrimmedFile("/proc/sys/kernel/osrelease"),
		Architecture:  runtime.GOARCH,
		Hostname:      strings.TrimSpace(hostname),
		CPUModel:      cpuModel,
		PhysicalCores: physicalCores,
		LogicalCores:  runtime.NumCPU(),
		GPUModel:      collectLinuxGPUModel(ctx),
		MemoryBytes:   totalMemory,
	}
}

func readLinuxOSRelease() (string, string) {
	content, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "Linux", ""
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, unquoteErr := strconv.Unquote(value); unquoteErr == nil {
			value = unquoted
		}
		values[key] = value
	}
	name := values["NAME"]
	if name == "" {
		name = "Linux"
	}
	return name, values["VERSION"]
}

func readLinuxCPUInfo() (string, int) {
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "", 0
	}
	model := ""
	physicalIDs := make(map[string]struct{})
	physicalID := ""
	coreID := ""
	commitCore := func() {
		if physicalID != "" && coreID != "" {
			physicalIDs[physicalID+":"+coreID] = struct{}{}
		}
		physicalID, coreID = "", ""
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "" {
			commitCore()
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name", "Hardware":
			if model == "" {
				model = strings.TrimSpace(value)
			}
		case "physical id":
			physicalID = strings.TrimSpace(value)
		case "core id":
			coreID = strings.TrimSpace(value)
		}
	}
	commitCore()
	cores := len(physicalIDs)
	if cores == 0 {
		cores = runtime.NumCPU()
	}
	return model, cores
}

func readLinuxTotalMemory() uint64 {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			value, _ := strconv.ParseUint(fields[1], 10, 64)
			return value * 1024
		}
	}
	return 0
}

func readTrimmedFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

func collectLinuxGPUModel(ctx context.Context) string {
	commandContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(commandContext, "nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output(); err == nil {
		return strings.Join(strings.FieldsFunc(strings.TrimSpace(string(output)), func(value rune) bool { return value == '\n' || value == '\r' }), ", ")
	}
	commandContext, cancel = context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandContext, "lspci").Output()
	if err != nil {
		return ""
	}
	var devices []string
	for _, line := range strings.Split(string(output), "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "vga compatible controller") || strings.Contains(lower, "3d controller") {
			devices = append(devices, strings.TrimSpace(line))
		}
	}
	return strings.Join(devices, ", ")
}

func collectLinuxCPU(ctx context.Context) Metric {
	first, ok := readLinuxCPUCounters()
	if !ok {
		return Metric{}
	}
	select {
	case <-ctx.Done():
		return Metric{}
	case <-time.After(125 * time.Millisecond):
	}
	second, ok := readLinuxCPUCounters()
	if !ok || second.total <= first.total || second.idle < first.idle {
		return Metric{}
	}
	totalDelta := second.total - first.total
	idleDelta := second.idle - first.idle
	return Metric{Percent: float64(totalDelta-idleDelta) / float64(totalDelta) * 100, Available: true}
}

type cpuCounters struct {
	total uint64
	idle  uint64
}

func readLinuxCPUCounters() (cpuCounters, bool) {
	content, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuCounters{}, false
	}
	fields := strings.Fields(strings.SplitN(string(content), "\n", 2)[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuCounters{}, false
	}
	var values []uint64
	for _, field := range fields[1:] {
		value, parseErr := strconv.ParseUint(field, 10, 64)
		if parseErr != nil {
			return cpuCounters{}, false
		}
		values = append(values, value)
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return cpuCounters{total: total, idle: idle}, true
}

func collectLinuxMemory() Metric {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Metric{}
	}
	values := make(map[string]float64)
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.ParseFloat(fields[1], 64)
		if parseErr == nil {
			values[strings.TrimSuffix(fields[0], ":")] = value
		}
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total <= 0 || available < 0 {
		return Metric{}
	}
	return Metric{Percent: (total - available) / total * 100, Available: true}
}

func collectLinuxGPU(ctx context.Context) Metric {
	if metric := collectNVIDIAGPU(ctx); metric.Available {
		return metric
	}
	if metric := collectAMDGPU(ctx); metric.Available {
		return metric
	}
	return collectLinuxDRMGPU()
}

func collectNVIDIAGPU(ctx context.Context) Metric {
	commandContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandContext, "nvidia-smi", "--query-gpu=utilization.gpu,name", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return Metric{}
	}
	var sum float64
	var count int
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.SplitN(line, ",", 2)
		percent, parseErr := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
		if parseErr != nil {
			continue
		}
		sum += percent
		count++
		if len(fields) == 2 {
			names = append(names, strings.TrimSpace(fields[1]))
		}
	}
	if count == 0 {
		return Metric{}
	}
	return Metric{Percent: sum / float64(count), Available: true, Device: strings.Join(names, ", ")}
}

func collectAMDGPU(ctx context.Context) Metric {
	commandContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandContext, "rocm-smi", "--showuse", "--json").Output()
	if err != nil {
		return Metric{}
	}
	var payload map[string]map[string]any
	if json.Unmarshal(output, &payload) != nil {
		return Metric{}
	}
	var sum float64
	var count int
	var names []string
	for name, fields := range payload {
		for key, raw := range fields {
			if !strings.Contains(strings.ToLower(key), "gpu use") {
				continue
			}
			value := strings.TrimSuffix(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(toString(raw)), "%", "")), "%")
			percent, parseErr := strconv.ParseFloat(value, 64)
			if parseErr == nil {
				sum += percent
				count++
				names = append(names, name)
			}
		}
	}
	if count == 0 {
		return Metric{}
	}
	return Metric{Percent: sum / float64(count), Available: true, Device: strings.Join(names, ", ")}
}

// collectLinuxDRMGPU 使用核心 DRM 驅動公開的 sysfs 使用率作為後備來源。
// 這可涵蓋未安裝 rocm-smi 的 AMD GPU，也適用於其他提供
// gpu_busy_percent 的 Linux DRM 驅動。
func collectLinuxDRMGPU() Metric {
	paths, err := filepath.Glob("/sys/class/drm/card*/device/gpu_busy_percent")
	if err != nil {
		return Metric{}
	}

	var sum float64
	var count int
	devices := make([]string, 0, len(paths))
	for _, path := range paths {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		percent, parseErr := strconv.ParseFloat(strings.TrimSpace(string(content)), 64)
		if parseErr != nil {
			continue
		}
		sum += percent
		count++
		devices = append(devices, filepath.Base(filepath.Dir(filepath.Dir(path))))
	}
	if count == 0 {
		return Metric{}
	}
	return Metric{Percent: sum / float64(count), Available: true, Device: strings.Join(devices, ", ")}
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}
