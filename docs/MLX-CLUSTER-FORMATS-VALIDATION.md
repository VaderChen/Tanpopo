# GGUF／Fast GGUF 與推測解碼叢集驗證

2026-10-03（Asia/Taipei），接續[先前 8 份 safetensors 模型的驗證](MLX-CLUSTER-MODEL-VALIDATION.md)，完成 GGUF、獨立 Fast GGUF、DFlash、DFlash2 與 MTP 的叢集整合及本機測試。**修正後的 6 組案例全部通過，合計 23 組單機／雙節點比對、6 次 SSE 與 3 次標準解碼對照，共 55 次生成請求。**

這是 `rdma9` 原始碼與隔離建置的驗收紀錄；本次未更新已安裝 App 或發布 Release。兩端部署時須使用相同的 Server、`model_sync_version: 2` 及同一份完整原生 Runtime。舊版不能只替換網頁就取得新能力。

## 測試條件與結果

設備為 M4 Pro／64 GiB、macOS 27.0。每組都啟動兩個獨立 Go Server 與兩個原生 MLX 程序，經 loopback UDP 探索、內容核對、交握與 TCP Ring 啟動；兩個 Rank 共用同一台 Mac 的 GPU。正式程式為 Go、Swift 與 C++，測試協調使用 Node.js，沒有使用 Python。

固定 temperature 0、Context 4096、Prefill 128，關閉 KV 量化、mmap、效能校準與 Server 的自動記憶體壓力調整。原生 Runtime 的記憶體預算檢查仍保留。以下「每 Rank 線性權重」是已分片層的參數量，不是程序 RSS 或整個模型的記憶體峰值。

| 驗證組合 | 實際載入／解碼 | 分片層數 | 每 Rank 線性權重 GiB | 結果 |
| --- | --- | ---: | ---: | --- |
| Qwen3.5-4B GGUF | Q4_0 來源，auto 轉換為 INT8／BF16、group 64；文字＋圖片 | 346 | 2.07 | 通過 |
| Gemma4-E2B 獨立 Fast GGUF | schema 3、mode3、group 32；文字 | 276 | 0.57 | 通過 |
| Qwen3.5-4B 獨立 Fast GGUF | schema 4、auto、group 64；空節點同步、文字＋圖片 | 346 | 2.07 | 通過 |
| Qwen3.5-4B safetensors＋DFlash | 本機 aligned Q4 Target、相容 DFlash1 Draft，block 5 | 248 | 0.93 | 通過 |
| Qwen3.5-9B GGUF＋MTP | Q4_K_M 來源、mode1；原 GGUF 內嵌預測層，block 2 | 359 | 3.27 | 通過 |
| Qwen3.8-27B safetensors＋DFlash2 | Q4 Target、相容 DFlash2 Draft，block 5；文字 | 497 | 6.71 | 通過 |

Qwen3.8 是本機模型名稱，實際 `model_type` 仍為 `qwen3_5`。這 6 組是格式與解碼路徑的覆蓋，不代表 6 種不同架構，也不代表已驗證所有量化配置或 Draft 配對。

各組均執行英文短句、中文多輪及跨 Prefill 區塊的長提示。兩組 Qwen3.5-4B GGUF／Fast GGUF 加入 56 × 56 紅色 PNG；三組推測解碼另生成 48 Token 中文內容。檢查項目包括：

- 回答及推理內容去除前後空白後相同，輸入／輸出／總 Token 數及結束原因一致。
- SSE 收到 `[DONE]` 與使用量統計，重組內容符合單機結果。
- 叢集健康資訊回報兩個 Rank、實際分片層數及較小的本地線性權重。
- 每組結束後兩端 Runtime 停止，叢集 session 清除；測試建立的 Server 全部退出。

最終 GGUF 生成驗收使用已建立的 Fast GGUF 快取。不能用這些數字推論首次冷轉換的記憶體峰值；首次轉換仍沿用既有轉換器，需各台有足夠的本機記憶體與磁碟空間。

## 推測解碼確實有執行

主節點負責 Draft 生成與 KV；Target 驗證呼叫相同的分片線性層。工作節點不載入 Draft。測試同時檢查單機與叢集的草稿提案計數大於零、沒有退回標準解碼，並確認工作節點沒有 Draft 載入紀錄。

| 48 Token 案例 | 叢集提案 Token | 接受 Token | 該次接受率 | 與標準解碼對照 |
| --- | ---: | ---: | ---: | --- |
| DFlash1，Qwen3.5-4B | 144 | 9 | 6.2% | 內容、Token 數及結束原因一致 |
| DFlash2，Qwen3.8-27B | 35 | 11 | 31.4% | 內容、Token 數及結束原因一致 |
| 內嵌 MTP，Qwen3.5-9B GGUF | 26 | 21 | 80.8% | 內容、Token 數及結束原因一致 |

接受率是這次提示與草稿配對的日誌數值，不能當作模型的一般接受率或加速比。DFlash1 這次的接受率偏低，證明能正確運作，也說明啟用推測解碼不保證比較快。

## 測試中修正的問題

