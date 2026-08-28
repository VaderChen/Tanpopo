# Tanpopo

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

`Tanpopo` 是一個以 Go 實作的本機模型服務管理器；名稱取自日語「蒲公英（たんぽぽ）」，象徵模型把生成的 Token 像種子般向外散發。管理介面提供簡單登入、模型服務管理與暫存式簡易對話；`llama-server` 維持跨平台與 GGUF 高相容性，Apple Silicon 另提供原生 Swift／MLX 的 `mlx-server`，支援文生文與多模態模型。

## 主要功能

- 本機帳號密碼登入；預設為 `root / root`，可在環境設定即時修改或經確認後關閉登入驗證，不建立使用者資料庫。
- llama-server 與 mlx-server Runtime 由部署包自動安裝、解析與版本管理，不需設定執行檔目錄。
- GGUF 模型目錄預設為 `~/services/models`，MLX 模型目錄預設為 `~/services/mlx-models`，兩者都可在環境設定調整。
- 支援 Hugging Face 公開、gated 與 private repository 的 GGUF 單檔或完整 MLX 模型下載。
- 下載工作在背景執行，管理畫面會顯示佇列、位元組數與進度。
- 自動掃描模型目錄及其子目錄內的 `.gguf` 檔案與完整 MLX 模型目錄。
- 執行狀態頁依所選 Runtime 提供支援模型下拉清單，不接受任意模型路徑。
- 執行狀態頁提供預設關閉的 DFlash 開關；不支援的 Target 會禁用開關，勾選時會即時重新掃描並檢查配對 Draft。
- 環境設定提供服務主機目錄瀏覽器，可由 Home、檔案系統或掛載磁碟選擇 GGUF／MLX 模型目錄，不需要作業系統 Automation 權限或外部工具。
- 可建立多組啟動參數並指定 `llama-server` 或 `mlx-server` Runtime，執行時才與選定模型動態組合。
- 內建 256K Context 的一般、KV Cache Q8、KV Cache Q4、強制關閉思考、MTP 與 DFlash 啟動 Profile；Apple Silicon 另有原生 MLX DFlash 1／2 Profile。
- 可啟動、停止並查看目前模型 Runtime 的 PID、URL 與最近 128 KiB 日誌。
- 成功啟動後會保存 Runtime、Profile、模型、mmproj 與 DFlash 選擇；關閉 Tanpopo 再開啟時，不需登入即可先自動恢復模型服務。使用者明確按下停止則不會自動恢復。
- 簡易對話支援 Markdown 與本機數學公式渲染；模型有提供 reasoning 或 `<think>` 區段時，會與最終回答分開顯示，等待生成時提供三點跳動狀態，完成後顯示輸入／輸出 Token 與每秒輸出 Token 數。
- 管理介面支援 `AUTO`、繁體中文、英文、日文與韓文；選擇會保存於本機設定，`AUTO` 依作業系統及瀏覽器語系決定。
- llama-server 與 mlx-server 直接監聽 Profile 指定的 Host／Port，兩個 Runtime 內部使用同一份 Tanpopo 安全策略快照驗證請求，不增加反向代理層。
- 模型 API 可選擇不限制、只使用核發金鑰、只使用 IP 白名單，或同時使用兩種限制。
- 設定保存採原子替換；Hugging Face Token 與模型 API 金鑰不會由設定 API 回傳明文。
- macOS 圖形工作階段會以原生 AppKit／WKWebView 視窗載入管理介面，不啟動外部瀏覽器；可啟用常駐模式，在系統選單列重新顯示視窗或完整結束服務。Linux、SSH 與 headless 工作階段維持 Shell 模式。

## 快速啟動

開發模式需要 Go 1.25 以上、CMake 與 C/C++ 工具鏈；建立 mlx-server 另需 Swift 6／Xcode。部署包會攜帶固定版本的 `llama-server` 與 Apple Silicon `mlx-server` Runtime，不需要另外下載 llama.cpp、MLX Server 或 Python。

