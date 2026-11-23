package llamacpp

import (
	"fmt"
	"strconv"
	"strings"

	"LlamaLoader/src/domain"
)

// 只在明確啟用時改變記憶體估算；未知／無效參數不能繞過保護。
func diskOffloadMode(command domain.StartupCommand) (enabled bool, cacheMB int, err error) {
	tileMB := 32
	for i := 0; i < len(command.ExtraArgs); i++ {
		arg := strings.TrimSpace(command.ExtraArgs[i])
		if arg == "--tanpopo-disk-offload" {
			if command.Runtime != domain.RuntimeLlamaServer {
				return false, 0, fmt.Errorf("MLX 磁碟模式請使用 --disk-cache-mb")
			}
			enabled = true
			continue
		}
		if strings.HasPrefix(arg, "--tanpopo-disk-offload=") {
			return false, 0, fmt.Errorf("--tanpopo-disk-offload 不接受數值")
		}
		name := strings.SplitN(arg, "=", 2)[0]
		if name != "--disk-cache-mb" && name != "--disk-tile-mb" {
			continue
		}
		if command.Runtime != domain.RuntimeMLXServer || arg != name {
			return false, 0, fmt.Errorf("%s 僅用於 MLX，請將旗標與整數值分列輸入", name)
		}
		i++
		if i >= len(command.ExtraArgs) {
			return false, 0, fmt.Errorf("%s 缺少整數值", name)
		}
		value, parseErr := strconv.Atoi(command.ExtraArgs[i])
		if parseErr != nil || value < 0 || value > 131072 || (name == "--disk-tile-mb" && (value < 1 || value > 512)) {
			return false, 0, fmt.Errorf("%s 數值無效", name)
		}
		if name == "--disk-cache-mb" {
			cacheMB = value
		} else {
			tileMB = value
		}
	}
	if command.Runtime == domain.RuntimeMLXServer {
		enabled = cacheMB > 0
		if enabled && cacheMB < 2*tileMB {
			return false, 0, fmt.Errorf("磁碟快取至少需容納兩個區塊")
		}
	}
	if enabled && hasAnyArgument(command.ExtraArgs, "--distributed-config", "--distributed-config-base64") {
		return false, 0, fmt.Errorf("磁碟輔助模式目前不能與 TCP Ring／RDMA 同時啟用")
	}
	return enabled, cacheMB, nil
}

func withLlamaDiskOffloadArguments(arguments []string) []string {
	args := withoutNamedValueArguments(arguments, "--tensor-read-lazy")
	filtered := make([]string, 0, len(args)+3)
	for _, arg := range args {
		if arg != "--tanpopo-disk-offload" && arg != "--no-warmup" {
			filtered = append(filtered, arg)
		}
	}
	return append(filtered, "--tensor-read-lazy", "on", "--no-warmup")
}
