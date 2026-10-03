package llamacpp

import "strings"

// 原生 Runtime 的致命錯誤出現在用法說明之前；只讀目前這次啟動的日誌，
// 避免把前一次失敗誤套到這次程序退出。
func runtimeFailure(exitError error, logs string) string {
	if start := strings.LastIndex(logs, "\n$ "); start >= 0 {
		logs = logs[start:]
	}
	lines := strings.Split(logs, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		message, ok := strings.CutPrefix(lines[i], "mlx-server error: ")
		if !ok {
			message, ok = strings.CutPrefix(lines[i], "分散式群組失敗：")
		}
		if ok && strings.TrimSpace(message) != "" {
			if len(message) > 768 {
				message = strings.ToValidUTF8(message[:768], "") + "…"
			}
			return strings.TrimSpace(message) + "（" + exitError.Error() + "）"
		}
	}
	return exitError.Error()
}
