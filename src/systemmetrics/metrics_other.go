//go:build !darwin && !linux

package systemmetrics

import (
	"context"
	"os"
	"runtime"
	"strings"
)

func collectPlatform(_ context.Context) Snapshot {
	return Snapshot{}
}

func collectPlatformInfo(_ context.Context) SystemInfo {
	hostname, _ := os.Hostname()
	return SystemInfo{
		OSName:       runtime.GOOS,
		Architecture: runtime.GOARCH,
		Hostname:     strings.TrimSpace(hostname),
		LogicalCores: runtime.NumCPU(),
	}
}
