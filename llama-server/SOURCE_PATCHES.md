# Tanpopo 原生引擎修補記錄

更新日期：2026-11-23。

以 `ggml-org/llama.cpp` 的 `d2bceda3de6d61429b885f4f012bc983c6dcfd50` 固定快照為基礎。升級原因是 Inkling 的循環層、相對注意力、Tokenizer、視覺投影與聊天解析依賴新的 GGML／模型介面，無法僅新增一個模型檔。

## 上游修正

- Inkling 文字／視覺與 Mini 架構：[#32217](https://github.com/ggml-org/llama.cpp/pull/32217)、[#32694](https://github.com/ggml-org/llama.cpp/pull/32694)。
- 循環層必須在量化 scale 載入後才建立權重別名：[#32704](https://github.com/ggml-org/llama.cpp/pull/32704)，已包含於固定快照。
- Tokenizer 的 `ignore_merges`：[#32799](https://github.com/ggml-org/llama.cpp/pull/32799)，已包含於固定快照。
- 額外移植 [#32807](https://github.com/ggml-org/llama.cpp/pull/32807)：Inkling 循環層可能在每次迭代選取不同專家，停用不適用的稀疏專家複製捷徑，避免重用不完整 GPU 權重。這是正確性修正，未宣稱 CPU offload 速度已最佳化。

## 保留的本地契約

- `common/arg.cpp`、`common/common.h` 與 `tools/server/openloader-access-control.*`：Tanpopo API 金鑰、IP 白名單及兩者組合的驗證。
- `tools/server/server-http.cpp`：保留上游 Router 子程序的瀏覽器來源檢查，同時接回 Tanpopo 存取驗證。
- `common/speculative.cpp`：外部 MTP 頭借用 Target 權重不代表共用 KV；僅 Gemma Assistant 使用共用快取路徑。採用上游分片載入器。
- `src/models/qwen4exp.cpp`、`src/llama-context.cpp`：保留已加入的外部 shared MTP 頭、Target embedding／輸出頭借用、形狀檢查與全域 HC hidden RMS。其他圖建構與 QSA 快取使用新上游實作，MTP 的 indexer 不再套用舊版純 attention cache。
- 固定來源識別，建置不執行 Git 更新。精簡建置只包含 Server、mtmd、UI 資產介面及可選的 `test-llama-archs`。

檢查更新差異時，應以這份記錄核對本地契約；不能直接覆蓋上游檔案而忽略存取控制或外部 MTP 頭支援。
