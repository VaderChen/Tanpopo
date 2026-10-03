# 全專案函式級最佳化

2026-10-03。本輪檢查 Go 管理服務、Swift 原生 Runtime、網頁串流與排版、檔案處理、背景工作及測試工具，完成七處共用路徑的最佳化。模型索引檢查、GGUF metadata 掃描與大型 SSE 事件解析的基準耗時，分別減少約 89%、72% 與 94%。這些是指定函式情境的量測，不能相加或解讀為整個 App 的推論加速比例。

原生版本由 `rdma10` 更新為 `rdma11`；叢集同步協定仍為 `model_sync_version: 2`。本輪驗證使用隔離設定與新建 Runtime，部署時各節點仍須使用同一份完整原生成品。前一輪分散推論與模型內容核對的結果保留於[MLX 函式最佳化報告](MLX-FUNCTION-OPTIMIZATION.md)，兩輪數字分開計算。

## 修改的函式與共用策略

| 路徑／函式 | 原本成本 | 修改與行為界線 |
| --- | --- | --- |
| `llamacpp.isMLXModelDirectory` | 依每個張量重複檢查其分片檔案 | 每次呼叫只核對各個不同分片一次；下一次呼叫重新確認，不建立可能過期的全域模型快取 |
| `llamacpp.skipGGUFBytes`、`readGGUFUint32/64` | 略過詞表時，逐項建立讀取／複製物件 | 對已有的 `bufio.Reader` 使用 Peek／Discard；一般 Reader 保留原本路徑，維持截斷錯誤及讀取位置 |
| `logBuffer.trimLocked` | 日誌達上限後反覆複製整份尾端 | 推進 `bytes.Buffer` 的讀取位置，由緩衝區攤銷搬移；輸出長度、換行邊界與鎖的範圍不變 |
| `Server.runtimeChatHTTPClient` | 每筆聊天建立並銷毀 Transport，無法重用連線 | 每個 Server 共用一個 Client；最多保留 8 條閒置連線、每個目的地 4 條，閒置 30 秒回收；Shutdown 關閉閒置連線 |
| `chat-stream.consume` | 每收到封包，重新掃描累積中的完整事件 | 只掃描新片段及前一片段末端 3 個字元，事件完成時才合併；保留 UTF-8、混合換行、多行 data、DONE 與取消處理 |
| `updateStreamingMessage` | 回答每次增長，都重排未改變的思考內容與公式 | 各區塊保存已呈現的原文，只在原文改變時排版；完成、折疊、用量與捲動狀態仍正常更新 |
| `RuntimeAccessControl.refreshIfNeeded`、`isIPAllowed` | 每次請求重新解析每一條 CIDR／位址與萬用字元規則 | 策略載入時編譯規則，每個請求只解析來源位址一次；保留每 10 秒更新、無效策略拒絕存取、IPv4／IPv6 與金鑰檢查 |

以上策略依分片檔名、Reader 型別、事件邊界、內容變化與 IP 規則生效，沒有依特定模型名稱分支。聊天金鑰仍放在各自的 Request，不保存 Cookie、不跟隨重新導向、不使用環境 HTTP Proxy；原有請求期限與取消傳遞保留。

## 前後基準

設備為 Apple M4 Pro／64 GiB、macOS 27.0；工具鏈為 Go 1.27.0、Node 26.6.0、Swift 6.4。所有效能樣本依序執行，量測期間沒有並行執行其他基準、建置或測試。以下取五次樣本的中位數；原始樣本及來源摘要見[機器可讀紀錄](validation/project-function-optimization-2026-10-03.json)。

| 情境 | 修改前 | 修改後 | 耗時減少 |
| --- | ---: | ---: | ---: |
| 4,096 個張量、4 份分片的 MLX 索引 | 6.861 ms | 0.758 ms | 89.0% |
| 32,768 筆詞表的 GGUF metadata | 1.193 ms | 0.330 ms | 72.4% |
| 已滿 128 KiB 日誌再追加一行 128 bytes | 5.213 µs | 0.0114 µs | 99.8% |
| 本機 HTTP 請求：每次建池／共用連線池 | 111.571 µs | 37.664 µs | 66.2% |
| 256 條 IP 規則，最後一條命中 | 180.058 µs | 1.552 µs | 99.1% |
| 單筆 294,952 bytes SSE 資料，每片 32 bytes | 107.650 ms | 6.276 ms | 94.2% |
| 2,000 筆一般 SSE 事件，每片 97 bytes | 1.178 ms | 1.209 ms | 增加 2.6%，未觀察到改善 |

