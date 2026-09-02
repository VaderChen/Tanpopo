# MLX API 延遲與取消排查

本文件說明外部程式直接呼叫模型 API 時，如何區分長輸入處理、串流等待與取消失效。服務行為與多人容量以 [MLX Runtime 規格](MLX-RUNTIME-SPEC.md)為準。

## 先區分三個時間點

1. **建立 SSE**：完成請求格式、模型與生成名額檢查後送出 header 與保活註解。Chat Completion 另有 assistant role chunk，尚不代表模型已產生文字。
2. **首個生成 token**：模型完成必要的輸入處理與 Prefill 後，才有思考／回答文字。等待期間每 10 秒的 `: keep-alive` 是標準 SSE 註解，不是文字內容或進度百分比。
3. **工作結束**：完成、失敗或取消後釋放生成名額與預留資源；不能僅以畫面停止更新判斷。

SSE 建立後發生的準備或生成錯誤，會以 SSE error 及結束標記回報；客戶端需檢查事件中的 error，不能只憑 HTTP 200 判定回答成功。

## 短問題仍然很慢

畫面上的最後一句不等於完整模型輸入。`messages` 可包含 system／developer 指令、對話歷史、工具呼叫與工具結果；`tools` 定義也會進入聊天模板。應先查看 Runtime 日誌的 `prompt_tokens`、`max_tokens` 與 `prefill_step_size`。

記憶體保護會依完整上下文與並行資源預算，必要時縮小當次 Prefill 分段；不截斷輸入，也不修改已保存的設定。分段可以限制暫存記憶體，卻不會消除處理長上下文所需的運算。因此「模型支援 256K 上下文」不等於任何 256K 請求都能快速出字。

若 token 數遠大於畫面顯示的文字，請 API 呼叫端核對實際序列化的 `messages` 與 `tools`，區分各角色內容、歷史、工具結果及工具定義的大小，檢查是否重複附加。統計時不必公開提示全文、憑證或業務資料。Runtime 現有日誌僅有整體 token 數，無法據此判定哪一部分佔比最大。

## 取消是否成功

以同一個 `request_id` 對照下列事件：

- `generation accepted`：已取得生成名額，可能仍在輸入準備。
- `generation started`：已完成 token 數與資源規劃，即將進入模型生成路徑；不代表 Prefill 已完成或首 token 已送出。
- `http request cancelled ... reason=connection_closed`：HTTP 層已察覺客戶端斷線。
- `generation released ... active_requests=...`：該請求的名額與預留帳本已釋放；數字表示整個 Runtime 尚有幾個生成請求。

每個請求有獨立的取消訊號。已提交的 GPU 運算不能強制撤回，後續分段／迭代則會檢查取消。其他使用者仍在生成或其他程式仍使用 GPU 時，整體 GPU 使用率不必降到零。

若已換上新版執行檔，但目前程序是在更換前啟動，需先停止模型服務、再載入並啟動。排查時應先確認執行版本與啟動時間，避免混用新舊程序的觀察結果。

完整驗收項目見 [MLX Runtime 規格](MLX-RUNTIME-SPEC.md#推論驗收項目)；語法與編譯通過不等同於所有推論情境已驗證。
