# FGGUF 轉換快取格式

## 定位

`.fgguf` 是 Tanpopo 為 `mlx-server` 設計的**內部轉換權重快取容器**。它保存 GGUF 經架構解析、tensor 轉換與 MLX 量化策略處理後的可執行權重，用來避免後續啟動重做相同轉換。

FGGUF 不是 GGUF 規格的延伸，也不是可交給 llama.cpp 或其他 GGUF 工具載入的模型交換格式。副檔名中的 `f` 代表 Tanpopo 的 fast／file-backed cache；所有讀寫端都必須先驗證 magic 與格式版本，不可只靠副檔名判斷。

FGGUF 的容器壓縮是無損的。GGUF 來源到 MLX 權重之間是否重新量化，則由 `auto`、`quality`、`speed`、`speed-passthrough`、group size 與 recurrent promotion 等轉換策略決定，和容器壓縮是兩個獨立階段。

## Fast GGUF 的角色與保證範圍

**快速GGUF模式（Fast GGUF）**是 Tanpopo／mlx-server 的通用 GGUF 最佳化入口，`.fgguf` 則是保存其轉換結果的快取容器；兩者不可混為一談。系統設定以獨立卡片保存快速模式開關與三種策略，不以模型檔名或固定模型清單套用特例：

| 介面策略 | Runtime 參數 | 權重處理 |
| --- | --- | --- |
| 預設 | `speed + group auto + recurrent controls` | auto 固定解析為 Group 64；K-Quant 重新量化為 INT8 |
| Beta 1 | `speed-passthrough + group 32 + recurrent controls` | 可表示的 Q4_K 沿用來源 4-bit sub-block；其他張量 Group 32 |
| Beta 2 | `speed-passthrough + group 64 + recurrent controls` | Q4_K 沿用方式相同；其他張量 Group 64 |

關閉快速模式時使用 `auto + group auto + recurrent off` 的一般轉換。Q4_K 沿用區段固定 32 元素是來源格式的 sub-block 規格，不是模型名稱特例，也不代表整個模型改用 Group 32。`quality` 模式使用 FP32 參考權重，定位是轉換誤差診斷，不是一般速度選項。

這套策略對多數「mlx-server 已能解析其架構與 tensor layout」的 GGUF 都有幫助，通常可降低重複轉換成本，並改善 MLX 路徑的生成速度或記憶體配置；但**不保證**所有 GGUF 都能載入、加速或保持相同精度。下列條件都可能影響結果：

- Runtime 是否實作該模型架構與多模態元件；
- GGUF metadata、Tokenizer、Chat Template 與 tensor 命名是否完整；
- 量化格式、tensor shape、Group 相容性與自訂 checkpoint 修改；
- 模型對低位元權重、KV Cache 量化及 recurrent 投影精度的敏感度。

成功建立 `.fgguf` 只表示轉換結果通過容器與權重契約驗證，不是模型品質認證。正式使用前應以實際提示、精度與速度測試比較 Fast GGUF 開啟／關閉；若輸出異常、速度沒有收益或模型不相容，可關閉 Fast GGUF，或改用 llama-server 直接執行原始 GGUF。

## 位元組配置

目前格式版本為 `1`，檔案依序由固定前置區、JSON 索引保留區與 tensor payload 組成：

| 位移 | 大小 | 內容 |
| --- | ---: | --- |
| `0` | 8 bytes | ASCII magic：`FGGUF001` |
| `8` | 8 bytes | JSON header 長度，`UInt64` little-endian |
| `16` | 可變 | UTF-8 JSON header |
| header 結尾至 `1,048,576` | 可變 | 保留／填零區 |
| `1,048,576` | 可變 | tensor payload；每個 tensor 起點對齊 16 bytes |

固定 1 MiB header 區讓寫入器可先串流輸出 tensor，再回頭原子填入索引，不需要在記憶體或另一份暫存 payload 中保留整個模型。讀取器仍必須使用 header 的 `payloadOffset` 並驗證其值，不可自行假設位移。

## Header 結構

Header 是單一 JSON object：

```json
{
  "formatVersion": 1,
  "cacheKey": "<conversion-cache-key>",
  "payloadOffset": 1048576,
  "metadata": {
    "format": "mlx",
    "tanpopo.gguf_cache_key": "<conversion-cache-key>"
  },
  "tensors": [
    {
      "name": "model.layers.0.self_attn.q_proj.weight",
      "dtype": "U32",
      "shape": [3584, 448],
      "encoding": "raw",
      "offset": 0,
      "storedBytes": 6422528,
      "rawBytes": 6422528
    }
  ]
}
```

`offset` 是相對於 `payloadOffset` 的位移。`storedBytes` 是容器中的實際長度，`rawBytes` 是還原後長度。`dtype` 使用 safetensors 相同的短名稱集合：`BOOL`、`U8`、`U16`、`U32`、`U64`、`I8`、`I16`、`I32`、`I64`、`F16`、`BF16`、`F32`、`F64`、`C64`。

