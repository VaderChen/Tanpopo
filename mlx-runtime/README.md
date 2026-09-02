# mlx-server Runtime

`mlx-server` 是 LlamaLoader 的 Apple Silicon 原生推論服務，模型載入方式參考 MangaKitchen：

- 文生文：`LLMModelFactory`
- 多模態：`VLMModelFactory`
- HTTP：SwiftNIO

服務提供 `/health`、`/v1/models`、`/v1/chat/completions`、`/v1/completions`、`/completion` 與 `/props`。

Chat Completion 與 Completion 的 `model` 省略、留空或與目前模型不符時，一律 fallback 至此程序已載入的模型；一般與串流的生成回應都標示實際模型 ID，不會根據客戶端名稱切換或重新載入模型。JSON 型別、權限與資源限制仍照常檢查。完整規則見 [請求模型名稱](../docs/MLX-RUNTIME-SPEC.md#請求模型名稱)；更新後需重新啟動 Runtime，既有程序才會使用新版行為。

版本固定於 `mlx-server/Package.swift`。打包後安裝到
`~/services/mlx-server/versions/<版本>/darwin-arm64`，並由 `current` 符號連結指向目前版本。
SwiftPM 產生的 `.bundle` 資源會與 `bin/mlx-server` 放在相同目錄，確保 MLX Metal Library 可由原生執行檔載入。

第一次成功建置的 `default.metallib` 會依平台、部署版本、Metal SDK／編譯器與 kernel 原始碼產生內容指紋，並保存於 `metal-cache/darwin-arm64/<fingerprint>/`。後續 `build.command`、`run.command` 會直接復用相同指紋的成品；`clean.command` 不會移除此快取。只有指紋改變或設定 `MLX_METALLIB_REBUILD=1` 時才會重新編譯。

## 長輸入與推論資源保護

SPEC 要求支援多人並行，目前階段先設定最多四人，詳見 [MLX Runtime 規格](../docs/MLX-RUNTIME-SPEC.md)。

支援分段的 Prefill 路徑依 `--prefill-step-size` 與本次資源預算分段，不能因模型具備視覺能力就忽略分段、一次處理整份上下文。共用的分段處理器會在每段完成後求值快取，保留 decoder state，並檢查取消與記憶體預算。Qwen 3.5 多模態路徑會先依完整輸入計算位置資訊，再同步切分 token、embedding、mask 與位置；後續生成會延續 RoPE delta。一般文字模型及 Gemma 4 的純文字 Prefill 也使用同一處理器；未宣告分段能力的路徑仍按完整輸入估算。

每次請求會在 tokenization 後、推論前檢查：

- 輸入與最大回答 token 數是否超過模型／啟動設定的上下文上限。
- 依模型 metadata 估算未量化 KV Cache、attention 與 logits 暫存，並參考裝置單一 buffer 上限、MLX 記憶體預算及目前用量。
- 模型是否實際支援分段 Prefill。支援時必要可暫時縮小分段；未宣告能力的路徑保守按整份輸入估算，不假設設定的分段大小一定生效。

不會默默截斷上下文、覆寫使用者設定或修改校準紀錄。預估超限時，非串流回傳 HTTP 413 與原因，已建立的串流改送 SSE error；Prefill／生成期間也會檢查記憶體。MLX 可攔截的運算／配置錯誤會轉成請求失敗：非串流回傳 JSON error，已開始的串流送出 SSE error 與結束標記，不偽裝成成功回答。

同一個 MLX Runtime 最多同時處理 4 個生成請求，第 5 個回傳 HTTP 429。各請求會共用記憶體預留帳本，估算 KV 與暫存後才進入推論；已實際配置的記憶體不重複扣除，尚未配置的預留額度也不會被其他請求再次使用。每個請求的暫存有份額上限，長上下文可能使用較小的 Prefill 分段。記憶體不足時即使名額未滿仍會拒絕請求，不能保證任意長度的 4 個上下文都能同時容納。

生成 worker 完成或失敗後會釋放名額與預留額度，取消請求也會停止後續分段／生成。執行中超過記憶體預算時，非串流回傳 HTTP 503。

HTTP 連線與生成工作共用每次請求獨立的取消訊號。外部程式中斷 8080 的 HTTP 請求時，會停止該請求後續的 Prefill／生成，不影響其他使用者；已提交的 GPU 運算仍需完成收尾。HTTP 層不再透過暫停 socket 讀取等待整份回應，避免長 Prefill／非串流期間延遲察覺斷線；改以有界佇列維持同連線的回應順序。日誌可用同一個 `request_id` 對照 `generation accepted`、`generation started`、`http request cancelled` 及 `generation released`。若外部程式只停止顯示、沒有中斷 HTTP，伺服器不會收到取消訊號。

日誌只記錄 token 數、實際分段大小與資源估算，不記錄提示內容。這是請求階段的保護，與介面上的「記憶體壓力保護」（載入前調整配置）不同；估算無法保證攔截作業系統強制終止或所有 GPU 驅動故障。

## SSE 與首 token 延遲

串流請求先取得生成名額便交還 SSE，不等待 Tokenization／Prefill；名額已滿仍在送出 SSE header 前回傳 HTTP 429。SSE 建立時及之後每 10 秒送出 `: keep-alive` 註解，Chat Completion 另保留標準的 assistant role chunk。保活由 HTTP event loop 排程，不是生成文字或進度百分比，客戶端不可將它當成首個回答 token。

畫面只顯示短問題，不表示完整輸入很短；`messages` 中的 system／developer 指令、歷史與工具結果，以及 `tools` 定義也會進入模型模板。應以日誌的 `prompt_tokens` 與實際 `prefill_step_size` 排查，而非只看最後一句問題或 GPU 使用率。通用診斷步驟見 [API 延遲與取消排查](../docs/MLX-RUNTIME-TROUBLESHOOTING.md)。