1. **GGUF 載入尚未接入分片。** GGUF／Fast GGUF 現在與 safetensors 共用載入前掛鉤。raw 張量直接依檔案位移按列讀取；LZFSE 張量逐一解壓至暫存檔，再建立 lazy Load。分片之後才實體化本地列，避免每個 worker 常駐完整解壓權重。Reader 保留開啟的檔案描述元，暫存檔取消連結後仍可完成讀取。
2. **GGUF 圖片回答與單機不同。** 實測發現 BF16 視覺投影的輸出寬度縮小後，Metal split-K 選擇不同加總分段，最大逐元素差異達 `0.00390625`。修正讓分片運算使用完整輸出寬度決定 K 加總順序，實際輸出與記憶體配置仍是本地列。修正依矩陣形狀生效，沒有加入模型或層名稱特例，也不改動一般單機 kernel。重新跑文字、圖片與 SSE 全部一致；啟用逐層診斷時沒有再回報非零差異。
3. **推測解碼會與 Target 的分片載入混在一起。** 現在先載入並分片 Target，再由主節點載入相容 Draft；worker 僅接收 Target 的模式與轉換策略，不接收 Draft 路徑。
4. **檔案模型與目錄模型的同步清單不同。** 新清單將 GGUF／Fast GGUF 入口一併納入摘要，只傳選定權重、分片與啟動資產。Fast GGUF 的 processor 與舊版 schema 3 資產也會核對；獨立 Fast GGUF 不會誤掛同目錄的原始 mmproj。配對期間保留原始 GGUF，不觸發單機的來源刪除選項。CLI 自動啟動 worker 時，同目錄的 mmproj 會跟隨其模型路徑，不再沿用主節點的絕對位置；此參數對應另有測試，尚未進行實體 SSH／RDMA 驗收。
5. **介面原本直接禁止 GGUF 與推測解碼。** 現在依 Runtime 能力及既有 Target／Draft 相容條件啟用，沿用 GGUF 轉換確認；叢集不提供略過快取的啟動選項。KV 量化互斥、舊版節點拒絕及 MTP 預測層缺失的檢查仍保留。

## 空工作節點同步

Qwen3.5-4B 獨立 Fast GGUF 案例使用原本為空的工作節點模型目錄，實際從主節點下載 **4,791,391,596 bytes**，包含三份 `.fgguf` 權重分片、manifest 與執行資產。每份檔案經大小及 SHA-256 核對後才發布副本；內容摘要為：

```text
aef63bb9d5841e1dfc2bb05ba02a754676b32267a7d788e00e49e59a3f448b00
```

工作節點沒有來源 `.gguf` 或外部 mmproj，仍能完成相同的文字、圖片與 SSE 回覆。這是實際檔案傳輸及模型載入測試，不是僅模擬進度或檔名。

六組矩陣之外，另以最後建置完成一次 CLI 自動 worker Smoke：兩個 Rank 使用不同 GGUF 目錄，只在主節點指定 mmproj，worker 正確對應自己的資產位置，重新建立所需快取後完成 `Ring ready` 回覆（21 個輸入、2 個輸出 Token）。程序停止並回收。這額外一次生成的 Runtime 雜湊與結果獨立保存；沒有算進上方矩陣的 55 次。

## 回歸與可追溯資料

- `go test ./...` 通過；最後的 Go 修改另重跑 `modelbundle`、`llamacpp`、`cluster`、`api`，全部通過。`modelbundle` 與 `cluster` 的 race detector 亦通過。
- 11 項 Swift 測試通過，涵蓋分散式設定、跨節點模型資產路徑、BF16 分片逐元素一致、raw／壓縮張量的延遲讀取及 Fast GGUF 邊界驗證。
- 最終 Runtime 重跑 12 組架構／量化微型案例全部通過，包含 Llama F32／Q4／Q8、Mistral、Phi3、Gemma2、Starcoder2、Qwen2、Qwen3、Qwen3 MoE、Qwen3.5 與文字入口。共用生命週期檢查涵蓋四人並行、第五人 429、取消隔離、worker／主節點故障、父端 EOF、重啟與回收。
- JavaScript、Shell 語法與 Git 空白檢查通過。另以 Node DOM 代替物檢查配對按鈕狀態、轉換確認及請求參數；此項不代表瀏覽器視覺驗收。

[機器可讀驗證紀錄](validation/mlx-cluster-formats-2026-10-03.json) 保存每組的原生執行檔與工具 SHA-256、Target／Draft 完整內容清單、逐項回答及 Token 數、分片統計與推測計數，已移除本機絕對路徑、管理位址、主機名稱與 PID。執行時工作目錄包含未提交修改，紀錄明確區分基底 commit 與實際測試成品；沒有將基底 commit 當作已包含本次修正的版本。

重跑方式見 [多模型工具](MLX-RDMA.md#重複執行本機多模型驗證)及 [GGUF／推測解碼設定](MLX-RDMA.md#gguf-與推測解碼的叢集設定)。

## 目前限制

- DFlash 仍需要既有單機相容的 safetensors Target／Draft；沒有新增 GGUF＋DFlash 的模型契約。MTP 本輪驗證的是原 GGUF 內嵌預測層，尚未另測外部 MTP Draft。
- 獨立 Fast GGUF 尚未保存內嵌 MTP 預測層，需要 MTP 時仍須保留原 GGUF。其他不支援的 Target／Draft 配對不能因加入叢集而自動相容。
- Draft、KV、Embedding、特殊算子及直接存取完整權重的運算仍可能由主節點負擔。首次冷轉換也沒有變成跨機轉換，不能把多台記憶體視為透明加總。
- 本輪沒有驗證隨機抽樣、長時間負載、多張高解析圖片或多於兩節點的真實模型。通用機制的覆蓋程度仍須靠更多 checkpoint 擴充。
- 沒有重新驗收兩台實體 Mac 的 Wi-Fi 傳輸，也沒有 Thunderbolt JACCL RDMA 硬體結果。先前的實體網路長請求問題仍待處理；同機正確不等於雙機加速或網路問題已修復。
