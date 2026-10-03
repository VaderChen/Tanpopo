# MLX 叢集與模型同步的函式級最佳化

2026-10-03，針對分散推論的高頻函式與模型內容核對完成量測、修改及 Smoke。**同機雙 Rank 的 48 Token 請求耗時中位數降低約 26–28%；固定模型檔案基準的核對耗時降低約 59%，既有模型重用耗時降低約 88%。** 以下數字只代表各自的測試條件，不是雙實體 Mac 或 Wi-Fi 的加速保證。

原生版本由 `rdma9` 更新為 `rdma10`，模型同步協定仍為 `model_sync_version: 2`。這是原始碼與隔離建置的驗證紀錄；未替換已安裝的 App 或發布 Release。部署時各節點仍須使用相同 Server 與同一份完整原生 Runtime。

## 修改的函式

| 位置 | 原本成本 | 本次修改 |
| --- | --- | --- |
| `DistributedTensorSession.execute` | 每次線性層運算都建立控制陣列、MLX 張量及描述字串 | 每層只保留最近一次形狀／型別的控制訊息；形狀改變時替換 |
| `DistributedTensorSession.workerLoop` | 每層都以 lazy Metal zeros 建立控制訊息及輸入零值 | 控制訊息改為一次建立的 CPU 零值張量；輸入零值交由有限容量快取重用 |
| `DistributedZeroBuffers.zeros` | 新增共用的零值張量管理 | 依 shape／dtype 重用，LRU 淘汰；最多保留 16 MiB、32 筆，超大張量不留存 |
| `modelbundle.CreateSelection`、`Matches`、`Ensure` | 每份檔案各配置 1 MiB 複製緩衝區與雜湊物件 | 每次函式呼叫共用一份緩衝區與 SHA-256 hasher，換檔時重設 |
| `modelbundle.Ensure` | 即使指定模型已正確，也先掃描整個模型庫 | 優先完整核對指定位置與內容摘要快取，命中即返回；未命中才掃描其他目錄 |

上述修改依資料型別、形狀與檔案內容生效，沒有模型名稱特例。既有的完整 SHA-256 核對、取消、路徑邊界與下載完成後才發布副本的行為均保留。

零值張量保留強參考，避免 collective 將其 backing buffer 當作可捐贈輸入；多次請求與多 Rank 的數值比對用來檢查是否殘留上一輪資料。16 MiB 是快取所保留張量的上限，不是 Runtime RSS 或整個 Metal allocator 的上限。控制訊息的重用沿用原本通訊鎖，並行請求不會交錯改寫同一份標頭。

## 原生推論前後比較

設備為 Apple M4 Pro／64 GiB、macOS 27.0；兩個獨立 Go Server 及原生 MLX 程序使用 loopback TCP Ring，共用本機 GPU。使用 release 建置，固定 temperature 0、Context 4096、Prefill 128；關閉 KV 量化、mmap、效能校準與 Server 的自動記憶體壓力調整。Runtime 的請求記憶體預算檢查仍保留。

基準版為前一輪 `rdma9` 加入相同的函式計時器；最佳化版為 `rdma10`。兩版都啟用 `TANPOPO_DISTRIBUTED_PROFILE=1`，每個模式各暖機一次，再量測五次完整 HTTP 請求。量測時沒有並行執行其他基準、編譯或測試。提示固定為「請用繁體中文連續描述一隻紅色小鳥在森林尋找食物的過程，至少一百字，不要標題。」，每次均輸出 48 Token。

| 模型 | 原叢集耗時中位數 | 最佳化後 | 耗時減少 | 原單機／新版單機中位數 |
| --- | ---: | ---: | ---: | ---: |
| Qwen3-0.6B Q4 | 5,960 ms | 4,265 ms | 28.4% | 144／143 ms |
| Qwen3.5-4B aligned Q4 | 7,949 ms | 5,909 ms | 25.7% | 711／710 ms |

所有暖機及量測回答、推理內容、輸入／輸出／總 Token 數與結束原因，在同版單機／叢集及修改前後均相同；文字比較只忽略前後空白。一般短句、多輪、長 Prefill 與 SSE 也通過比對。

這兩個可放入本機的小型模型，雙 Rank 仍明顯慢於單機。改善的是既有叢集路徑的函式成本，不能據此認為使用叢集就能加速，也不能外推無法放入單台的大型模型表現。

