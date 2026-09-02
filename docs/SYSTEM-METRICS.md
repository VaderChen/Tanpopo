# 系統記憶體指標

底部狀態列與 `/api/system/metrics` 顯示整台主機的 RAM 使用率，不是 Tanpopo、模型程序或 GPU 單獨使用的記憶體。後端每 3 秒集中更新快照，所有管理介面讀取同一份資料；百分比顯示到小數點後一位。

## macOS 計算方式

總實體記憶體取自 `sysctl hw.memsize`，頁面大小與分類取自 `vm_stat`，依系統實際頁面大小換算，不固定假設 4 KiB 或 16 KiB。

```text
已使用頁面 = active + inactive + speculative + wired
           + occupied-by-compressor - purgeable - file-backed
RAM 使用率 = 已使用頁面 × 頁面大小 ÷ 總實體記憶體 × 100%
```

- 排除檔案快取與可清除頁面，不再以「總量減 free 與 speculative」當成畫面的已使用 RAM。
- 保留 wired 與壓縮器實際佔用的實體記憶體。壓縮量使用 `Pages occupied by compressor`，不是壓縮前的 `Pages stored in compressor`；Swap 也不加到實體 RAM 使用率。
- 所有必要欄位必須存在且能正確解析；重複欄位、無效頁面大小或超出合理範圍的總量都視為採集失敗，顯示 `N/A`，不以缺漏的零值或舊公式冒充有效結果。

此公式與 [Stats 3.0.14 的 RAM 計算](https://github.com/exelban/stats/blob/v3.0.14/Modules/RAM/readers.swift)一致。不同工具的採樣時間與四捨五入仍可能造成小幅差距；檔案快取並非沒有佔用實體 RAM，只是不納入此「已使用」口徑。

## Linux 與資料介面

Linux 維持 `/proc/meminfo` 的 `(MemTotal - MemAvailable) / MemTotal` 算法，不套用 macOS 頁面分類。既有 API 的 `memory.percent` 與 `memory.available` 結構不變；`available` 表示讀值是否有效，不是可用 RAM 的百分比。

## 不同保護機制不可混用

- **狀態列顯示**：50% 與 80% 是顏色區間，不是 macOS 的記憶體壓力等級，也不代表還能安全載入同等比例的模型。
- **啟動前的記憶體壓力保護**：改用獨立的可用位元組讀值，不從顯示百分比反推。macOS 保留原有的 `free + speculative` 保守口徑；Linux 使用 `MemAvailable`。系統保留量與配置調整規則不因顯示修正而放寬。
- **MLX 推論期間的資源檢查**：仍依模型、上下文、裝置與並行請求預算判斷，不使用狀態列百分比來決定容量。詳見 [MLX Runtime 規格](MLX-RUNTIME-SPEC.md)。

更新管理服務執行檔後，需重新啟動 Tanpopo 才會使用新的統計方式；只重新整理網頁或重新載入模型不會更新既有管理服務程序。
