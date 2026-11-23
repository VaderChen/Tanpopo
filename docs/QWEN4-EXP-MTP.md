# Qwen3.8-Flash-Next／Qwen4-Exp MTP

更新日期：2026-11-12。

本次加入 llama-server 與原生 Swift MLX 的文字 MTP 路徑。**驗證狀態是編譯與靜態邏輯檢查完成；依使用者要求，不執行模型載入、推論、Smoke、效能或雙機測試。** 不代表目前已驗收推論品質、接受率、速度或叢集行為。

## 模型與來源

[Unsloth Qwen3.8-Flash-Next-GGUF](https://huggingface.co/unsloth/Qwen3.8-Flash-Next-GGUF) 的 GGUF 架構是 `qwen4exp`，對應 Hugging Face `qwen4_exp`／`qwen4_exp_text`。它增加 Hyper-Connection 寬殘差、QSA 索引注意力與 PLE n-gram embedding，不能套用 Qwen3.5 的名稱別名。

- 模型標頭比對版本：`084c631ac221016019d6e0a863afe92d90edd3b0`。
- llama-server 參考 [llama.cpp PR #28243](https://github.com/ggml-org/llama.cpp/pull/28243)，來源提交 `6fcaa16f4b360649933a54d1f91ad40ed35c0e11`；移植 C++ 部分，另依 MLX-VLM 參考實作修正 MTP hidden normalization 為跨全部 HC stream 的全域 RMS。
- Swift 參考 [MLX-VLM Qwen4-Exp](https://github.com/Blaizzy/mlx-vlm/tree/633757c84b48b736fb1c259683aeca80e1384834/mlx_vlm/models/qwen4_exp) 與其 MTP drafter；保留 MIT 授權於 `Qwen4Exp-LICENSE.txt`。未執行 Python，也沒有引入 Python 執行期。

## 安裝與啟動

1. 在 Tanpopo 的模型下載頁選擇主模型 GGUF 分片。companion discovery 會排入同組所有分片，並優先選取同倉庫的 shared Q8 MTP 頭。
2. 建議草稿為 `MTP/mtp-Qwen3.8-Flash-Next-shared-Q8_0.gguf`。shared 指共用 Target 的 token embedding 與輸出頭，不是共用 KV Cache。草稿檔不可當作主模型啟動。
3. 等待所有分片與草稿下載完成。主模型選單只顯示第一片；缺片時會明確報錯。配對不是僅靠檔名：Go 檢查 GGUF 架構、角色與維度，MLX 載入器再驗證完整權重名稱、形狀及 Target／Draft 設定。
4. llama-server 選擇 MTP profile，搭配外部 Draft。參數為 `--model-draft <草稿.gguf> --spec-type draft-mtp --spec-draft-n-max 2`。
5. MLX 選擇 MLX MTP profile，搭配同一個 GGUF Draft。參數為 `--mtp-draft <草稿.gguf> --mtp-block-size 2`。請使用文字模式、清空 mmproj，並以 `temperature: 0` 產生請求。

MLX 也註冊原生 safetensors Target 與 `qwen4_exp_mtp` 草稿型別。外部 GGUF 頭可以配原生或 GGUF／Fast GGUF Target，由 Swift 驗證配置；Fast GGUF 本身不是 llama.cpp 格式。Qwen3.5 既有的內嵌 MTP 流程仍保留。

## 實作範圍

| 項目 | 處理方式 |
| --- | --- |
| HC | 每個 stream 的零中心 RMS、低秩混合、注入閘門與最終 mixer |
| GDN | 重用既有 recurrent 實作，額外提供 L2 Q/K normalization 與 sigmoid output gate；既有模型預設不變 |
| QSA | 完整區塊平均、RoPE、FP32 索引分數、top-k 區塊與 causal tail；以遮罩交給標準 SDPA |
| PLE | SplitMix64 n-gram 雜湊、EOS 邊界、分片 embedding、分組正規化與 causal depthwise convolution |
| MTP | 讀取 Target 最後的寬隱藏狀態、全域 hidden RMS、逐 stream projection、草稿 attention／MoE／HC mixer |
| 快取 | KV 與索引同時 trim；recurrent、PLE convolution 與 token history 原子 checkpoint／回滾 |
| GGUF | 依 metadata 還原設定、norm offset、GDN head 排序、共享專家閘門維度、indexer projection 與 MTP 合併 projection |
| 分片 | 驗證 split metadata、完整載入、重複 tensor 檢查、合併量化策略與記憶體估算；Fast GGUF 快取識別包含每一片 |
| 量化 | 未指定 group size 時，依所有 tensor 的內層維度從 64 降至 32；支援 PLE 160 維列。明確指定不相容的 group size 仍拒絕 |

實際發布的 UD-IQ1_M 第一片僅含 metadata，第二片有 1,223 個 tensor，最後一片保存 PLE 表。因此不能以第一片的權重數量或檔案大小代表整個模型。MTP 頭的 block_count 是 49、nextn_predict_layers 是 1；主模型 block_count 是 48。配對比較的是扣除 MTP 層後的主幹層數。

## 目前限制

- 原生 MLX 這次只實作文字路徑；未接 Qwen4-Exp 的影像 encoder／MRoPE 影像座標。指定 mmproj 時明確拒絕。
- 原生 MLX MTP 先限制 greedy、block size 2，使用一個 token 的 recurrent checkpoint。非 greedy 請求沿用迭代器的標準生成 fallback，不宣稱 MTP 加速。
- QSA 目前有正確的索引與稀疏遮罩語意，但仍由標準 SDPA 計算；尚未移植稀疏 attention Metal kernel，不能宣稱已達上游速度或長 context 記憶體效率。
- PLE token 雜湊與選取 shard 使用少量 CPU 同步；尚未進行效能優化驗收。
- 不保證任意模型都有 MTP：模型必須具有相容、已訓練的 prediction head。一般 Draft／DFlash／MTP 的契約不能互換。
- 未下載完整模型權重，未啟動本機或遠端推論；僅讀取發布檔的 metadata／tensor table 前綴作靜態比對。

## 驗證記錄

- llama-server：CMake Release 編譯。
- MLX：`swift build --package-path mlx-server -c release`。
- Tanpopo：`go build -buildvcs=false ./...`；專案同時含 Git 與 SVN 工作資料，停用 Go 的 VCS stamping 以完成建置。
- 前端：Node 語法檢查。
- 差異：`git diff --check`。

未執行推論或 Smoke 測試；後續開放測試時，需要驗證主模型單獨生成、MTP 全拒絕／部分接受的 cache 回滾、長 context QSA、Fast GGUF 重載與叢集一致性。

## 2026-11-23 原生引擎相容層更新

為加入 Inkling-2-Mini，llama-server 更新到固定上游快照。Qwen4-Exp 採用上游新的圖建構與 QSA indexer 快取，同時保留外部 shared MTP 頭、全域 hidden RMS 與 Target 權重借用；詳見 [原生引擎修補記錄](../llama-server/SOURCE_PATCHES.md)。已通過小型合成 Qwen4-Exp 一般解碼的 Metal／CPU 比對、快取還原與混合 batch 檢查；未重新驗收實際 Qwen4-Exp MTP 權重、品質或接受率，不能把一般解碼 Smoke 當成 MTP 驗收。