### 耗時下降的位置

下表是五次量測之間的累積差值，單位為秒：

| 模型／函式區段 | 原版 | 最佳化後 |
| --- | ---: | ---: |
| Qwen3-0.6B 控制訊息 collective | 9.358 | 4.283 |
| Qwen3-0.6B 輸入 collective | 6.422 | 3.622 |
| Qwen3.5-4B 控制訊息 collective | 11.549 | 5.392 |
| Qwen3.5-4B 輸入 collective | 8.406 | 4.784 |

五次請求的資料交換次數未變：兩個模型分別仍有 48,020／60,760 次輸入交換及相同次數的輸出匯集。控制訊息數另包含量測期間的心跳。資料張量大小與數值運算方式保持相同，降低的是反覆配置、零值求值及相互等待的成本。

計時點位於 `DistributedGroup.collective`：`input_eval_ms` 記錄 collective 前的輸入求值，`gather.input_eval_ms` 包含本地線性運算；`collective_ms` 包含 CPU stream、通訊及其他 Rank 的等待，不能視為純網路延遲。`input_bytes` 是本 Rank 傳入的張量大小，不能視為網卡流量。計時器預設關閉，診斷方式見[函式耗時量測](MLX-RDMA.md#函式耗時量測)。

## 模型核對前後比較

Go 基準使用實際落盤的模擬資料與完整 SHA-256：12 份各 22 bytes 的 JSON、4 份各 256 KiB 的權重資料檔，共 1,048,840 bytes；不執行模型載入。`Ensure` 額外加入 256 個其他模型目錄。檔案已暖機，每組執行五次 `-benchtime=500ms -benchmem`，下表為中位數。

| 函式情境 | 原耗時 | 最佳化後 | 耗時減少 | 每次配置量：原版 → 新版 |
| --- | ---: | ---: | ---: | ---: |
| 建立內容清單 | 1.509 ms | 0.627 ms | 58.4% | 16.04 → 1.03 MiB |
| 核對內容清單 | 1.490 ms | 0.612 ms | 58.9% | 16.02 → 1.02 MiB |
| 重用已有正確模型 | 5.365 ms | 0.634 ms | 88.2% | 16.58 → 1.03 MiB |

配置量約減少 94%。這組基準刻意包含多份小檔案，以量測逐檔配置成本；大型權重的磁碟讀取與完整 SHA-256 成本仍存在，不能把這些百分比當作數十 GB 模型的總載入時間改善。

## 回歸與證據

本輪沿用同一個多模型工具，關閉 profiler 後再跑 GGUF、Fast GGUF 與推測解碼路徑，**六組共 55 次生成請求全部通過**。驗證涵蓋 Qwen3.5-4B GGUF、Gemma4-E2B 獨立 Fast GGUF、Qwen3.5-4B 獨立 Fast GGUF、Qwen3.5-4B＋DFlash、Qwen3.5-9B GGUF＋MTP、Qwen3.8-27B＋DFlash2；Qwen3.5 的 GGUF 與獨立 Fast GGUF 也包含圖片。推測解碼檢查提案計數、標準解碼對照及 worker 不載入 Draft。

其他檢查包括 13 項 Swift 測試、12 組架構／量化微型案例、三 Rank 不等長分片及三個 Server 的探索／交握／回收。並行、第五人 429、取消隔離、父端 EOF、worker／主節點故障及重新啟動沿用現有原生 Smoke。Go 全套與 `modelbundle`／`cluster` 的 race detector 通過。

[機器可讀驗證紀錄](validation/mlx-function-optimization-2026-10-03.json) 保存每次耗時、計時快照、回答、模型設定摘要、原生與 Go 執行檔 SHA-256、測試工具 SHA-256，以及格式回歸結果。執行時工作目錄含未提交修改，基底 commit 與實際建置明確分開。完整重跑方式見[多模型與函式量測指南](MLX-RDMA.md#重複執行本機多模型驗證)；Go 函式基準位於 [bundle_benchmark_test.go](../src/modelbundle/bundle_benchmark_test.go)。

本輪沒有重新驗證實體 Wi-Fi 或 Thunderbolt JACCL RDMA，也沒有改變通訊輪數、逾時期限或支援的模型契約。原有 [GGUF／推測解碼限制](MLX-CLUSTER-FORMATS-VALIDATION.md#目前限制)仍適用。