目前支援兩種 `encoding`：

- `raw`：未壓縮，`storedBytes` 必須等於 `rawBytes`。檔案絕對位移需同時符合 dtype 與 16-byte 對齊，啟用 MMap 時可建立 file-backed `MLXArray`。
- `lzfse`：Apple LZFSE 無損壓縮。讀取時先解壓成剛好 `rawBytes` 的連續緩衝區，再建立 `MLXArray`；此 tensor 不走 MMap。

## 混合壓縮策略

量化矩陣通常接近高熵資料，對整個模型做單一壓縮不但節省有限，還會使全部權重失去 MMap，並讓每次啟動都必須完整解壓。FGGUF 因此以 tensor 為決策單位：

1. 小於 4 KiB 或大於 256 MiB 的 tensor 直接保存為 `raw`，避免小檔管理成本或大型暫存緩衝區。
2. 大型 tensor 先各取開頭、中段、結尾最多 128 KiB 作為樣本。
3. 樣本壓縮後必須不超過原大小的 94%，才進行完整 LZFSE 壓縮。
4. 完整壓縮結果必須不超過原大小的 92%，也就是至少節省約 8%，否則丟棄壓縮結果並保存 `raw`。
5. 所有 tensor 依名稱排序寫入，下一個 payload 起點補齊至 16-byte 邊界。

這個策略優先保留主要 INT4／INT8 矩陣的檔案映射能力，只壓縮確實有容量收益的 BF16、scale、bias 或其他低熵資料。門檻屬容器寫入器策略，不是格式必要條件；相容讀取器只需正確實作 header 所宣告的 encoding。

## Shard 與 Manifest

單一 shard 的未壓縮 tensor 總量以 2 GiB 為上限，檔名納入原始 GGUF 檔名與 cache key 前綴，例如：

```text
Qwen3.5-4B-Q4_0.tanpopo-a1b2c3d4e5f6789012345678-00001-of-00002.fgguf
Qwen3.5-4B-Q4_0.tanpopo-a1b2c3d4e5f6789012345678-00002-of-00002.fgguf
```

新快取的 shard 與 `<模型>.<cache-key>.fgguf.json` manifest 直接放在原始 GGUF 的同一目錄。manifest 使用 schema `3`，記錄 cache key、Runtime 版本、來源名稱、來源標準化完整路徑、轉換策略、未壓縮容量、實際儲存容量與 shard 清單。來源完整路徑讓管理介面的「清除快取」可精確定位原始 GGUF；schema `2` 的舊 manifest 只有來源檔名，僅在目前模型清單中檔名唯一時允許配對。

快取先寫入同目錄的隱藏暫存目錄，所有 shard 入位後才發布 manifest。讀取端只承認完整 manifest，因此中斷寫入不會被當成可用快取。若模型目錄不可寫，轉換會明確失敗，不會悄悄改存到其他磁碟。

## 讀取驗證

讀取器採 fail-closed，至少驗證以下條件：

- magic、格式版本、header 長度、cache key 與 `payloadOffset`；
- dtype、shape 計算出的容量必須等於 `rawBytes`；
- encoding 只能是已知值，且 `raw` 的兩種長度必須相同；
- tensor 區段不可重疊、不可溢位或超出實際檔案；
- `raw` tensor 必須符合 MMap 對齊要求；
- LZFSE 解壓結果必須剛好等於 `rawBytes`。

任何檢查失敗都不會嘗試猜測或部分載入。上層會把該快取視為無效，回到重新轉換流程。

## 相容性與生命週期

- 新建立的轉換快取使用 manifest schema `3` 與 `.fgguf` shard，並與原始 GGUF 同目錄儲存。
- 舊 manifest schema `2` 與 `.safetensors` shard 不會被新版 Runtime 載入；管理介面仍可辨識並清除這些舊快取，避免混用不同容器布局。
- 快取鍵仍涵蓋來源檔案內容特徵、Runtime 版本、量化 profile、group size 與 recurrent promotion；不同策略不會誤用彼此的權重。
- 「清除快取」會刪除指定原始 GGUF 對應的所有 `.fgguf` shard 與 manifest，不刪除原始 GGUF、mmproj、Draft 或模型目錄。
- 模型服務正在使用該 GGUF 時，管理 API 會拒絕清除，必須先停止 Runtime。

## 限制

- FGGUF 目前依賴 macOS Compression framework 的 LZFSE，不承諾跨平台交換。
- 壓縮 tensor 需要 eager 解壓並配置連續記憶體；容量下降與啟動成本之間必須由寫入門檻控制。
- 已量化權重通常壓縮率不高，實際節省量依模型、dtype 與轉換策略而異，不應以副檔名推定固定壓縮率。
- FGGUF 不應上傳為 Hugging Face GGUF 模型或以 `.gguf` 名義散布。
