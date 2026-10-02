# MLX 分散推論與 RDMA 使用指南

狀態：實驗功能；2026-10-02。Runtime 版本尾碼為 `rdma4`。

本功能讓多台 Mac 共同執行同一個模型。正式程式仍是 Go、Swift 與 C++，不需要 Python、pip 或 `mlx_lm.server`。

## 支援範圍

- TCP Ring 支援 2–8 個節點；JACCL RDMA 維持雙節點。Rank 0 提供 API、請求排程、Tokenizer、Attention、KV Cache 與抽樣；其餘 Rank 執行權重分片的線性層運算。
- 後端 `jaccl` 使用 MLX Core 的 Thunderbolt RDMA；`ring` 使用 TCP，兩者共用相同推論協定。Ring 測試成功不代表 RDMA 硬體已驗證。
- 已宣告運算契約的架構為 `LlamaModel`（含使用此實作的 Mistral）、`Qwen2Model`、`Qwen3Model`，輸入必須是完整 safetensors 模型目錄。
- 通用演算法切分 `Linear`／`QuantizedLinear` 的輸出列，量化權重、scales、biases 與偏置使用相同列範圍。列數有餘數時，分片最多相差一列；通訊先補齊長度再移除暫存列，支援三台等無法整除的組合。輸出列數小於節點數的線性層留在主節點。
- 架構透過 `DistributedLinearCompatible` 宣告能力，切分演算法不辨識模型檔名。新增架構前要確認它透過 `Linear.callAsFunction` 使用線性層，沒有直接讀取完整 `weight` 或替換成不同運算語意的子類別。
- 主節點保留四個生成名額與每個請求獨立的取消、KV Cache、Prefill 及記憶體預算。單次遠端線性運算共用鎖，避免不同請求混用 collective 的順序與資料。
- 第一版不支援 GGUF／Fast GGUF、多模態、MoE、自訂 Linear 子類別、DFlash、MTP 或管線平行。遇到不支援的組合會回報錯誤。

此版本採每層輸入廣播與輸出匯集，通訊頻率較高。目的先建立可驗證的權重分攤與原生服務路徑，尚未對大型模型做效能調校，不能承諾雙機加速。

多台記憶體不會形成透明的共用記憶體。KV Cache、Embedding、Norm 及不能切分的部分仍由主節點負擔；可容納模型大小需以各節點實際預算判斷。

## 建置

在專案根目錄執行：

```bash
./scripts/build-mlx-server-runtime.sh
./mlx-runtime/prebuilt/darwin-arm64/bin/mlx-server --distributed-capabilities
```

建置器沿用固定的 `mlx-swift 0.31.6` 與專案 Vendor fork，依序套用版本化的 Metal、分散式後端及分片讀取補丁，不需升級整個 Swift MLX 依賴組合。

macOS SDK 包含 `infiniband/verbs.h` 時，自動編入 JACCL；較舊 SDK 只編入 Ring。建置 JACCL 需要 macOS 26.2 或以上的 SDK。未編入 JACCL 的成品不能用來驗證 RDMA，應更換工具鏈後重新建置。

`jaccl_library_available: true` 僅表示可載入後端，不能證明 RDMA 已啟用、線材正確或另一台已連線。`hardware_verified` 固定為 false，避免把本機探測冒充雙機驗收。

將同一份 Runtime 的**整個 `bin` 目錄**複製到另一台，包括 `mlx-swift_Cmlx.bundle`。兩台模型目錄內容也必須相同，可以位於不同絕對路徑。啟動會核對 Runtime 執行檔與模型內容的 SHA-256，再核對切分計畫。

正式建置以建置腳本為入口。自行執行 `swift test` 前，先執行一次建置腳本，以確保 SwiftPM checkout 已套用補丁。

## 在 Tanpopo Server 一鍵啟動 TCP Ring

此模式由 2–8 台 Tanpopo Server 各自管理一個原生 Runtime，不需要配對金鑰、SSH、Python 或手寫 Ring 設定檔。所有節點必須安裝同一份 `rdma4` 或後續相容 Runtime；`--distributed-capabilities` 必須回報 `ring_available: true`、`managed_parent_stdin: true` 與足夠的 `max_ring_nodes`。

1. 各台在「系統設定」指定 MLX 模型目錄，下載相同版本、相同量化格式的支援模型。根目錄可以不同，模型相對路徑必須一致，例如各台都是 `Qwen3-8B-4bit`。
2. 各台到「執行狀態 → TCP Ring 叢集」，按卡片右側的「搜尋節點」。這會開啟區網探索及節點清單對話框；工作節點可關閉對話框，探索仍保持開啟。
3. 主節點先選擇 `mlx-server`、文字模型與一般啟動參數，再在搜尋對話框勾選 1–7 個節點。本機自動加入，可逐台勾選或全選可用節點；忙碌、Runtime 版本不同與不支援的節點不可選取，失聯節點會移出清單。
4. 按「配對並啟用」。系統先保留所有成員、核對版本與模型設定、協商埠，再啟動各 Rank。原生 Runtime 接著核對完整模型及執行檔 SHA-256，載入分片。
5. 推論就緒後，從主節點進行對話或呼叫 API。任一成員按「停止叢集」或既有「停止服務」，都會清理整組。

