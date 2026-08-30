# MLX MMap 內部實作說明

## 狀態

MLX MMap 已整合至 Tanpopo 管理介面。執行狀態頁的進階設定控制本次啟動是否使用 MMap，啟動 Profile 則提供記憶體保留目標。

## 目標

- 讓 MLX safetensors 權重直接使用檔案映射頁面，避免載入時先複製完整權重。
- 在系統有記憶體壓力時，允許 macOS 回收未修改的模型檔案頁面，需要時再從檔案載入。
- 壓低 MLX 中間緩衝區快取，並為生成流程套用一致的 Wired Memory 策略。
- 讓一般模型、視覺模型與 DFlash Draft 共用同一套載入流程。

## 目前行為

啟用內部 MMap 模式時，mlx-server 會：

1. 逐一解析 safetensors 的 tensor 區段。
2. 依虛擬記憶體頁面邊界映射檔案，並以零拷貝 MLXArray 包裝。
3. 將「實體記憶體減去保留目標」設為 MLX 配置目標。
4. 將 MLX 快取限制在最多 64 MiB，載入前後主動清理未使用快取。
5. 在生成期間套用 Wired Memory Ticket，降低工作緩衝區無限制成長的機會。Wired limit 會先夾到 Metal 的 `recommendedMaxWorkingSetSize`；超過該值時 MLX 會直接觸發致命錯誤而中止生成。

保留目標不是 Runtime 的硬性記憶體上限。自動模式會保留實體記憶體的 10%，且至少保留 2 GiB；內部亦接受 4、8、16、24、32、48、64、96 或 128 GiB。

## 記憶體量測與執行期守門

映射權重的頁面**不會**計入行程的 `phys_footprint`，`vmmap` 也幾乎看不到（mapped file resident ≈ 0），它們實際存在系統的檔案快取裡。因此只看行程層級的數字會嚴重低估實際用量，必須用 `MemoryMappedRegionRegistry.residentBytes()`（`mincore`）另外加總：

```
真實用量 = phys_footprint + 映射權重常駐量
```

`MLXMemoryMapPlan.startMemoryGuard()` 每 2 秒比對這個總量與預算，超出時清掉 MLX 配置器快取並輸出 `MLX MMap over_limit …`。

**使用者空間無法強制逐出映射權重的頁面。** 實測 `madvise(MADV_DONTNEED)`、`madvise(MADV_FREE_REUSABLE)`（EPERM）、`msync(MS_INVALIDATE)` 與 `MAP_FIXED` 重新映射都不會讓 `mincore` 的常駐量下降；`MAP_FIXED` 只會清掉行程自己的 `external` 計數，檔案快取仍在。這些是乾淨頁面，回收時機由核心的記憶體壓力決定。

所以保留目標要更接近硬上限，只能靠准入控制（載入前估算權重 + KV，超出就拒絕）或自動收斂 KV，光靠執行期回收做不到。另外在 64 GiB 機器上保留值最大只能選到 48 GiB（預算 16 GiB），無法設出更緊的預算。

## 對齊限制

Metal 綁定緩衝區時使用「基底 MTLBuffer + 位元組位移」，位移必須同時滿足元素型別對齊與向量化載入的 16 位元組對齊。safetensors 的資料區起點是 `8 + header 長度`，實務上是任意位元組位置，且 mmap 只能對齊到 VM 頁面，無法事後修正位元組級偏移。

未對齊時映射出來的權重不會報錯，但 Metal 會讀到錯誤位址，生成結果會變成無意義字元。因此 `MemoryMappedTensorArray` 會先以 `canMap(fileOffset:dtype:)` 檢查對齊，不符合的 tensor 改走 `pread` 對齊複製，語意等同 eager 載入。載入時會輸出 `MLX MMap file=… mapped=… copied=… copied_mib=…` 供判讀實際映射比例。

抽樣本機模型的資料區起點（mod 16）：Qwen3.5-4B-MLX-4bit 為 7、Qwen3.5-4B-DFlash 為 9、ornith-1.5-9B-MLX-4bit 為 11、gemma-3-12b-it-4bit 兩個分片為 1 與 15，Qwen3.8-27B-MLX-4bit 三個分片中只有第一個為 0。也就是說多數既有模型會整份走複製路徑，MMap 的節省幅度取決於模型檔案本身是否對齊；把 header 補成 16（或頁面）對齊後重新封裝即可完整映射。

## 格式相容性

- MLX safetensors：原生支援且資料區對齊的 tensor 會使用檔案映射。
- GGUF：未經轉換且可直接建立 MLXArray 的 tensor 可使用檔案映射。
- GGUF 量化轉換、重新量化、型別轉換或模型特定 sanitize 所產生的新 tensor，仍會配置新的 MLX 記憶體；因此節省幅度取決於模型格式。
- DFlash：Target 與 Draft 的 safetensors 會沿用同一個載入模式。

## 啟用方式

mlx-server 接受下列參數：

- `--mmap`
- `--no-mmap`
- `--mmap-reserve-gb <數值>`

一般使用者可由 Tanpopo 介面啟用；命令列參數保留給直接啟動 mlx-server 的情境。

## 已完成的驗證（2026-08-29，M4 Pro 64 GiB，模型放在外接 SSD）

| 模型／模式 | 載入時間 | 行程 footprint | 映射常駐 | tok/s |
| --- | --- | --- | --- | --- |
| Qwen3.5-4B-4bit eager | 1.2 s | 2.56 GiB | — | 70–80 |
| Qwen3.5-4B-4bit mmap（對齊後） | 1.0 s | 0.34 GiB | 2.22 GiB | 73–79 |
| Qwen3.8-27B-4bit eager | 42.6 s | 14.73 GiB | — | 14.53 |
| Qwen3.8-27B-4bit mmap（對齊後） | 1.2 s | 0.63 GiB | 14.12 GiB | 14.33–14.68 |

同一組 greedy 參數下，eager 與 MMap 的輸出 SHA-1 完全相同。MMap 的主要效益是載入時間（27B 由 42.6 s 降到 1.2 s）與把權重從行程記憶體移到可回收的檔案快取，吞吐量沒有明顯差異。

尚未驗證：70B 級模型、視覺模型、DFlash、GGUF 映射路徑，以及長時間生成與記憶體壓力下的頁面回收行為。守門的超限分支在上述測試中沒有觸發（保留值最大 48 GiB，預算 16 GiB 仍高於 27B 的 14.75 GiB）。

## 正式開放前檢查

- 以不同記憶體容量測試 8B、30B 與 70B 級模型。
- 比較 eager 與 MMap 的載入尖峰、常駐記憶體、首次 Token 延遲及持續生成速度。
- 驗證 MLX、GGUF、視覺模型與 DFlash 的啟動、停止及重載。
- 驗證記憶體壓力下的頁面回收與再次載入行為。
- 持續比較不同模型格式的映射覆蓋率，並更新多國語言說明。
