# mlx-swift-lm 上游貢獻準備

本目錄整理 Tanpopo 目前對 `mlx-swift-lm` 的擴充，將產品功能拆成可由官方獨立審查的通用能力。這些文件都是提交草稿，尚未送到 GitHub。

## 建議提交順序

| 順序 | 主題 | 目前狀態 | 理由 |
|---|---|---|---|
| 1 | Sampling distribution 基礎 | 已完成程式與單元測試 | 不綁定 DFlash，可直接補足 speculative sampling 所需的公開能力 |
| 2 | DFlash 架構設計 | Issue 草稿完成 | 先與維護者確認如何整合現有 MTP iterator，避免平行維護第二套 speculative stack |
| 3 | Exact rejection sampling 與 cache transaction | 等待設計方向 | 需要同時處理 dense／sparse draft 分布、LogitProcessor 狀態與 hybrid cache rollback |
| 4 | DFlash 1／2 模型與 Qwen target capture | 等待第 2、3 項介面穩定 | 模型支援應建立在共用介面上，不直接帶入 Tanpopo 專用 Runtime |
| 5 | MMap 權重載入實驗 | Issue 草稿與量測規格完成 | 官方 loader 已有 lazy load 與並行 materialization，必須先用數據證明 mmap 的適用區間 |
| 6 | GGUF 混合 group size loader 經驗 | Issue 草稿完成 | MLX 已支援 per-layer group size；先分享 shape-based loader 修正與合成測試，不誤報成 kernel bug |

## 已完成的第一批程式

- 工作副本：獨立的本機 `mlx-swift-lm` checkout
- 分支：`codex/speculative-sampling-distribution`
- 基準 commit：`37688d2cf7d3906e08c74479c9d9949ce6b81136`
- 修改：
  - 新增 `LogitDistributionSampler`，公開 sampler 實際使用的未正規化 log weights。
  - `TopPSampler` 與 `CategoricalSampler` 共用相同 log-weight 計算進行取樣。
  - `ArgMaxSampler` 不宣告為機率分布 sampler，避免把 greedy 語意誤用於 rejection sampling。
  - 新增 distribution、filter 與 sampler capability 測試。
- 驗證：`SampleTests` 35 項全部通過。

這一批刻意不加入 DFlash 型別。它只建立 lossless speculative decoding 所需的 sampler 基礎，降低 PR 的審查範圍。

## 建議官方採納的部分

1. `LogitDistributionSampler` 與 exact rejection sampling 的共用工具。
2. Draft proposal 的 dense／sparse 機率表示，讓 DFlash 1 與 DFlash 2 共用同一驗證流程。
3. 可提交已接受 prefix 的 cache transaction。一般 KV cache 走 staged commit；GDN／Mamba 等 recurrent cache 走 checkpoint 或重播。
4. Target 中間層特徵的按需輸出。選取 layer 應由 drafter descriptor 宣告，不由 iterator 寫死模型型別。
5. DFlash 1／2 checkpoint 的設定驗證、權重 sanitize 與 factory 註冊。

## 先提供 PoC，不建議直接合併的部分

1. 目前的 `MemoryMappedSafetensors`。它仍需要完整 benchmark，且多個 tensor 映射到同一檔案頁面時，`mincore` 統計必須先去重。
2. Tanpopo 的 MMap 記憶體目標。這是產品層 guardrail，不是 Runtime 的硬性記憶體上限。
3. `TaskLocal` 載入模式、UI switch、服務啟動參數與健康檢查。這些屬於 Tanpopo 的整合策略。
4. 只為特定模型名稱或檔案配置的條件分支。上游介面應依 capability 與 checkpoint metadata 判斷。

## 提交前人工確認

官方規範要求提交者本人讀完並認可 Issue／PR 文案，也要求揭露 AI 協作。請在送出前逐字閱讀：

- [Sampling 基礎 PR 草稿](./PR-SAMPLING-DISTRIBUTION.md)
- [DFlash 設計 Issue 草稿](./ISSUE-DFLASH.md)
- [MMap 實驗 Issue 草稿](./ISSUE-MMAP.md)
- [GGUF 混合 group size Issue 草稿](./ISSUE-GGUF-MIXED-GROUP-SIZES.md)
- [量測規格](./BENCHMARK-PLAN.md)

未經人工確認，不應勾選模板中的認可核取方塊，也不應送出 Issue 或 PR。
