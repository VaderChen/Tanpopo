# 單一 AMD Runtime 與互斥模式

## 介面與參數

保留標準版與 macOS MLX。AMD 只有一份執行檔及一個選單項目，沿用 `runtime: "llama-server"`、`runtime_variant: "amd-vulkan"`。Vulkan／Strix Halo 是互斥調校模式，不是兩個 Runtime；Strix Halo 模式底層也是 Vulkan。

| 啟動參數 | 選單名稱 |
| --- | --- |
| 標準版（未選 AMD Runtime） | LLaMA Server（標準版） |
| `--tanpopo-amd-mode=vulkan` | LLaMA Server（AMD Vulkan） |
| `--tanpopo-amd-mode=strix-halo` | LLaMA Server（Strix Halo） |
| `--tanpopo-amd-mode=auto` 或省略 | 依偵測結果顯示上述其中一種 AMD 名稱 |

在啟動參數的額外參數欄輸入一行，例如 `--tanpopo-amd-mode=vulkan`。可建立不同參數組合供比較；主頁切換參數、編輯參數與執行中狀態均同步名稱，不新增第二個 AMD 項目。重複指定、未知值、缺少值，以及在標準版／MLX 使用 AMD 參數，均拒絕 API 儲存或啟動。

## 單一來源與切換

成品使用 [halo-box/strix-llama.cpp](https://github.com/halo-box/strix-llama.cpp)，包含 Halo 通用分支功能。管理層參數不傳給 llama-server，而是控制其子程序環境：

- Vulkan 模式：設定 `GGML_VK_MMV_NO_SPLIT=1`，使用未拆分的批次 mat-vec 路徑。
- Strix Halo 模式：移除該環境變數，使用上游拆分路徑。不能設為 `0`，因為上游判斷的是變數是否存在。
- 只修改模型子程序的環境，不修改主程序環境，也不輸出其他環境內容。
- 兩種模式均保留分支的 RDNA 3.5 修正；這不是切換兩份 fork，也不是關閉所有 Strix 分支功能，其餘架構／驅動條件仍由上游程式處理。
- 僅建置 Vulkan，不啟用 HIP／ROCm，不自動開啟推測 prefill。是否更快或適用所有 AMD GPU 尚需實機驗證。

## 偵測規則

新 Runtime 選項出現需同時符合 Linux／Windows x64、有相容成品與合法 manifest、`--help` 介面檢查通過，以及該成品的 `--list-devices` 列出 AMD Vulkan GPU。macOS 或只有 AMD CPU 不提供可用的 AMD Runtime；若已有 AMD 參數，仍保留選項並標示不可用。

自動模式採第一個可用 AMD Vulkan GPU。GPU 名稱含 `gfx1151`、`Strix Halo` 或 Radeon 8040S／8050S／8060S 時選 Strix Halo；其他 AMD GPU 選 Vulkan。不以 CPU 品牌或一般 RDNA 3.5 推定 Strix Halo。明確指定 Strix Halo 時會尋找符合條件的 GPU，找不到便報錯，不強制套用於其他硬體。

每次只選一張 GPU 與一種模式，混合 GPU 主機也不增加 AMD Runtime 項目。探測各有 5 秒逾時、1 MiB 輸出限制與 15 秒介面快取；啟動前重新探測。可用只是基本執行與介面檢查，不是簽章、完整安全或模型效能驗收。

AMD 固定管理 `--device` 與 `--parallel 4`，覆蓋同名額外參數。四個 slot 不代表任意模型／上下文都能放進記憶體。校準包含 Runtime 成品識別與完整啟動參數，不共用不同模式的參數結果。

## 建置與部署

唯一目錄為 `llama-runtime/variants/amd-vulkan/<平台>/`，其下包含 `bin/llama-server`、`LICENSE` 與 `runtime.json`。Windows 使用 `.exe` 並附帶所需 DLL；沒有第二份 `strix-halo` 成品目錄。

manifest 使用 schema 2，來源固定為 `halo-box/strix-llama.cpp`，提供完整 40 位小寫 commit SHA、`platform`（linux-amd64 或 windows-amd64）、`backend: "vulkan"`，以及 `modes: ["vulkan", "strix-halo"]`。舊 schema 1 的 Halo 一般版成品不符合雙模式契約，會被拒絕。

Linux x64 建置：

```bash
bash scripts/build-amd-vulkan-runtime.sh /path/to/unmodified-strix-source FULL_COMMIT_SHA
```

需 Node.js、CMake、編譯器、Vulkan headers／loader 與 glslc。入口在暫存副本移植標準版的 Tanpopo 存取控制（API Key、IP 白名單、政策熱更新），不更動來源或標準版。移植錨點不相容便停止，不跳過安全政策。來源 SHA 由建置者提供，manifest 不是來源簽章證明。

入口不執行 Git、不下載、不安裝系統套件、不清空 dist；既有成品存在時拒絕覆蓋。Windows 須使用相應工具鏈建置同一移植後原始碼並配置相依 DLL。一般 Release 封裝尚不會自動收集此選用目錄，部署時須一併配置。

## 完成程度與驗證界線

已提供單一 Runtime 偵測、互斥參數、選單名稱同步、啟動環境、四個 slot、校準隔離，以及原始碼移植與 Linux 建置入口。開發主機為 macOS，尚未產出 AMD Vulkan 執行檔；沒有相容成品時 AMD 不可啟動；已有的 AMD 參數仍顯示並標示不可用。

本次驗證限定編譯與語法檢查；未執行模型推論、效能或四人實機測試，未重啟服務、封裝或同步 GitHub。

## 不可用狀態與對話中斷

已儲存的啟動參數不因 Runtime 探測失敗而從列表移除；介面保留 Runtime 選擇並顯示不可用原因。參數儲存僅檢查格式與模式合法性，實際啟動與校準仍檢查 Runtime 可用性。

簡易對話在串流中斷時保留已收到的文字與思考內容，標示「回答未完成」，並另行顯示錯誤。未完成的回答不加入後續模型請求的歷史；重新整理仍會清除頁面對話，本次沒有新增紀錄保存功能。