一般事件的前後樣本範圍分別為 1.114–1.652 ms、1.184–1.880 ms，範圍重疊；本輪只主張大型跨封包事件的解析改善。SSE 使用記憶體串流並逐筆核對輸出，不含網路與瀏覽器排版時間。

Go 檔案基準使用已落盤的模擬資料，不載入模型。每組執行 `-benchtime=300ms -count=5`。索引每次仍完整解析 JSON，缺檔及路徑越界仍會失敗。日誌基準只量測記憶體追加，不包含寫入磁碟、讀取日誌 API 或呈現 UI。

| Go 每次操作的配置量中位數 | 修改前 | 修改後 |
| --- | ---: | ---: |
| 模型索引 | 2,690,474 bytes | 920,230 bytes |
| GGUF metadata | 1,117,534 bytes／65,564 次 | 66,008 bytes／14 次 |
| 日誌追加 | 65,540 bytes | 0 bytes |
| HTTP 基準的整筆操作 | 26,900 bytes／179 次 | 9,419 bytes／89 次 |

日誌原版不是零配置：Go 輸出的 `allocs/op` 會將不足一次的平均值取整數，應一併查看 `B/op`。HTTP 比較使用同一個本機測試伺服器與回應，舊作法每次建立／關閉 Client，新作法重用 Client；不包含模型生成。為避免測試自身大量短連線耗盡臨時埠，採固定 `-benchtime=100x -count=5`。最初採自動迭代次數的測試遇到臨時埠耗盡，該次資料未納入結果。

Swift 白名單基準先載入策略暖機，各樣本檢查 1,000 次；這是 256 條混合規則且最後命中的情境，不代表只有一條規則的常見設定也能獲得同樣改善。檔案更新與金鑰雜湊成本沒有算入這項改善。

聊天呈現另以固定思考內容、300 次答案更新及一次完成通知核對：Markdown 與公式排版各由 602 次降至 301 次。測試呼叫正式更新函式並核對 DOM 替身狀態，這是呼叫次數改善，沒有宣稱瀏覽器實際繪製時間減半。

## 專案檢查範圍

| 區域 | 檢查重點與處理 |
| --- | --- |
| Go API、模型管理、模型列表與日誌 | 修改索引、GGUF、日誌、聊天 Client；保留載入、模型切換、串流與錯誤處理 |
| MLX 原生 HTTP、權限與請求執行 | 修改 IP 規則解析；檢查既有 HTTP keep-alive、取消、有限佇列與請求資源限制 |
| 分散執行、權重與 GGUF／推測解碼 | 沿用前輪通用分片、控制張量及有限零值快取；本輪不更動數值運算或通訊輪數 |
| 模型同步、下載、更新安裝與驗證 | 核對既有雜湊緩衝重用、串流寫入、進度批次回報及原子替換；完整摘要驗證保留 |
| 設定、啟動參數、登入工作階段 | 核對型別化複製、記憶體查詢、鎖與持久化邊界；保留安全性所需的複製與撤銷步驟 |
| 系統狀態、NetPass、更新檢查 | 核對背景快照、程序生命週期、狀態快取及取消；避免將週期性工作移入每筆請求 |
| 網頁聊天、模型與下載介面 | 修改串流解析及內容更新；檢查輪詢、狀態呈現與設定儲存流程 |
| 桌面殼層、目錄瀏覽、建置工具 | 桌面 GPU 狀態已使用共用快照與循序請求；目錄瀏覽與啟動／建置屬使用者操作或冷路徑，本輪未新增跨操作快取 |

本輪聚焦專案自行維護的函式及高頻路徑，沒有為了增加修改數量而重寫第三方函式庫。未測量的函式沒有附加推測的加速數字。

## 正確性與 Smoke

本輪測試結果與實際模型輸出記錄於同一份[驗證 JSON](validation/project-function-optimization-2026-10-03.json)。