```bash
cd /path/to/Tanpopo
./run.command
```

`run.command` 會先檢查目前平台的開發用 Runtime。版本相符時直接沿用；缺少或版本不符時，會由專案內鎖定的原始碼編譯 `llama-server`，Apple Silicon 也會一併編譯 `mlx-server`，完成後才啟動 Go Service。Linux 不會嘗試編譯 Apple Silicon 專用的 MLX Runtime。

在本機 macOS 圖形登入工作階段，服務開始監聽後會自動彈出原生管理視窗，載入目前 Session 對應的登入頁或主畫面，不會呼叫 Safari、Chrome 等外部瀏覽器。環境設定的「常駐」預設關閉；切換開關時會立即獨立保存，開啟後 Tanpopo 會出現在系統選單列，關閉視窗只會隱藏 UI，Go Service 與模型 Runtime 繼續在後台執行。可從選單列重新顯示視窗，或選擇「結束 Tanpopo」完整停止服務。常駐關閉時，關閉視窗仍會正常停止 Go Service。Linux、SSH、無圖形登入工作階段及其他未提供原生 UI 的平台，會維持現有 Shell 前景執行方式。可用環境變數明確覆寫模式：

```bash
TANPOPO_UI=shell ./run.command  # 強制 Shell
TANPOPO_UI=gui ./run.command    # 支援平台強制開啟原生視窗
```

如需強制重編兩個模型 Runtime，可執行：

```bash
TANPOPO_REBUILD_RUNTIMES=1 ./run.command
```

首次啟動會由 `agent.sample.properties` 建立 `agent.properties`。管理服務預設監聽 `0.0.0.0:10082`，本機可使用：

```text
http://127.0.0.1:10082
```

區域網路內其他裝置可使用 `http://<主機區網 IP>:10082` 連線；實際可達範圍取決於主機防火牆與路由設定。

預設登入資料：

```text
帳號：root
密碼：root
```

可在「環境設定」即時修改管理帳號與密碼；保存後會撤銷所有既有 Session，並要求使用新帳密重新登入。登入欄位會優先提示瀏覽器使用英數鍵盤，但不限制帳號密碼字元。「記住我」未勾選時使用瀏覽工作階段 Cookie；勾選後才會依 `session_hours` 建立持久 Cookie。兩種模式都只保存隨機 Session Token，不會把帳號或密碼寫入網站儲存空間。已登入的使用者也可切換登入開關，確認警告後會立即把停用狀態原子寫入 `agent.properties`，不必再輸入目前密碼或按另一個儲存按鈕；原帳密仍會保留供日後重新啟用。登入驗證關閉時，能連線至管理服務的使用者都可直接操作管理功能。

## llama-server Runtime

本專案只管理 `llama-server`，不會把 llama.cpp 函式庫直接連結進 Go 程序。部署包會同時保存自訂 llama.cpp 的固定版本號、可建置原始碼，以及封裝時可取得的下列預編譯 Runtime：

- macOS Apple Silicon（`darwin/arm64`，Metal）
- Linux x64（`linux/amd64`，CPU）

封裝器會自動建立目前主機平台的預編譯 Runtime；其他平台的預編譯檔若不存在則直接略過，不會讓封裝失敗。部署至未附預編譯 Runtime 的平台時，包含 Linux x64 或 Linux ARM64，安裝程式會從包內相同版本的原始碼原生編譯 `llama-server`，該目標主機必須安裝 C/C++ toolchain 與 CMake。預設安裝至：

```text
~/services/llama.cpp/versions/<llama-server-version>/<platform>/bin/llama-server
~/services/llama.cpp/current -> versions/<llama-server-version>/<platform>
```

Runtime 位置不再由管理介面設定。後端會依序從部署包內的預編譯目錄、安裝後的 `current` 版本、開發專案的 Runtime 目錄與系統 PATH 尋找 `llama-server`。一般部署只需執行 `install.sh`，不需要手動填寫路徑。

