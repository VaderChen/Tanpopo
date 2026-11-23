# 磁碟輔助大模型推論

Tanpopo 新增兩個可直接選取的啟動預設，讓模型權重不必全部常駐記憶體。原生 MLX 使用有預算的分塊讀取；llama-server 使用檔案映射、CPU 分頁與 GPU 自動配置。這兩條路徑不具備相同的快取控制能力。

Apple Silicon 的 CPU 與 GPU 共用統一記憶體，不能把「RAM + VRAM」相加當作額外容量。磁碟提供權重的 backing storage；KV Cache、輸出、中間運算及常駐參數仍需記憶體。模型檔案大於 RAM 不再直接等同拒絕啟動，也不代表任何大小的模型必定可執行。

## 使用方式

在「啟動參數」選擇以下預設，再從執行狀態頁載入模型：

| 預設 | 適用來源 | 預設行為 |
| --- | --- | --- |
| MLX 磁碟輔助（1 GiB 快取，8K） | MLX safetensors、已有快取的 GGUF、獨立 Fast GGUF | 1 GiB 權重快取、32 MiB 區塊、128-token Prefill、8K Context |
| GGUF 磁碟輔助（GPU 自動配置，8K） | llama-server 原生支援的 GGUF，包含分卷 GGUF | 強制 mmap、GPU 自動 fit、4 GiB 裝置餘裕、按需張量讀取、單一 slot、8K Context |

預設會隨啟動參數設定檔版本 16 自動新增。從版本 15 升級時保留原有自訂 Context 與額外參數。修改設定只影響下一次啟動。

MLX 預設不需要另外開啟 MMap。磁碟模式會以檔案列讀取取代整個張量的 mmap 包裝；如果同時開啟 MMap，Profile 的記憶體保留目標仍會套用。GGUF 磁碟預設會自動開啟 mmap，執行狀態會回報實際啟用值。

MLX 的「額外參數」可調整下列值，旗標與數值各占一行：

```text
--disk-cache-mb
1024
--disk-tile-mb
32
--prefill-step-size
128
```

`--disk-cache-mb 0` 停用磁碟分塊。快取至少需容納兩個區塊，區塊範圍為 1–512 MiB；數值單位為 MiB。快取過大會擠壓 KV 與運算暫存，啟動時會檢查預算。

直接使用原生 Runtime 的範例：

```sh
mlx-server --model /path/to/model \
  --disk-cache-mb 1024 --disk-tile-mb 32 \
  --context-size 8192 --prefill-step-size 128
```

llama-server Profile 內的 `--tanpopo-disk-offload` 是 Tanpopo 的受管旗標，不可直接傳給原生 llama-server。Manager 會移除此旗標，實際套用 `--load-mode mmap --tensor-read-lazy on --no-warmup --fit on --fit-target 4096`。保留目標可在 Profile 調整；此模式的 GPU Layers 由 fit 決定。`--tensor-read-lazy` 只作用於上游支援的按需張量，例如每層詞嵌入；一般 CPU 權重仍由作業系統管理分頁，沒有 MLX 同等的權重快取上限。

## 原生 MLX 的處理方式

- 使用模型的模組與張量契約判定，不依模型名稱設定例外。支援標準 `Linear`、`QuantizedLinear`、`Embedding`、`QuantizedEmbedding`、`SwitchLinear`、`QuantizedSwitchLinear`。
- 線性層沿輸出列分塊；量化權重、scales 與 biases 同步讀取，保留原來的量化格式。
- Embedding 只讀取被查詢的列，合併重複索引與相鄰列，並還原輸入順序。共用詞嵌入的輸出頭也會走分塊線性運算。
- MoE 依路由選出的專家讀取對應區塊，支援排序／未排序索引及上、下投影的廣播形狀；不因一次選取而載入整組專家。
- LRU 權重快取與下一塊預取共用預算。正在運算的區塊不可逐出；每塊結果完成求值後才釋放引用，避免 lazy graph 留住所有權重。另限制快取筆數，避免大量小型詞嵌入索引造成 metadata 膨脹。
- 檔案偏移使用 64-bit。超過 2 GiB 的來源張量不再透過單一大 buffer 映射；每次只建立區塊，原有普通 MMap 路徑的限制未被放寬。
- Fast GGUF 的 raw 張量保留 lazy 檔案來源；LZFSE 張量使用固定 64 KiB 輸入／輸出緩衝串流解壓至暫存檔，再按列讀取。需要足夠的系統暫存磁碟空間；暫存目錄可能與模型所在外接 SSD 不同。
- 自訂子類別不假定等同標準矩陣運算。不能分塊的參數與其來源運算圖在求值前納入常駐預算；小型 slice 若仍引用巨大來源，也不能繞過預檢。模型融合運算若直接讀取已卸載的完整 weight，會回報不支援，避免靜默全量載入。

