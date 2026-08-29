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
	return Snapshot{
		CPU:    collectDarwinCPU(ctx),
		GPU:    collectDarwinGPU(ctx),
		Memory: collectDarwinMemory(ctx),
	}
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

func collectDarwinMemory(ctx context.Context) Metric {
	totalOutput, err := runCommand(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize")
	if err != nil {
		return Metric{}
	}
	total, err := strconv.ParseFloat(strings.TrimSpace(string(totalOutput)), 64)
	if err != nil || total <= 0 {
		return Metric{}
	}
	vmOutput, err := runCommand(ctx, "/usr/bin/vm_stat")
	if err != nil {
		return Metric{}
	}
	pageSize, freePages, speculativePages, ok := parseVMStat(string(vmOutput))
	if !ok {
		return Metric{}
	}
	available := float64(freePages+speculativePages) * float64(pageSize)
	return Metric{Percent: (total - available) / total * 100, Available: true}
}

func parseVMStat(output string) (pageSize, freePages, speculativePages uint64, ok bool) {
	pageMatch := vmPageSizePattern.FindStringSubmatch(output)
	if len(pageMatch) != 2 {
		return 0, 0, 0, false
	}
	pageSize, err := strconv.ParseUint(pageMatch[1], 10, 64)
	if err != nil || pageSize == 0 {
		return 0, 0, 0, false
	}
	values := map[string]*uint64{
		"Pages free":        &freePages,
		"Pages speculative": &speculativePages,
	}
	for _, line := range strings.Split(output, "\n") {
		name, value, found := strings.Cut(line, ":")
		target, tracked := values[strings.TrimSpace(name)]
		if !found || !tracked {
			continue
		}
		number := strings.TrimSuffix(strings.TrimSpace(value), ".")
		parsed, parseErr := strconv.ParseUint(number, 10, 64)
		if parseErr != nil {
			return 0, 0, 0, false
		}
		*target = parsed
	}
	return pageSize, freePages, speculativePages, true
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