```text
<部署包>/llama-server/prebuilt/<platform>/bin/llama-server
~/services/llama.cpp/current/bin/llama-server
<開發專案>/llama-runtime/prebuilt/<platform>/bin/llama-server
```

啟動模型時，Tanpopo 會把 Profile、模型與安全策略快照路徑動態組合後，直接讓 llama-server 監聽 Profile 指定的 Host／Port。實際命令概念如下：

```bash
llama-server \
  --model /完整路徑/model.gguf \
  --mmproj /完整路徑/mmproj-model.gguf \
  --host 0.0.0.0 \
  --port 8080 \
  --ctx-size 262144 \
  --n-gpu-layers -1 \
  --openloader-access-control <安全策略快照>
```

對外 Runtime 直接使用 Profile 設定的 Host 與 Port；執行狀態頁會顯示並可複製 OpenAI 相容 Base URL，例如 `http://<主機 IP>:8080/v1`。一般 HTTP、OpenAI 相容 API 與 SSE 回應都由 llama-server 直接處理。

管理介面的「啟動命令」可保存多組參數 Profile。執行狀態頁選定 Profile 與 GGUF 後，Go 後端才會動態組合命令列並直接啟動 `llama-server`，不會產生或執行 `.sh`。Profile 的「額外參數」採每行一個 argument，例如：

```text
--flash-attn=on
--jinja
```