主節點沿用選定參數的服務埠、Context、一般推論參數與 KV Cache 量化開關。工作節點只接收模型相對路徑、Context、成員名單與 Ring 端點，不接受遠端指定任意命令、執行檔路徑或額外 CLI 參數。DFlash、MTP、多模態、GGUF 及自訂分散式 CLI 參數不適用；此模式使用 Runtime 預設的非 mmap 載入。

探索設定儲存在管理設定檔同層的 `data/cluster.json`，權限 `0600`，包含穩定節點 ID、探索開關、埠與網路介面，不再需要金鑰。舊版金鑰會忽略，重新儲存設定時移除。重開 Server 會恢復已啟用的探索，但推論需重新選取成員與配對，不會使用舊端點自動恢復。

![TCP Ring 搜尋與複選節點對話框](../images/tcp-ring-discovery.jpg)

圖中為真實介面搭配模擬節點；操作展示不代表實體多機或 RDMA 測試結果。

### 網路與交握

- UDP multicast 位址 `239.255.82.82`，預設埠 `10083`，TTL 1；每兩秒探索，12 秒未更新就移除節點。所有節點探索埠須相同。
- 預設探索可用 IPv4 介面，可在「網路設定」指定 `en0` 等介面並套用。同機測試可用 `lo0`；多網卡環境應選各端共同可達的介面。跨 VLAN、用戶端隔離 Wi-Fi 或封鎖 multicast 的網路不在自動探索範圍。
- 各台管理 HTTP 埠（預設 `10082`）必須互通；跨機時不能只監聽 `127.0.0.1`。Ring 自動配置各端非特權 TCP 埠，請允許 Tanpopo Server 與 mlx-server 區網連入。
- 探索協定升為 version 2，以 hello／challenge 回應核對往返可達性，控制連線只採實際來源 IP、已探索成員與固定管理路徑；時間戳、目的節點與 nonce 防止誤投與重放。新舊探索協定不互通，所有 Server 需一起更新。
- 此模式不使用共享金鑰或 HMAC，HTTP 握手及原生張量通道亦未加密；來源 IP 與 nonce 並非密碼學身分驗證。**僅適用信任的區域網路**；開啟探索即允許該區網已探索的 Server 邀請本機執行支援模型，關閉探索後不再接受邀請。
- 系統時間需相差不超過 30 秒。管理登入未啟用時，探索設定與啟動只接受本機操作；啟用管理登入後才能遠端管理。管理 API 仍有登入與跨來源檢查；節點控制 API 拒絕瀏覽器 Origin 與非 JSON 請求。
- 所有節點共用各自 Manager 的 GPU 保留，防止覆蓋單機推論或另一群組。任一成員啟動失敗會整組回滾；每兩秒更新租約，任一端失聯超過 20 秒開始清理，正常停止預留最多八秒再強制結束。Server 意外退出時，原生程序會透過 stdin EOF 結束。此模式不會啟用 Thunderbolt RDMA。

### API 與同機三 Server Smoke

管理 API：`GET /api/cluster/status`、`PUT /api/cluster/config`、`POST /api/cluster/start`、`POST /api/cluster/stop`。探索設定只需 `enabled`、`discovery_port` 與 `interface`。啟動 JSON 使用 `peer_ids` 陣列（不含本機）、`model`、`startup_command_id` 及可選 `kv_cache_quantization_enabled`；空白、重複、失聯或超出上限的選取會遭拒絕。`POST /api/cluster/control` 是已探索節點間的協定。

```bash
TANPOPO_DISTRIBUTED_SMOKE=1 go test ./tests/distributed \
  -run 'TestTanpopoDiscoveredRingSmoke|TestNativeThreeRankCollectivesSmoke' \
  -count=1 -v -timeout 5m
```

此測試建立三個獨立 Go Server、各自設定／模型目錄與三個 Swift Runtime，使用真實 UDP multicast、免金鑰交握與正式管理 API。驗證單／三節點輸出及 Token 數一致、SSE、保留衝突、整組停止、模型不符回滾、Server 強制結束、父端 EOF、租約回收與重啟探索。另一項原生 Smoke 驗證三份無法平均分割的 FP32／FP16／BF16 一般與 Q4 線性層、四路交錯運算及 collective。

