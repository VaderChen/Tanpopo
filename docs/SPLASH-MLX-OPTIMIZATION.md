# Splash 實作分析與 Tanpopo MLX 最佳化

2026-11-10。本輪參考 [Inco AI Splash](https://github.com/incoai/splash)，固定原始碼 commit `9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2`。對照 Tanpopo 的 `mlx-swift 0.31.6` 與專案內維護的 `mlx-swift-lm 3.31.4`，選擇能沿用既有模型契約的運算與同步最佳化。

## 架構差異與可借用的設計

Splash 可載入 MLX checkpoint，但執行端是自有的 C++／Metal 引擎。其權重載入、運算圖、排程及 KV 管理彼此配合；不是把一個加速套件掛進 MLX 就能得到同樣效果。官方效能表亦有指定模型、硬體、量化與測試條件，不能直接推算 Tanpopo 的加速倍數。依據：[權重載入與核心設計](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/docs/internals.md)、[官方效能條件](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/docs/performance.md)。

| Splash 的機制 | 實作依據 | Tanpopo 的處理方向 |
| --- | --- | --- |
| 在 projection 的後段融合 SwiGLU 與 residual，減少 GPU 派送及中間結果 | [`Linear::addPrefillSwiGlu`、`addPrefillUpWithGate`](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/runtime/ops/Linear.cpp) | 先將一般 FFN 接到既有 `compiledSiluProduct`；保持各投影及權重配置 |
| 將取樣、候選與驗證計算串接於 GPU 運算圖 | [`Sampling::addSelection`](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/runtime/ops/Sampling.cpp)、[`DraftSelector::add`](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/runtime/ops/DraftSelector.cpp) | 移除 DFlash 驗證前不必要的草稿讀回與 residual 正規化的 CPU 判斷；保留接受 prefix、回滾及隨機抽樣順序 |
| 依運算形狀、資料格式與裝置能力選擇 kernel | [`KernelSelection`](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/runtime/ops/KernelSelection.cpp) | 本輪沿用 MLX 的編譯與裝置派送，不加入依模型名稱或機型硬編碼的替換 |
| 有界 prefix／recurrent state 快取及請求排程 | [`Cache`](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/runtime/engine/Cache.cpp)、[`Scheduler`](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/runtime/engine/Scheduler.cpp) | 後續須一併設計 KV 所有權、並行隔離、多模態位置狀態、取消與記憶體預算；本輪未加入跨請求快取 |
| 動態推測驗證額度 | [`VerifyBudget`](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/runtime/model/VerifyBudget.cpp) | 保留現有 block size 和接受規則；不以改變驗證長度取得本輪效能數字 |
| ANE 與 GPU 協作處理 Prefill | [`AneFfn`](https://github.com/incoai/splash/blob/9124ff2ce1e6d19dab6bb0f2aa94b214b1271ce2/runtime/ops/AneFfn.cpp) | 需另處理私有框架、部署相容性與數值驗證，未納入本輪 |

Splash 採 Apache-2.0。本輪參考其設計，沒有複製其 Metal kernel、Python 服務或模型專用權重格式；Tanpopo 仍由 Go Server 呼叫 Swift／MLX Runtime。既有 MLX 原始碼授權與標示保留。

## 本輪套用範圍

融合運算採用專案既有的共用函式，涵蓋相同的 `silu(gate) * up` 運算模式，不依 checkpoint 名稱挑選。一般量化層與分散線性層仍各自產生 gate、up；融合只在兩個輸出之後，未更動分片計畫或 TCP collective 輪數。自訂 activation、不同資料型別轉換與 recurrent gating 不因名稱相近就自動替換。

DFlash 的 residual 分布仍是正值部分的 `p - q` 再正規化；總和為零時回到 Target 分布。正規化改為張量條件選擇，先使用安全分母，避免未採用的分支發生除以零。Greedy 與 sampling 的草稿提案、隨機 key 使用及 cache 回滾規則維持原順序。

## 驗證

使用者於本輪進行中指示「先暫時不做測試，確認語法與邏輯正確即可」，因此後續僅完成 Release 編譯、JavaScript 語法檢查與邏輯審查，沒有啟動真實模型或叢集測試。完整紀錄見 [驗證 JSON](validation/splash-mlx-optimization-2026-11-10.json)。

- 原生 Release 編譯成功，包含 MLXLLM、MLXVLM、MLXLMCommon 與 Server；版本為 `1.5.0-mlxswiftlm-3.31.4-gguf-dflash2-mtp-mmap-fastgguf-cache12-rdma11-splash1`。
- 42 個模型檔案、47 處相同 FFN 算式改用共用融合函式。逐處確認投影順序、輸入、輸出與權重名稱；不同 dtype 轉換、recurrent gate 與其他 activation 保持原本實作。
- DFlash 的 dense／sparse residual、零總和 fallback、亂數 key 使用次序、接受 prefix、processor 更新與 cache trim／rollback 均已對照原流程；仍保留接受 prefix 與輸出 token 所需的 CPU 讀回。
- `node --check scripts/compare-mlx-runtimes.mjs` 與 `git diff --check` 通過。比較腳本已保留供後續使用，但本輪尚未執行其完整 HTTP／SSE 流程。

暫停指示之前已完成 4 項 Swift 測試，0 失敗：融合運算的 27 組 dtype／形狀／布局比較完全相同；DFlash 的 16 組 greedy／sampling、dense／sparse、全接受及不同拒絕位置均維持預期輸出和 KV cache；residual 包含三種 dtype、零總和、稀疏未選中 token 保留與固定 seed 取樣比對。這些是函式與合成 Target 測試，沒有使用真實 DFlash drafter 權重。

同樣在暫停前完成的局部基準如下。環境為 Apple M4 Pro、64 GiB、macOS 27.2、Swift 6.4；張量大小為 `[1, tokens, 8192]`，每次執行 32 個相依乘積，暖機 3 次，交錯先後順序量測 10 次，表列中位數。

| dtype | tokens | 原運算 ms | 融合後 ms | 耗時降低 |
| --- | ---: | ---: | ---: | ---: |
| float32 | 1 | 0.3791 | 0.3440 | 9.2% |
| float32 | 8 | 0.4314 | 0.3611 | 16.3% |
| float32 | 128 | 2.0054 | 1.4737 | 26.5% |
| float16 | 1 | 0.4324 | 0.3574 | 17.4% |
| float16 | 8 | 0.4521 | 0.3818 | 15.6% |
| float16 | 128 | 1.0365 | 0.7988 | 22.9% |
| bfloat16 | 1 | 0.4243 | 0.3599 | 15.2% |
| bfloat16 | 8 | 0.5250 | 0.4362 | 16.9% |
| bfloat16 | 128 | 1.1622 | 0.8748 | 24.7% |

桌面背景負載未受控制，這些數字僅供局部運算參考，**不代表完整模型的加速比例**。真實模型吞吐量、長提示、SSE、多模態、GGUF／Fast GGUF、DFlash／MTP 完整路徑與實體叢集仍待後續驗證；原有報告的通過紀錄不代表本次版本已重測。

## 後續驗證方式

已保留修改前與修改後的隔離 Runtime，位於 `.cache/splash-study-20261110/{baseline,candidate}-runtime`，沒有替換正在使用的 App。`scripts/compare-mlx-runtimes.mjs` 接受設定 JSON 與新的結果目錄，會依 ABBA 順序載入兩個版本，逐筆比對回答、Token 數、結束原因及 SSE；每個版本都先暖機，僅停止自己建立的程序。

設定檔範例（路徑需換成實際位置）：

```json
{
  "baseline": "/path/to/baseline-runtime/bin/mlx-server",
  "candidate": "/path/to/candidate-runtime/bin/mlx-server",
  "samples": 3,
  "models": [{ "name": "模型名稱", "path": "/path/to/model" }]
}
```

恢復測試時才執行：

```sh
node scripts/compare-mlx-runtimes.mjs comparison.json new-results
```

若後續要採用 Splash 更完整的 projection epilogue、prefix cache 或命令圖重播，需先量測目前瓶頸，再處理量化、shape、記憶體所有權、取消及多請求隔離。不能只複製固定模型的 kernel 或讓不同請求共用可變 KV cache。