多模態模型可在執行狀態頁另外選擇檔名含 `mmproj` 的 GGUF。後端會在模型目錄內安全解析檔案路徑，並於啟動時加入 `--mmproj <完整路徑>`；純文字模型可保留「不使用 mmproj」。詳細行為請參考 [llama.cpp 多模態說明](https://github.com/ggml-org/llama.cpp/blob/master/docs/multimodal.md)。

內建 Profile 的 Context Size 均為 256K（`262144`）。KV Cache Profile 使用 `--cache-type-k` 與 `--cache-type-v`；「強制關閉思考」同時設定 `--reasoning off`、`--reasoning-budget 0` 與 `--jinja`；MTP 使用主模型內建的 MTP heads，不需要 Draft GGUF。DFlash 由執行狀態頁的獨立開關控制，預設關閉；勾選後才會把配對的 Draft 與 Target 動態組合，llama-server 會加入 `--model-draft` 與 `--spec-type draft-dflash`。若 Draft 不存在，畫面會取消勾選並提示前往「模型下載」；啟動時後端仍會再次驗證路徑與架構。參數格式以 [llama-server 選項](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)與 [llama.cpp speculative decoding 說明](https://github.com/ggml-org/llama.cpp/blob/master/docs/speculative.md)為準。

## mlx-server Runtime

`mlx-server` 是本專案內建的 Apple Silicon 專用執行檔，核心載入方式參考 MangaKitchen，直接使用 MLX Swift：

- 文生文模型：`LLMModelFactory`
- 多模態模型：`VLMModelFactory`
- HTTP Server：SwiftNIO

整個應用不呼叫 Python、`mlx_lm.server`、pip 或虛擬環境；Go 管理服務與 Swift MLX Runtime 都是原生執行檔。部署主機不需要安裝 Python。

啟動時會檢查模型目錄內的 `config.json`，自動判斷文生文或多模態架構，再從同一目錄載入 safetensors、Tokenizer 與 Processor。模型目錄及所有子目錄都會被掃描；只有同時包含 `config.json` 與 safetensors 權重的目錄會出現在清單。

API 提供 OpenAI／llama-server 常用相容端點：

```text
GET  /health
GET  /v1/health
GET  /props
GET  /models
GET  /v1/models
POST /chat/completions
POST /v1/chat/completions
POST /v1/completions
POST /completion
```

`/v1/chat/completions` 支援 OpenAI 格式的文字 content、`image_url` 多模態 content parts，以及原生 `tools`、`tool_choice`、`message.tool_calls` 與工具結果訊息。`stream: true` 會在生成期間逐 Token 輸出 OpenAI 相容 SSE；客戶端關閉串流或取消請求時，SwiftNIO Channel 會立即取消對應的 MLX 生成 Task，不會等待整段回答生成完畢。`/models` 與 `/v1/models` 會回傳相同的目前載入模型，方便不同 Provider 客戶端自動取得正確 Model ID。模型 API 的金鑰與 IP 白名單由 mlx-server 自己在 SwiftNIO 請求入口執行。

內建 MLX Profile 包含一般、KV Cache Q4、強制關閉思考、DFlash 1 Greedy 與 DFlash 2 Sampling。一般 MLX Profile 的 Context Size 預設 256K，由 Go 後端轉成 `--max-kv-size 262144`。常用原生參數包含 `--kv-bits`、`--kv-group-size`、`--kv-scheme`、`--prefill-step-size`、`--thinking` 與 `--no-thinking`。

### 原生 MLX DFlash 1／2

MLX DFlash 是直接整合在 Swift Runtime 與專案內維護的 `mlx-swift-lm 3.31.4` fork，不會呼叫 Python。Target 與 Draft 都放在可設定的 MLX 模型根目錄下；執行狀態頁會依 Target 架構啟用或禁用 DFlash 開關，勾選後才會選取與 Target 配對的 Draft。實際啟動時後端才會解析安全的相對路徑並加入：

```text
--dflash-draft <Draft 模型完整目錄>
--dflash-block-size 5
--temperature 0
```

目前採用可驗證、可精確回退的實作範圍：

- 支援文生文 Qwen3 dense，以及相容的 Qwen3.5 hybrid Target；Draft 可為 `DFlashDraftModel` 或 `DFlash2DraftModel`。Qwen3.5 啟動時會明確使用文字模型路徑，不載入 Vision wrapper。
- DFlash 2 實作 grouped dynamic causal convolution 與 candidate path selector；selector 只保存 top-k sparse proposal probability，避免為每個位置保留完整 vocabulary 分布。
- Greedy 解碼使用精確 token 比對，結果與 Target Greedy 一致；Sampling 使用 exact rejection sampling，支援 temperature、top-p、top-k、min-p 與既有 penalty processor，保留 Target 條件分布。同一 seed 的 DFlash 與一般生成不保證逐 token 相同，因兩者消耗亂數的路徑不同。
- DFlash 2 搭配 Qwen3.5／Qwen3.8 hybrid Target 且使用 Greedy 時，會自動把有效 Block Size 限制為 2，避免 Gated Delta Net 多 token 與逐 token 浮點累積順序造成 argmax 分歧；Sampling 仍使用 Profile 設定的完整 Block Size。
- DFlash 1 Profile 的 Block Size 預設為 5，DFlash 2 Profile 預設為 8，且都不會超過 Draft checkpoint 的訓練值。
- Draft 支援 full attention 與 sliding attention；Qwen3.5 Target 的 Gated Delta Net cache 會先還原驗證前狀態，再只重播已接受的 prefix，attention KV 則移除被拒絕的 suffix。
- Target 僅使用可精確 trim／rollback 的一般 cache，不支援 rotating 或量化 target KV Cache。
- DFlash Profile 雖保留全域 256K Context 選項供介面一致性使用，但啟用 Draft 時不會傳入 `--max-kv-size`；實際可用上下文仍受 Target 模型設定與記憶體限制。
- 多模態 DFlash target 留待後續階段；未啟用 DFlash 時不影響既有多模態功能。

若單筆 API 請求覆寫為不支援的 Target cache 設定，`mlx-server` 會記錄原因並安全回退到同一 Target 的一般生成，不會用近似演算法冒充 lossless speculative decoding。生成日誌會輸出 proposed、accepted 與 acceptance rate，方便比較 DFlash 是否真的帶來效益。技術流程依據 [DFlash 論文](https://arxiv.org/abs/2602.06036)與[官方 MLX 參考實作](https://github.com/z-lab/dflash/blob/main/dflash/model_mlx.py)。

固定版本位於 `mlx-server/Package.swift`；`mlx-swift-lm 3.31.4` 以含 DFlash 擴充的專案內 fork 鎖定，其餘鎖定 `mlx-swift 0.31.6`、`swift-transformers 1.1.9` 與 `swift-nio 2.101.3`。部署時安裝至：

```text
~/services/mlx-server/versions/<mlx-server-version>/darwin-arm64/bin/mlx-server
~/services/mlx-server/current -> versions/<mlx-server-version>/darwin-arm64
```

後端會自動從部署包、上述 `current` 版本或開發專案的 `mlx-runtime/prebuilt/darwin-arm64` 尋找執行檔，並同時驗證相鄰的 MLX Metal Library；環境設定不需要也不提供 Runtime 路徑欄位。

`build.command` 與 `run.command` 會將第一次成功產生的 MLX Metal Library 保存於 `mlx-runtime/metal-cache/darwin-arm64/<fingerprint>/default.metallib`。指紋涵蓋平台、部署版本、Metal SDK／編譯器與全部 kernel 原始碼；相同指紋的後續建置會直接把預編譯檔注入 SwiftPM bundle，不再重編 Metal kernel。`clean.command` 不會刪除此快取；只有指紋改變，或明確設定 `MLX_METALLIB_REBUILD=1` 時才會重建。

## 模型 API 安全性

「環境設定」可獨立控制兩項模型 API 限制：

| 存取金鑰 | IP 白名單 | 行為 |
| --- | --- | --- |
| 關閉 | 關閉 | 不增加額外限制，維持既有 API 相容性 |
| 開啟 | 關閉 | 只驗證核發的模型 API 金鑰 |
| 關閉 | 開啟 | 只驗證實際連線來源 IP |
| 開啟 | 開啟 | 金鑰與 IP 必須同時通過 |

金鑰使用密碼學安全亂數產生並加上 `olk_` 前綴，明文只在核發時顯示一次；設定檔只保存 SHA-256 雜湊。客戶端可使用標準 Bearer Header：

```http
Authorization: Bearer <核發的金鑰>
```

也可使用：

```http
X-OpenLoader-Key: <核發的金鑰>
```

IP 白名單每行一筆，支援完整 IPv4／IPv6、CIDR 與 `*` 萬用字元，例如：

```text
127.0.0.1
192.168.1.*
2001:db8:*
10.0.0.0/8
```

單獨使用 `*` 代表允許所有 IP。llama-server 與 mlx-server 都只依 TCP 連線的實際來源位址判斷，不信任客戶端提供的 `X-Forwarded-For`；若前方另有反向代理，白名單應設定該受信任代理的來源 IP。

Go 管理服務只負責核發金鑰並以原子替換方式更新安全策略快照，不參與模型 API 的資料轉送。兩個 Runtime 在第一次請求載入快照，之後每 10 秒檢查一次更新；請求驗證使用 Runtime 記憶體內的快取，不會逐筆回主系統核對。啟用限制後若快照遺失、損壞或版本不相容，Runtime 會採 fail-closed 並回傳 `503`。策略更新與金鑰撤銷最遲在下一次重新載入週期生效，不需重新載入模型。

## Hugging Face 模型下載

下載表單需要：

- 模型格式／Runtime：GGUF 單檔或完整 MLX 模型目錄。
- Repository：例如 `bartowski/Qwen2.5-7B-Instruct-GGUF`。
- GGUF 檔名：選擇 GGUF 時必填；為 repository 內的完整相對檔名，可包含子目錄。
- Revision：預設為 `main`，也可使用 tag、branch、完整 commit hash 或 `refs/pr/...`。

每個主 GGUF 會在模型根目錄下建立獨立且穩定的群組目錄，目錄名稱由 repository、主 GGUF 檔名與短識別碼組成；自動偵測的 mmproj 與 DFlash Draft 會下載到同一群組。建立工作前，服務會先讀取相同 repository／revision 的檔案清單，自動尋找 `.gguf` 格式的 mmproj 與 DFlash Draft。每種類型最多選擇一個；若有多個候選，會優先採用與主 GGUF 位於相同目錄且檔名 token 最相近的檔案。附屬檔案已存在且未啟用覆寫時會直接略過，不影響主模型下載。DFlash Draft 位於其他 repository 時無法由目前 repository 自動判斷，仍需另行下載。

選擇 MLX 時不需輸入單一檔名。服務會先確認 repository 同時具有 `config.json` 與 safetensors，再將模型權重、Tokenizer、Processor、Chat Template 等 Runtime 檔案保留原相對路徑並下載到 `~/services/mlx-models` 下的獨立目錄。分片模型只有在 index 所列的全部 safetensors 都存在後才會出現在啟動模型清單，避免下載一半時誤啟動。

服務依照 [Hugging Face 官方單檔下載說明](https://huggingface.co/docs/huggingface_hub/en/package_reference/file_download)使用以下 URL 格式：

```text
https://huggingface.co/{owner}/{model}/resolve/{revision}/{filename}
```

若模型需要授權，請先在 Hugging Face 接受模型條款，再於「環境設定」保存 Access Token。Token 只會寫入權限為 `0600` 的 `data/settings.json`，管理 API 僅回傳是否已設定。

下載先寫入同目錄的 `.part-*` 暫存檔，完成後才替換正式模型；同一個目的檔案同時間只允許一個下載工作。啟用覆寫時會先暫存舊檔，成功替換後再移除暫存備份。模型選擇清單會遞迴掃描模型根目錄與所有子目錄，因此新舊目錄結構中的 `.gguf` 都會顯示。

## 設定檔

### `agent.properties`

服務進入點與本機登入資料。網路與檔案路徑設定修改後需重新啟動；管理帳密與登入開關可由環境設定即時套用：

```json
{
  "service_name": "Tanpopo",
  "http_host": "0.0.0.0",
  "http_port": 10082,
  "web_path": "./website",
  "settings_path": "./data/settings.json",
  "startup_commands_path": "./data/startup_commands.json",
  "access_control_path": "./data/access_control.json",
  "runtime_state_path": "./data/runtime_state.json",
  "default_account": "root",
  "default_pwd": "root",
  "disable_authentication": false,
  "session_hours": 24
}
```

### `data/settings.json`

由「環境設定」保存 GGUF／MLX 模型目錄與 Hugging Face 設定，可在下一次啟動模型或下載工作時直接生效。llama-server 與 mlx-server 的位置由應用程式自動解析，不寫入此設定檔。

### `data/startup_commands.json`

由「啟動命令」頁保存多組模型 Runtime 啟動參數。首次建立時會建立 llama-server 的一般、KV Cache Q8、KV Cache Q4、強制關閉思考、MTP、DFlash，以及 mlx-server 的一般、KV Cache Q4、強制關閉思考、DFlash 1／2 Profile；Context 均預設 256K。舊版設定檔升級時也會補上缺少的內建 Profile。修改 Profile 不會影響正在執行的程序，下次啟動時才會套用。

### `data/access_control.json`

保存模型 API 的金鑰開關、IP 白名單、金鑰名稱與 SHA-256 雜湊，檔案權限為 `0600`。金鑰明文不會寫入檔案，也不會由查詢 API 回傳。llama-server 與 mlx-server 只讀取這份快照，不會呼叫管理服務核對單一請求。既有安裝不需手動建立，首次啟動新版服務時會自動產生。

### `data/runtime_state.json`

保存最後選定的 Runtime、模型相對名稱、啟動參數 ID、mmproj 與 DFlash 狀態，不保存本機模型根目錄、Runtime 二進位檔位置或任何金鑰。模型服務成功啟動後會標記為下次需恢復；關閉 Tanpopo 時只停止子程序並保留旗標，因此恢復流程不需要先登入管理介面。使用者在執行狀態頁按下「停止服務」時則會清除恢復旗標。若模型或啟動參數已不存在，自動恢復會停止並記錄可診斷錯誤，避免每次啟動重複失敗。

## REST API

啟用管理介面登入時，除健康檢查、登入與 Session 狀態外，其餘 API 都需要已登入的 Session Cookie；關閉登入驗證時則可直接使用管理 API。

| Method | Path | 用途 |
| --- | --- | --- |
| `GET` | `/api/health` | 服務健康檢查 |
| `POST` | `/api/login` | 使用本機設定帳密登入 |
| `POST` | `/api/logout` | 清除目前 Session |
| `GET/PUT` | `/api/admin-credentials` | 查詢登入狀態或更新管理帳密與登入開關；不回傳密碼明文 |
| `GET/PUT` | `/api/settings` | 讀取或保存模型目錄與 Hugging Face 設定 |
| `GET/PUT` | `/api/access-control` | 讀取或保存模型 API 金鑰／IP 白名單策略 |
| `POST` | `/api/access-control/keys` | 核發模型 API 金鑰；明文只在此回應一次 |
| `DELETE` | `/api/access-control/keys/{id}` | 撤銷模型 API 金鑰 |
| `POST` | `/api/system/directories` | 瀏覽服務主機的可用目錄 |
| `GET` | `/api/models?runtime=...` | 依 Runtime 掃描 GGUF 或 MLX Target 模型；MLX 可加 `role=draft` 取得支援的 DFlash Draft |
| `GET/POST` | `/api/startup-commands` | 查詢或建立啟動參數 Profile |
| `PUT/DELETE` | `/api/startup-commands/{id}` | 修改或刪除啟動參數 Profile |
| `GET/POST` | `/api/downloads` | 查詢或建立下載工作 |
| `GET` | `/api/runtime/status` | 查詢目前模型 Runtime 狀態 |
| `GET/DELETE` | `/api/runtime/logs` | 查詢或清除目前 Runtime 日誌 |
| `POST` | `/api/runtime/start` | 依 Profile 載入模型並啟動 Runtime |
| `POST` | `/api/runtime/stop` | 停止目前模型 Runtime |

## 專案結構

```text
src/cmd/llamaloader/  服務進入點與關閉流程
src/api/              HTTP 路由與網站入口
src/config/           本機 JSON 設定讀寫與驗證
src/accesscontrol/    模型 API 金鑰與 IP 白名單策略快照管理
src/startupcommand/   llama-server 啟動參數 Profile 保存
src/session/          最小化的記憶體登入 Session
src/download/         Hugging Face 背景下載與進度
src/llamacpp/         模型掃描與 llama-server／mlx-server 程序管理
src/domain/           共用資料型別
src/directorybrowser/ 服務主機目錄瀏覽資料
src/desktopui/        GUI 工作階段偵測與原生視窗程序管理
desktop-ui/           macOS AppKit／WKWebView 原生 UI 原始碼
scripts/              llama-server／mlx-server／原生 UI 建置工具
llama-server/         鎖定版本、只供建置 llama-server 的精簡 llama.cpp 原始碼
llama-runtime/        預編譯 Runtime 暫存與封裝規格
mlx-server/            Swift／MLX 相容 API Server 與鎖定的 mlx-swift-lm fork
mlx-runtime/           Apple Silicon 預編譯 Runtime 與持久化 Metal Library 快取
website/              登入與主畫面靜態資源
```

## 授權

Tanpopo 中由專案著作權人擁有的原創程式碼採雙軌授權，適用範圍見 [`LICENSE-NOTICE.md`](LICENSE-NOTICE.md)：

- 開放原始碼：GNU General Public License v3.0 or later（GPL-3.0-or-later），完整條款見 [`LICENSE`](LICENSE)。
- 商業授權：不適合採用 GPL 的使用情境，可另行洽談書面商業授權，詳見 [`COMMERCIAL-LICENSE.md`](COMMERCIAL-LICENSE.md)。

第三方程式碼與相依套件維持各自的授權條款，詳見 [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md)。

## 建置

專案內的 `llama-server/` 是預設且已鎖定版本的原始碼。執行 `build.command` 時，如果目前主機缺少相同版本的 llama-server Runtime，封裝器會自動原生編譯並保存至 `llama-runtime/prebuilt/<platform>`。例如也可以手動執行：

```bash
./scripts/build-llama-server-runtime.sh \
  ./llama-server \
  ./llama-runtime/prebuilt/darwin-arm64 \
  custom-4e97ac86ebe2-openloader.3
```

Linux x64 使用相同命令，但輸出位置改為 `./llama-runtime/prebuilt/linux-amd64`。已有預編譯檔時，其 `VERSION` 必須與內建原始碼版本一致。若封裝主機沒有其他平台的預編譯檔，該平台不會造成封裝失敗；部署包仍會攜帶完整精簡原始碼，並在安裝到該平台時原生編譯。封裝時也可以指定自訂原始碼與鎖定版本：

Apple Silicon 的 `mlx-server` 可獨立建立；`build.sh` 找不到正確版本的預編譯檔時，也會在 Apple Silicon 封裝主機自動執行此步驟：

```bash
./scripts/build-mlx-server-runtime.sh
```

```bash
LLAMA_CPP_SOURCE_DIR=/path/to/our-llama.cpp \
LLAMA_SERVER_VERSION=b12345-custom.1 \
./build.sh

./bin/Tanpopo
```

`build.sh` 只會建置及收集 `llama-server` target，不會執行 Git 或更新 llama.cpp。內建快照目前鎖定官方 commit `4e97ac86ebe2c4cb8212d98d2641ad6768810896`，包含 `qwen35` 與 `qwen35moe` 架構支援。封裝內容會排除 `.git`、文件、測試、範例、既有 build 目錄等開發檔案，但保留建置 `llama-server` 所需的 `ggml`、`common`、`tools/server`、`tools/mtmd`、UI 載入器、CMake 與相依原始碼。依先前約定，部署 ZIP 由維護者需要時自行執行 `build.command` 產生。

未指定 `LLAMA_CPP_SOURCE_DIR` 時，封裝器會直接優先使用專案內的 `llama-server/`；只有該目錄不存在時，才依序檢查專案內的 `llama.cpp`、`~/services/llama.cpp`、`~/llama.cpp`，最後在 `~/Codes` 內尋找唯一的外部原始碼。內建原始碼的版本鎖定位於 `llama-runtime/SOURCE_VERSION`；外部來源可使用 `LLAMA_SERVER_VERSION`、`LLAMA_SERVER_VERSION_FILE` 或來源目錄的 `LLAMA_SERVER_VERSION` 指定，否則會讀取 `cmake/build-info.cmake` 或原始碼 commit 中繼資料。版本檔不可命名為 `VERSION` 放在 llama.cpp 原始碼根目錄，避免 macOS 不分大小寫的檔案系統攔截 C++ 標準標頭 `<version>`。

可先執行 `./build.command --check`，只檢查實際採用的原始碼、版本及 Runtime 策略，不會開始編譯或建立 ZIP。輸出會明確標示哪些平台會在封裝時編譯，以及哪些平台改由部署時原生編譯。

執行檔仍需能讀取工作目錄下的 `agent.sample.properties` 與 `website/`。如由其他目錄啟動，可使用參數指定設定範本：

```bash
./bin/Tanpopo \
  -config /path/to/agent.properties \
  -sample-config /path/to/agent.sample.properties
```
