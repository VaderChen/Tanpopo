# Inkling-2-Mini

首次支援版本為 [Tanpopo 1.26.1123 build 1005](https://github.com/VaderChen/Tanpopo/releases/tag/v1.26.1123-build-1005)，發布於 2026-11-23。

## 支援範圍

Tanpopo 透過原生 `llama-server` 及 Apple Silicon 的 Swift `mlx-server` 支援 [Thinking Machines Inkling-2-Mini](https://huggingface.co/thinkingmachines/Inkling-2-Mini) 的文字與圖片推論。模型是 `inkling2_loop` 循環式 MoE，不能以 Qwen／Llama 的名稱別名載入。此整合不需要 Python。

| 路徑 | 狀態 |
| --- | --- |
| llama-server＋主線格式 GGUF | 已整合文字生成、聊天樣板與思考區段解析 |
| llama-server＋mmproj | 已整合圖片輸入 |
| mmap／CPU／GPU 混合載入 | 使用原生引擎通用路徑，另納入循環專家複製修正；效能與可用容量仍依硬體決定 |
| Swift MLX | 載入下列轉換好的 MLX safetensors，支援循環層、混合精度 MoE 與視覺塔 |
| Fast GGUF | 此模型使用 GGUF 時請選用 llama-server |
| TCP Ring／RDMA | 此次未加入此模型的分散推論支援 |
| DFlash／MTP | 此次未加入；官方 checkpoint 含 MTP 設定不代表目前引擎已可啟用 |

## Apple Silicon 上使用 MLX

從「模型下載」選擇 MLX，再開啟「常用模型」的 30B 級，點選 **Inkling 2 Mini · 4-bit MLX** 或 **8-bit MLX**。下載完成後，在「執行狀態」選擇 mlx-server 與該模型目錄，使用自動類型啟動。圖片處理參數取自模型目錄的 `config.json`。

兩個版本各有四個 safetensors 分片，權重共約 17.31 GB 與 18.09 GB。4-bit／8-bit 依發佈者對一般量化層的命名，路由專家仍保留混合精度。來源為 [MLX Community 4-bit](https://huggingface.co/mlx-community/Inkling-2-Mini-4bit) 與 [Pipe Network 8-bit](https://huggingface.co/pipenetwork/Inkling-2-Mini-MLX-8bit)。

Swift 與 Metal 程式移植自 MIT 授權的 [mlx-swift-lm PR #821](https://github.com/ml-explore/mlx-swift-lm/pull/821)。文字模型及核心使用 `09d7821133ba`，影像與訊息框架解析使用 `ba8b1af3f5f1`，並適配專案的 mlx-swift 0.31.6、權重載入器與 OpenAI 相容 API。

## 搭配 llama-server 使用 GGUF

1. 從「系統設定 → 關於」選擇「更新並重新啟動」，或從本頁上方 Release 安裝對應平台的套件。
2. 到「模型下載」選取 GGUF 的 **Inkling 2 Mini · IQ4_NL**。快速選單使用 `ggml-org/Inkling-2-Mini-GGUF`，固定版本為 `e8a2cecf0e78914f07cc27b9be711a9b2c7561d9`。
3. 主模型選擇 `Inkling-2-Mini-IQ4_NL.gguf`；下載器會依既有附屬檔規則配對 mmproj。文字用途可以不載入 mmproj；圖片用途必須選擇同模型的投影檔。
4. 在執行狀態選取 **llama-server** 與 **Inkling-2-Mini（GGUF，8K）**，指定下載完成的主模型，按下「載入並啟動」。
5. 使用簡易對話或 OpenAI 相容的 `/v1/chat/completions`。思考區段由原生聊天解析器輸出，不需手動套用其他模型的 template。

IQ4_NL 主檔約 17.63 GB，Q8_0 mmproj 約 0.35 GB；這只是磁碟檔案大小，執行時還需要 KV Cache、attention 暫存與系統記憶體。不能以檔案大小當成最低 RAM 保證。首次使用先採 8K context、parallel 1、batch 128、ubatch 64，確認可運作後再調整。

官方標示的長 context 上限不代表本機硬體能直接配置。預設不啟用 MTP／DFlash，也不強制關閉模型思考。

### 其他 GGUF 發佈來源

支援一般 `.gguf` 與發佈者使用的 `.gguf_file` 別名。下載請求保留原始遠端路徑，僅在本機落盤時把 `.gguf_file` 改為 `.gguf`；分片與 mmproj 也使用相同規則。imatrix 校準檔不會混入主模型選單。

常用模型中的 **UD-Q4_K_XL** 與 **UD-Q8_K_XL** 固定使用 Unsloth 主線相容的 `Mainline_Support/`，該目錄的權重副檔名為 `.gguf_file`。這項副檔名處理不會改寫 tensor 或模型架構，也不會把第三方 fork 格式轉換成主線格式。首次使用建議採上方固定版本的 ggml-org 檔案。

### API 範例

```json
{
  "messages": [{"role": "user", "content": "請用一句繁體中文介紹自己。"}],
  "temperature": 0.2,
  "max_tokens": 256,
  "chat_template_kwargs": {"reasoning_effort": "low"},
  "stream": true
}
```

`reasoning_effort` 控制模型樣板的思考強度，不能保證完全沒有思考 Token。使用者可以在直接呼叫原生 API 時指定此值；不會因此修改其他模型的預設值。

## 建置與驗證

此版 llama-server 的來源版本為 `custom-d2bceda3de6d-openloader.3-qwen4mtp2-inkling2mini1`。可用 `./build-llama-server.sh` 建置。完整固定來源與本地相容層記錄見 [原生引擎修補記錄](../llama-server/SOURCE_PATCHES.md)。

驗證結果保存於 [Smoke 記錄](validation/inkling2-mini-smoke-2026-11-23.json)。只記錄實際執行的檢查；小型 Smoke 不代表完整模型品質、長 context、效能或分散推論驗收。

本次在 Apple M4 Pro、64 GiB、macOS 27.2.1 上完成：

- MLX 4-bit 與 8-bit 各核對四個分片的 SHA-256。兩者經本機 `mlx-server` 的聊天 API 回答算術題 `4`，辨識純紅圖片，並以 SSE 回答 `Paris`。
- IQ4_NL 主模型與 Q8_0 mmproj 的 SHA-256 核對，以及經 Tanpopo 啟動完整模型。
- 原生文字生成與思考分離、Tanpopo 簡易對話、SSE 串流。
- 256×256 純紅色圖片輸入，模型回答 `red`。
- 原生 API 回傳結構化 `get_temperature({"city":"Taipei"})` 工具呼叫；沒有實際執行工具。
- 金鑰與 IP 白名單組合驗證、缺少金鑰時拒絕存取。
- 五組 Llama／Qwen3.5／Qwen4-Exp／Inkling 小型架構檢查、Go 對應套件測試與前端語法／下載選單 Smoke。

驗證使用獨立服務。MLX 兩個版本均逐一核對四個權重分片的 SHA-256。安裝包不含模型權重，下載所需容量請見本頁各模型說明。
