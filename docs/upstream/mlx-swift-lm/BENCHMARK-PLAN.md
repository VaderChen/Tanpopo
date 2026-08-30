# MLX MMap 與 DFlash 量測規格

本規格用來產生可供上游審查的證據。所有比較必須固定模型、prompt、生成參數、KV cache、系統版本與硬體，不以單次最佳值代表結果。

## MMap 實驗

### 測試組別

1. 官方 main loader。
2. Tanpopo mmap loader，MMap 關閉。
3. Tanpopo mmap loader，MMap 開啟。

若第 1、2 組不一致，先解釋 Tanpopo fork 的其他差異，不直接把差值歸因於 mmap。

### 模型級距

- `fit`：權重與 KV cache 可舒適放入實體記憶體。
- `near`：穩態用量接近實體記憶體。
- `over`：模型明顯超過可用實體記憶體，用來觀察 page-fault thrashing，不預設它具備實用速度。

### 每輪流程

1. 關閉其他大量使用 GPU／記憶體的程式。
2. 記錄 macOS、晶片、總記憶體、電源模式與溫度狀態。
3. 清楚標示是否為冷檔案快取。冷啟動與暖啟動分開統計，不混合平均。
4. 啟動 Runtime，記錄開始載入、服務可用與第一個 token 的時間。
5. 使用固定 prompt，先產生 32 tokens 暖機，再產生 256 tokens 計時。
6. 每秒記錄 process footprint、RSS、mapped-file residency 與 page faults。
7. 每組至少執行 5 輪，報告 median、p90 與最差值。

### 必填欄位

| 欄位 | 單位 | 備註 |
|---|---:|---|
| load_seconds | 秒 | 從啟動到模型服務可接受請求 |
| first_token_seconds | 秒 | 從請求到第一個輸出 token |
| prompt_tokens_per_second | tok/s | 使用 Runtime 回報值 |
| generation_tokens_per_second | tok/s | 固定輸出長度 |
| peak_phys_footprint | MiB | 行程實體 footprint |
| steady_phys_footprint | MiB | 暖機後中位數 |
| mapped_virtual_bytes | MiB | 活著的權重映射虛擬位元組 |
| mapped_resident_unique | MiB | 依檔案 inode 與 page offset 去重 |
| copied_fallback_bytes | MiB | 對齊失敗而複製的 tensor payload |
| page_faults | 次 | 若系統工具可取得 |
| memory_pressure | 等級 | normal／warn／critical |

### 判讀原則

- MMap 降低 `phys_footprint`，但 `mapped_resident_unique` 同幅增加時，不得宣稱實體記憶體等量下降。
- 冷載入變快但 generation tok/s 明顯下降時，必須把它描述為啟動與執行效能交換。
- `over` 組若長時間 swap／fault、tok/s 接近不可用，只能證明能啟動，不能證明能實際運作。
- 記憶體目標是 guardrail，不是 Runtime 的硬性使用上限。

## DFlash 實驗

### 固定條件

- 相同 target 模型與量化。
- 相同 prompt 與輸出上限。
- 相同 temperature、top-p、top-k、min-p、seed 與 LogitProcessor。
- 相同 KV cache 策略。

### 比較組別

1. Target 普通自回歸生成。
2. DFlash greedy。
3. DFlash stochastic exact rejection sampling。

### 必填欄位

- prompt tok/s、generation tok/s。
- proposed、accepted、emitted token 數。
- acceptance rate。
- target calls、draft calls、verified positions。
- peak memory 與 draft 模型額外權重。
- stochastic 組的分布一致性檢驗。

### 正確性門檻

1. Greedy 必須與普通 target 逐 token 相同。
2. Sampling 不要求相同 seed 產生逐 token 相同序列，但大量樣本的輸出分布必須與 target 基準相容。
3. 首次 rejection 後只提交 accepted prefix 與 correction token；被拒絕的 KV／recurrent state 不可進入下一輪。
4. Stateful LogitProcessor 只能看到真正輸出的 token。
5. 不支援的 cache、batch 或多模態輸入必須 fail closed 或清楚退回普通生成。