微型 safetensors fixture 由 Go 產生；測試的獨立設定關閉啟動前系統記憶體保留檢查，不改使用者設定。這可驗證同機完整流程；不同實體 Mac 的網路、防火牆、效能及 Thunderbolt RDMA 仍需設備到位後驗收。一般 Profile 的 `launch: "local"`／SSH 自動帶起 worker 仍限定雙節點，三個以上由各 Server 管理，或各 Rank 使用 `manual` 啟動。

## JACCL 設備前置作業

JACCL 需要兩台具備 Thunderbolt 5 的 Apple Silicon Mac，均執行 macOS 26.2 以上，並以 Thunderbolt 5 線直接連接。

每台需由使用者進入 Recovery，於終端執行 `rdma_ctl enable`，重新開機後確認：

```bash
ibv_devices
ibv_devinfo
```

依實際連線找到各自的 `rdma_en*` 裝置，確認連線為 `PORT_ACTIVE`。裝置名稱不保證兩台相同；更換插孔後需要重新核對。

另保留可達的管理網路，用於 SSH 與 JACCL Coordinator。依官方 MLX 流程停用 Thunderbolt Bridge、配置各條 Thunderbolt 連線的網路位址，並避免主機睡眠。程式不會代替使用者變更 Recovery、網路介面或睡眠設定。

Coordinator 與 RDMA 連線應位於受信任的網路。遠端啟動採用既有 SSH 金鑰與已確認的 host key，不停用主機身分驗證，也不儲存 SSH 密碼。

