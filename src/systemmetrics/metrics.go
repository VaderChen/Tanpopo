package systemmetrics

import (
	"context"
	"math"
	"net"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Metric 是單一系統資源的即時使用率。無法可靠偵測時 Available 會是 false。
type Metric struct {
	Percent   float64 `json:"percent"`
	Available bool    `json:"available"`
	Device    string  `json:"device,omitempty"`
}

// Snapshot 是同一次採集取得的 CPU、GPU 與記憶體狀態。
type Snapshot struct {
	CPU       Metric    `json:"cpu"`
	GPU       Metric    `json:"gpu"`
	Memory    Metric    `json:"memory"`
	UpdatedAt time.Time `json:"updated_at"`

	// MemoryAvailableBytes 保留平台原有的啟動前保護口徑，不從顯示用百分比反推。
	MemoryAvailableBytes uint64 `json:"-"`
}

// NetworkInterface 是單一非 loopback 網路介面的公開狀態摘要。
type NetworkInterface struct {
	Name            string   `json:"name"`
	Up              bool     `json:"up"`
	MTU             int      `json:"mtu"`
	HardwareAddress string   `json:"hardware_address,omitempty"`
	Addresses       []string `json:"addresses"`
}

// SystemInfo 是不含敏感憑證的本機硬體與作業系統摘要。
type SystemInfo struct {
	Platform       string             `json:"platform"`
	OSName         string             `json:"os_name"`
	OSVersion      string             `json:"os_version"`
	OSBuild        string             `json:"os_build"`
	KernelVersion  string             `json:"kernel_version"`
	Architecture   string             `json:"architecture"`
	Hostname       string             `json:"hostname"`
	CPUModel       string             `json:"cpu_model"`
	PhysicalCores  int                `json:"physical_cores"`
	LogicalCores   int                `json:"logical_cores"`
	GPUModel       string             `json:"gpu_model"`
	MemoryBytes    uint64             `json:"memory_bytes"`
	Network        []NetworkInterface `json:"network_interfaces"`
	ManagementURLs []string           `json:"management_urls,omitempty"`
	CollectedAt    time.Time          `json:"collected_at"`
}

// Collector 在背景集中採集系統狀態，所有瀏覽器只讀取同一份快照。
type Collector struct {
	mu       sync.RWMutex
	snapshot Snapshot
	info     SystemInfo
}

func NewCollector() *Collector {
	return &Collector{}
}

// Start 立即開始採集，之後依 interval 更新，並在 ctx 結束時停止。
func (c *Collector) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	go func() {
		c.collectInfo(ctx)
		c.collect(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.collect(ctx)
			}
		}
	}()
}

func (c *Collector) Info() SystemInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.info
}

func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

func (c *Collector) collect(ctx context.Context) {
	snapshot := collectPlatform(ctx)
	snapshot.CPU = normalizeMetric(snapshot.CPU)
	snapshot.GPU = normalizeMetric(snapshot.GPU)
	snapshot.Memory = normalizeMetric(snapshot.Memory)
	snapshot.UpdatedAt = time.Now()
	network := collectNetworkInterfaces()
	c.mu.Lock()
	c.snapshot = snapshot
	c.info.Network = network
	c.mu.Unlock()
}

func (c *Collector) collectInfo(ctx context.Context) {
	info := collectPlatformInfo(ctx)
	info.Platform = runtime.GOOS
	info.Network = collectNetworkInterfaces()
	info.CollectedAt = time.Now()
	c.mu.Lock()
	c.info = info
	c.mu.Unlock()
}

func collectNetworkInterfaces() []NetworkInterface {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := make([]NetworkInterface, 0, len(interfaces))
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := networkInterface.Addrs()
		formattedAddresses := make([]string, 0, len(addresses))
		for _, address := range addresses {
			value := strings.TrimSpace(address.String())
			if value != "" {
				formattedAddresses = append(formattedAddresses, value)
			}
		}
		sort.Strings(formattedAddresses)
		up := networkInterface.Flags&net.FlagUp != 0
		hardwareAddress := strings.TrimSpace(networkInterface.HardwareAddr.String())
		if !up && hardwareAddress == "" && len(formattedAddresses) == 0 {
			continue
		}
		result = append(result, NetworkInterface{
			Name:            networkInterface.Name,
			Up:              up,
			MTU:             networkInterface.MTU,
			HardwareAddress: hardwareAddress,
			Addresses:       formattedAddresses,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		leftActive := result[left].Up && len(result[left].Addresses) > 0
		rightActive := result[right].Up && len(result[right].Addresses) > 0
		if leftActive != rightActive {
			return leftActive
		}
		if result[left].Up != result[right].Up {
			return result[left].Up
		}
		return strings.ToLower(result[left].Name) < strings.ToLower(result[right].Name)
	})
	return result
}

func normalizeMetric(metric Metric) Metric {
	if !metric.Available || math.IsNaN(metric.Percent) || math.IsInf(metric.Percent, 0) {
		metric.Percent = 0
		metric.Available = false
		return metric
	}
	metric.Percent = math.Round(max(0, min(100, metric.Percent))*10) / 10
	return metric
}