這裡的快取上限只約束卸載權重區塊，**不是整個行程的 RAM／VRAM 硬上限**。常駐參數、KV、輸出、Metal 工作區、配置器快取與作業系統頁面快取另計。MLX 仍沿用請求資源預算及記憶體壓力檢查。

## 支援範圍與限制

| 組合 | 狀態 |
| --- | --- |
| MLX safetensors 的標準線性層／Embedding／MoE | 已實作分塊執行，需保留可按列讀取的檔案來源 |
| Fast GGUF raw／LZFSE | 已實作 lazy 讀取／串流解壓 |
| 原始 GGUF + 已存在且相容的 Fast GGUF 快取 | 可使用快取進入磁碟模式 |
| 原始 GGUF 首次轉換 | 尚未提供全流程有界轉換；磁碟模式會明確拒絕。可先在記憶體足夠的環境轉換、改用 safetensors，或以 llama-server 直接執行 GGUF |
| safetensors DFlash／MTP 草稿 | 與 Target 共用載入 hook 及權重快取預算；仍須滿足原有模型相容性規則。尚未驗證完整模型的推測解碼結果 |
| 原始 GGUF／內嵌 GGUF MTP 草稿搭配 MLX 磁碟模式 | 明確拒絕，避免草稿全量轉換 |
| TCP Ring／RDMA + 磁碟分塊 | 目前互斥，啟動前回報；原有叢集模式維持原行為 |
| 自訂融合層、非檔案來源的大型 sanitize 結果 | 依常駐預算或完整權重存取防護拒絕，不宣稱所有架構都已驗證 |

這是新增的原生執行路徑；未使用 Python。主要目標是擴充可載入容量，速度取決於儲存吞吐量、快取命中、模型密集程度與 Prefill 大小。密集模型若每個 token 都要重新讀入大量權重，GPU 利用率低可能是等待 I/O。MoE 可只讀啟用專家，但命中率與實際收益仍需實機測量。

## 觀察與排查

原生 MLX `/health`、`/props` 及其 `/v1/` 別名會新增 `disk_offload`：

- `offloaded_bytes`、`resident_parameter_bytes`、`modules`：卸載與常駐規劃。
- `cache_limit_bytes`、`cache_used_bytes`、`cache_peak_bytes`：權重區塊快取預算與用量。
- `cache_hits`、`cache_misses`、`read_bytes`：累計查詢與讀取量，包含預取造成的讀取。
- `tile_bytes`、`mode`：區塊預算與執行模式。

啟動日誌的 `TANPOPO_DISK_OFFLOAD` 也會列出基本預算。這些數字不等於 SSD 的實體 I/O 計數；作業系統可能從檔案頁面快取供應資料。

若回報沒有 Fast GGUF 快取，不要持續增加 `--disk-cache-mb`：這是來源格式轉換的限制。若回報常駐來源運算圖太大，表示模型的 sanitize 或自訂運算未保留可按列讀取的張量。若回報快取預算不足，先縮小快取／Context／Prefill，保留實際運算空間。

## 驗證

本次只進行編譯、參數／設定遷移檢查與小型合成資料 Smoke，沒有載入真實超大型模型，也沒有宣稱速度改善。Smoke 涵蓋浮點與量化的線性層、詞嵌入及共用輸出頭、MoE 排序／重複路由、快取逐出、完整權重存取防護、LZFSE 串流解壓，以及超過 2 GiB 偏移的稀疏檔案與大型 Embedding 查詢。

檢查紀錄：[disk-offload-smoke-2026-11-12.json](validation/disk-offload-smoke-2026-11-12.json)。原生版本標記為 `disk1`。目前工作目錄已建置，不代表已發布 GitHub Release 或更新正在執行中的 App。