- Go 全套通過；`llamacpp` 與 `api` 通過 race detector；追加的聊天測試確認上游已收到請求後取消，且下一筆仍可成功。
- 24 項 Node 測試與 25 個 JavaScript 檔案語法檢查通過。
- Swift 共 64 項：61 項通過，3 項需另外指定真實 GGUF 原始佈局／Tokenizer／Layer Trace 的診斷測試依既有條件略過。
- 原生完整 Smoke 通過：12 組架構／量化微型案例、三 Rank 不等長分片，以及三個獨立 Server 的 UDP 探索、交握、缺檔同步、故障回收與重啟。
- 實際模型三組、26 筆生成請求全部通過：Qwen3-0.6B Q4、Qwen3.5-4B GGUF（含圖片）、Qwen3.5-4B＋DFlash；英文、中文多輪、跨區塊 Prefill、SSE，以及 DFlash 的標準解碼對照均一致。

新增回歸檢查包括隨機日誌切片與 Reset、GGUF 短讀及剩餘位置、刪檔後重新核對、SSE 每個位元組切點，以及 IP 位址家族、CIDR 邊界、萬用字元和無效策略。HTTP 測試確認不同金鑰不互相沿用、不保存 Cookie、不跟隨重新導向；原生 Smoke 另涵蓋四人並行、第五人 429、取消隔離與名額回收。

模型比對逐筆檢查回答、推理內容、輸入／輸出／總 Token 數與結束原因；文字只忽略前後空白。DFlash 另確認實際草稿提案、未回退到標準解碼，以及 worker 不載入 Draft。這一輪未重跑全部 Fast GGUF／MTP／DFlash2 矩陣，前輪的六組 55 筆紀錄仍保留在 MLX 最佳化報告中，不能當作 `rdma11` 的新測量。

全套測試也修正兩項既有測試問題：叢集 UI 測試資料更新為同步協定 2，並確認協定 0／1 仍被拒絕；網路診斷清理測試等待 Socket 的 close 事件，不再把尚未派送的事件誤判為連線洩漏。

## 重跑方式

在專案根目錄依序執行效能基準；避免同時編譯、載入模型或執行其他基準：

```sh
go test ./src/llamacpp -run '^$' -bench 'Benchmark(RuntimeLogAppend|MLXShardInventory|GGUFTokenMetadata)$' -benchmem -benchtime=300ms -count=5
go test ./src/api -run '^$' -bench '^BenchmarkRuntimeChatTransport$' -benchmem -benchtime=100x -count=5
node scripts/benchmark-chat-stream.cjs
node tests/chat-render.test.cjs website/assets/chat.js
mkdir -p .cache/project-function-optimization
swiftc -O mlx-server/Sources/MLXServer/AccessControl.swift scripts/benchmark-runtime-access.swift -o .cache/project-function-optimization/access-benchmark
.cache/project-function-optimization/access-benchmark
```

上述指令重跑目前版本；修改前資料來自本輪開始時的工作目錄快照，不能將基底 commit 誤當成當時完整程式。驗證 JSON 保存前後檔案 SHA-256、工具 SHA-256、原生與 Go 執行檔 SHA-256。HTTP 的舊作法參考路徑保留在基準工具中。

建置與服務 Smoke 使用隔離成品：

```sh
MLX_SERVER_OUTPUT_DIR="$PWD/.cache/project-function-optimization/runtime" scripts/build-mlx-server-runtime.sh
TANPOPO_DISTRIBUTED_SMOKE=1 TANPOPO_MLX_SERVER="$PWD/.cache/project-function-optimization/runtime/bin/mlx-server" go test ./tests/distributed -count=1 -v -timeout 8m
go test ./...
go test -race ./src/llamacpp ./src/api
node --test tests/*.test.cjs tests/*.test.mjs
TANPOPO_MLX_JACCL=1 LLAMA_LOADER_PREBUILT_METALLIB=1 swift test --package-path mlx-server -c release -Xswiftc -suppress-warnings
```

多模型工具與案例欄位見[多模型驗證指南](MLX-RDMA.md#重複執行本機多模型驗證)。本輪沒有重新驗證實體 Wi-Fi 或 Thunderbolt RDMA，也未替換已安裝的 App 或發布 Release；同機 Smoke 的成功與函式基準不能代替實體網路的吞吐驗收。