依據：[Apple TN3205](https://developer.apple.com/documentation/technotes/tn3205-low-latency-communication-with-rdma-over-thunderbolt)、[MLX 分散式設定](https://ml-explore.github.io/mlx/build/html/usage/launching_distributed.html)。

## 雙機設定

在主節點建立例如 `/Users/yourname/cluster.json`。以下位址、裝置名稱、帳號及路徑均需依設備調整：

```json
{
  "version": 1,
  "backend": "jaccl",
  "coordinator": "192.168.20.10:5500",
  "operationTimeoutSeconds": 120,
  "startupTimeoutSeconds": 600,
  "nodes": [
    {
      "rdmaDevice": "rdma_en2"
    },
    {
      "rdmaDevice": "rdma_en3",
      "ssh": "yourname@mac-b",
      "runtimePath": "/Users/yourname/tanpopo-runtime/bin/mlx-server",
      "modelPath": "/Users/yourname/services/mlx-models/your-model"
    }
  ]
}
```

- 陣列順序代表 Rank；主節點永遠是第一筆。
- `coordinator` 是主節點可供另一台連入的 IPv4 與連接埠，不接受 `0.0.0.0`、DNS 名稱或 IPv6。通訊連接埠需在 1024–65535。
- 第二筆的 `ssh` 可以是 SSH config 的 Host 別名或 `user@host`。需事先在主節點確認免互動的 SSH 登入可用。
- 填寫 `ssh` 時，Rank 0 自動啟動另一台 Runtime，透過參數傳遞設定；不需要先在另一台建立同路徑的設定檔。
- `launch` 可指定 `ssh`、`local` 或 `manual`。省略時，有 `ssh` 欄位就採 SSH，否則為手動啟動；`local` 的單機模擬方式見下方。
- 手動啟動時，兩台各自準備相同設定內容，並指定 `--distributed-rank 0`／`1`。
- 啟動預設最多等待 600 秒，通訊運算預設最多等待 120 秒。大型模型內容校驗及載入較久時，可調整 `startupTimeoutSeconds`，最大 7200 秒。

先做不需要模型的原生通訊與數值 Smoke：

```bash
/path/to/bin/mlx-server \
  --distributed-config /Users/yourname/cluster.json \
  --distributed-smoke
```

此命令會使用相同 SSH 啟停流程，在兩台執行 collective、不同資料型別、一般／量化線性層、四路交錯運算、健康檢查及停止測試。

通過後啟動模型：

```bash
/path/to/bin/mlx-server \
  --model /Users/yourname/services/mlx-models/your-model \
  --distributed-config /Users/yourname/cluster.json \
  --host 127.0.0.1 --port 8080
```

由 Tanpopo 管理時，在 MLX 啟動參數的「額外參數」加入：

```text
--distributed-config /Users/yourname/cluster.json
```

然後選擇相容的原生 MLX 模型並啟動。管理器控制主節點，主節點管理本機或 SSH worker；受管模式不接受 `--distributed-rank` 等內部參數。

`/health` 與 `/props` 會回報 `distributed` 資訊，包括實際後端、群組大小、分片層數、本地線性層權重位元組數與主節點保留部分。只有模型核對、分片載入及雙節點健康檢查成功後才開放 API。

## 載入、容量與故障處理

權重使用 MLX Core lazy Load 的列範圍讀取：先套用模型結構，再直接讀取本 Rank 的線性層列。即使 safetensors 不符合 mmap 對齊，也不先將整個線性 tensor 複製進記憶體。分散式模式的權重讀取策略優先於一般 mmap 選項；若設定 mmap reserve，仍沿用其本機記憶體保留限制。

Go 啟動前保留系統記憶體的檢查仍有效；完整模型檔案大小不能代表本機分片，故該部分交由 Runtime 在分片配置前驗證。每個節點預留通訊暫存，主節點的請求仍受既有 KV 與 Prefill 記憶體檢查約束。程式不會自動把兩台實體 RAM 相加作為可用預算。

主節點每 10 秒送出健康探測。Worker 等待主節點的期限是 `max(60, operationTimeoutSeconds × 2)` 秒；單次運算另有自己的期限。無法恢復的通訊錯誤或逾時會終止該 Rank，需重新啟動整個服務。本機及 SSH worker 都監控父端 stdin EOF，避免主節點結束後遺留程序。

一般請求取消只停止主節點對應的生成工作，已送出的線性層運算先收尾；worker 不保存請求狀態，所以不會取消其他使用者的 KV Cache。節點或連線故障則影響整個分散式服務，不能保證既有請求不中斷。

## 單機雙節點，直接由 Tanpopo Server 管理

以下設定讓 Tanpopo 的正常啟動路徑自動帶起兩個原生 Runtime，不需要 SSH、Python 或手動開第二個終端：

```json
{
  "version": 1,
  "backend": "ring",
  "operationTimeoutSeconds": 120,
  "startupTimeoutSeconds": 600,
  "nodes": [
    { "ringAddress": "127.0.0.1:5500" },
    { "ringAddress": "127.0.0.1:5501", "launch": "local" }
  ]
}
```

將設定存成絕對路徑的 JSON，在 MLX Profile 額外參數加入 `--distributed-config /絕對路徑/local-cluster.json`，再從 Tanpopo 啟動相容模型。Server 啟動 Rank 0，Rank 0 直接啟動 Rank 1，兩者使用同一份 Runtime 及模型。停止模型或關閉 Server 時會回收兩個程序；關閉 Server 前仍在執行的模型，重開時沿用原 Profile 自動恢復。

`local` 只接受 loopback TCP Ring，不可混用 SSH 或 JACCL。若需要驗證不同目錄，可在第二筆指定絕對 `modelPath`；兩份模型仍會核對內容。兩個程序共用這台 Mac 的實體記憶體與 GPU，適合功能 Smoke，不代表有兩台機器的容量或效能。

## Go 原生完整服務 Smoke

在專案根目錄執行：

```bash
./scripts/build-mlx-server-runtime.sh
TANPOPO_DISTRIBUTED_SMOKE=1 go test ./tests/distributed \
  -run TestTanpopoDistributedSmoke -count=1 -v -timeout 12m
```

測試由 Go 編譯並啟動真正的 Tanpopo Server，以管理 API 建立 Profile，再由 Server 啟動單機或雙節點模型。Go 直接產生微型 safetensors 與 Tokenizer，沒有模型下載、Python、pip 或虛擬環境。指定其他 Runtime 成品時，可設定 `TANPOPO_MLX_SERVER=/絕對路徑/bin/mlx-server`。

所有帳密、API Key、設定、模型與連接埠均在獨立暫存環境建立；不修改現有 `agent.properties` 或 `data`。固定微型資料的 Smoke 沿用預設關閉的 Go 啟動前記憶體壓力檢查，避免其他桌面工作的負載影響流程驗證；Runtime 的分片與請求預算仍生效。系統記憶體保留的檢查另由 `src/llamacpp` 測試驗證。此測試需 macOS Apple Silicon；一般 `go test` 未設定啟用旗標時會略過。

涵蓋的實際流程：

- 管理登入、模型 API Key、Profile 建立、啟動與就緒狀態。
- Llama、Qwen2、Qwen3 的單機／雙節點 Chat 輸出與 Token 數、分段長 Prefill、SSE。
- 經 Go 聊天代理四人受理、一般／SSE 第五人回傳 429、取消隔離及名額回收。
- Runtime 的 401／429 保留原有語意；模型金鑰失敗不會讓網頁誤判管理登入失效。
- 主節點／worker 故障回報、父端 EOF、停止回收、Server 重開自動恢復。
- 缺少節點的啟動期限、模型內容不一致及管理狀態回報。
- 原生 collective、FP32／FP16／BF16、一般／Q4 線性層及四路交錯運算。

真正設備到位後，仍需驗證 JACCL 的數值結果、長時間穩定性、線材中斷／worker 結束後回收，以及大型模型的首 Token 延遲、生成速度、逐節點記憶體與 CPU 使用量。本機 Ring 通過不能替代這些驗收。
