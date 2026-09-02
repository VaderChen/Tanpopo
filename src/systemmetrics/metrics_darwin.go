//go:build darwin

package systemmetrics

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	darwinCPUUsagePattern = regexp.MustCompile(`(?m)^CPU usage:\s*([\d.]+)% user,\s*([\d.]+)% sys,\s*([\d.]+)% idle`)
	darwinGPUUsagePattern = regexp.MustCompile(`"Device Utilization %"\s*=\s*([\d.]+)`)
	darwinGPUModelPattern = regexp.MustCompile(`"model"\s*=\s*"([^"]+)"`)
	vmPageSizePattern     = regexp.MustCompile(`page size of\s+(\d+)\s+bytes`)
)

func collectPlatform(ctx context.Context) Snapshot {
	snapshot := Snapshot{
		CPU: collectDarwinCPU(ctx),
		GPU: collectDarwinGPU(ctx),
	}
	snapshot.Memory, snapshot.MemoryAvailableBytes = collectDarwinMemory(ctx)
	return snapshot
}

func collectPlatformInfo(ctx context.Context) SystemInfo {
	hostname, _ := os.Hostname()
	totalMemory, _ := strconv.ParseUint(darwinSysctl(ctx, "hw.memsize"), 10, 64)
	physicalCores, _ := strconv.Atoi(darwinSysctl(ctx, "hw.physicalcpu"))
	logicalCores, _ := strconv.Atoi(darwinSysctl(ctx, "hw.logicalcpu"))
	cpuModel := darwinSysctl(ctx, "machdep.cpu.brand_string")
	if cpuModel == "" {
		cpuModel = darwinSysctl(ctx, "hw.model")
	}
	gpuModel := ""
	if output, err := runCommand(ctx, "/usr/sbin/ioreg", "-r", "-c", "AGXAccelerator", "-d", "1"); err == nil {
		if matches := darwinGPUModelPattern.FindStringSubmatch(string(output)); len(matches) == 2 {
			gpuModel = strings.TrimSpace(matches[1])
		}
	}
	return SystemInfo{
		OSName:        "macOS",
		OSVersion:     commandString(ctx, "/usr/bin/sw_vers", "-productVersion"),
		OSBuild:       commandString(ctx, "/usr/bin/sw_vers", "-buildVersion"),
		KernelVersion: commandString(ctx, "/usr/bin/uname", "-r"),
		Architecture:  runtime.GOARCH,
		Hostname:      strings.TrimSpace(hostname),
		CPUModel:      cpuModel,
		PhysicalCores: physicalCores,
		LogicalCores:  logicalCores,
		GPUModel:      gpuModel,
		MemoryBytes:   totalMemory,
	}
}

func darwinSysctl(ctx context.Context, name string) string {
	return commandString(ctx, "/usr/sbin/sysctl", "-n", name)
}

func commandString(ctx context.Context, name string, args ...string) string {
	output, err := runCommand(ctx, name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func collectDarwinCPU(ctx context.Context) Metric {
	output, err := runCommand(ctx, "/usr/bin/top", "-l", "1", "-n", "0")
	if err != nil {
		return Metric{}
	}
	matches := darwinCPUUsagePattern.FindStringSubmatch(string(output))
	if len(matches) != 4 {
		return Metric{}
	}
	idle, err := strconv.ParseFloat(matches[3], 64)
	if err != nil {
		return Metric{}
	}
	return Metric{Percent: 100 - idle, Available: true}
}

func collectDarwinGPU(ctx context.Context) Metric {
	output, err := runCommand(ctx, "/usr/sbin/ioreg", "-r", "-c", "AGXAccelerator", "-d", "1")
	if err != nil {
		return Metric{}
	}
	matches := darwinGPUUsagePattern.FindStringSubmatch(string(output))
	if len(matches) != 2 {
		return Metric{}
	}
	percent, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return Metric{}
	}
	device := "Apple GPU"
	if model := darwinGPUModelPattern.FindStringSubmatch(string(output)); len(model) == 2 {
		device = strings.TrimSpace(model[1])
	}
	return Metric{Percent: percent, Available: true, Device: device}
}

func collectDarwinMemory(ctx context.Context) (Metric, uint64) {
	totalOutput, err := runCommand(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize")
	if err != nil {
		return Metric{}, 0
	}
	total, err := strconv.ParseUint(strings.TrimSpace(string(totalOutput)), 10, 64)
	if err != nil || total == 0 {
		return Metric{}, 0
	}
	vmOutput, err := runCommand(ctx, "/usr/bin/vm_stat")
	if err != nil {
		return Metric{}, 0
	}
	stats, ok := parseVMStat(string(vmOutput))
	if !ok {
		return Metric{}, 0
	}
	// 與 Stats 的實體 RAM 使用口徑一致：扣除檔案快取與可清除頁面，
	// 保留 wired 與壓縮器實際佔用量；不可使用壓縮前的邏輯頁數。
	// 先轉浮點再加減，避免無號整數溢位或相減下溢。
	usedPages := float64(stats.active) + float64(stats.inactive) +
		float64(stats.speculative) + float64(stats.wired) + float64(stats.compressed) -
		float64(stats.purgeable) - float64(stats.fileBacked)
	used := usedPages * float64(stats.pageSize)
	// 啟動前保護繼續只採用 free + speculative，不因顯示公式改變而放寬預算。
	available := (float64(stats.free) + float64(stats.speculative)) * float64(stats.pageSize)
	if used < 0 || used > float64(total) || available > float64(total) {
		return Metric{}, 0
	}
	return Metric{Percent: used / float64(total) * 100, Available: true}, uint64(available)
}

type darwinVMStats struct {
	pageSize    uint64
	free        uint64
	active      uint64
	inactive    uint64
	speculative uint64
	wired       uint64
	compressed  uint64
	purgeable   uint64
	fileBacked  uint64
}

func parseVMStat(output string) (darwinVMStats, bool) {
	pageMatch := vmPageSizePattern.FindStringSubmatch(output)
	if len(pageMatch) != 2 {
		return darwinVMStats{}, false
	}
	pageSize, err := strconv.ParseUint(pageMatch[1], 10, 64)
	if err != nil || pageSize == 0 {
		return darwinVMStats{}, false
	}
	stats := darwinVMStats{pageSize: pageSize}
	values := map[string]*uint64{
		"Pages free":                   &stats.free,
		"Pages active":                 &stats.active,
		"Pages inactive":               &stats.inactive,
		"Pages speculative":            &stats.speculative,
		"Pages wired down":             &stats.wired,
		"Pages occupied by compressor": &stats.compressed,
		"Pages purgeable":              &stats.purgeable,
		"File-backed pages":            &stats.fileBacked,
	}
	seen := make(map[string]bool, len(values))
	for _, line := range strings.Split(output, "\n") {
		name, value, found := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		target, tracked := values[name]
		if !found || !tracked {
			continue
		}
		number := strings.TrimSuffix(strings.TrimSpace(value), ".")
		parsed, parseErr := strconv.ParseUint(number, 10, 64)
		if parseErr != nil || seen[name] {
			return darwinVMStats{}, false
		}
		*target = parsed
		seen[name] = true
	}
	if len(seen) != len(values) {
		return darwinVMStats{}, false
	}
	return stats, true
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	commandContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(commandContext, name, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("執行 %s 失敗: %w", name, err)
	}
	return output, nil
}
