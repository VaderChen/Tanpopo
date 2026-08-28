# OpenLoader

`OpenLoader` 是一個以 Go 實作的本機模型服務管理器；目前為相容既有部署，服務執行檔名稱仍為 `LlamaLoader`。管理介面提供簡單登入與模型服務管理；`llama-server` 維持跨平台與 GGUF 高相容性，Apple Silicon 另提供原生 Swift／MLX 的 `mlx-server`，支援文生文與多模態模型，兩種 Runtime 共用啟動、停止、狀態與日誌介面。

## 主要功能

- 本機帳號密碼登入；帳號與密碼只定義在 `agent.properties`，不建立使用者資料庫。
- llama-server 與 mlx-server Runtime 由部署包自動安裝、解析與版本管理，不需設定執行檔目錄。
- GGUF 模型目錄預設為 `~/services/models`，MLX 模型目錄預設為 `~/services/mlx-models`，兩者都可在環境設定調整。
- 支援 Hugging Face 公開、gated 與 private repository 的 GGUF 單檔或完整 MLX 模型下載。
- 下載工作在背景執行，管理畫面會顯示佇列、位元組數與進度。
- 自動掃描模型目錄及其子目錄內的 `.gguf` 檔案與完整 MLX 模型目錄。
- 執行狀態頁依所選 Runtime 提供支援模型下拉清單，不接受任意模型路徑。
- 環境設定提供服務主機目錄瀏覽器，可由 Home、檔案系統或掛載磁碟選擇 GGUF／MLX 模型目錄，不需要作業系統 Automation 權限或外部工具。
- 可建立多組啟動參數並指定 `llama-server` 或 `mlx-server` Runtime，執行時才與選定模型動態組合。
- 內建 256K Context 的一般、KV Cache Q8、KV Cache Q4、強制關閉思考、MTP 與 DFlash 啟動 Profile。
- 可啟動、停止並查看目前模型 Runtime 的 PID、URL 與最近 128 KiB 日誌。
- 設定保存採原子替換；Hugging Face Token 不會由設定 API 回傳明文。

## 快速啟動

開發模式需要 Go 1.25 以上、CMake 與 C/C++ 工具鏈；建立 mlx-server 另需 Swift 6／Xcode。部署包會攜帶固定版本的 `llama-server` 與 Apple Silicon `mlx-server` Runtime，不需要另外下載 llama.cpp、MLX Server 或 Python。

```bash
cd /path/to/OpenLoader
./run.command
```

`run.command` 會先檢查目前平台的開發用 Runtime。版本相符時直接沿用；缺少或版本不符時，會由專案內鎖定的原始碼編譯 `llama-server`，Apple Silicon 也會一併編譯 `mlx-server`，完成後才啟動 Go Service。Linux 不會嘗試編譯 Apple Silicon 專用的 MLX Runtime。如需強制重編兩個 Runtime，可執行：

```bash
LLAMA_LOADER_REBUILD_RUNTIMES=1 ./run.command
```

首次啟動會由 `agent.sample.properties` 建立 `agent.properties`。管理服務預設監聽 `0.0.0.0:10082`，本機可使用：

```text
http://127.0.0.1:10082
```

區域網路內其他裝置可使用 `http://<主機區網 IP>:10082` 連線；實際可達範圍取決於主機防火牆與路由設定。

預設登入資料：

```text
帳號：admin
密碼：change-me
```

正式使用前請先停止服務，修改 `agent.properties` 的 `default_account` 與 `default_pwd`，再重新啟動。登入欄位會優先提示瀏覽器使用英數鍵盤，但不限制帳號密碼字元；這兩個欄位只由本機設定檔讀取，修改後需要重新啟動服務。

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

啟動模型時會產生以下概念相同的命令：

```bash
llama-server \
  --model /完整路徑/model.gguf \
  --mmproj /完整路徑/mmproj-model.gguf \
  --host 0.0.0.0 \
  --port 8080 \
  --ctx-size 262144 \
  --n-gpu-layers -1
```

管理介面的「啟動命令」可保存多組參數 Profile。執行狀態頁選定 Profile 與 GGUF 後，Go 後端才會動態組合命令列並直接啟動 `llama-server`，不會產生或執行 `.sh`。Profile 的「額外參數」採每行一個 argument，例如：

```text
--flash-attn=on
--jinja
```

多模態模型可在執行狀態頁另外選擇檔名含 `mmproj` 的 GGUF。後端會在模型目錄內安全解析檔案路徑，並於啟動時加入 `--mmproj <完整路徑>`；純文字模型可保留「不使用 mmproj」。詳細行為請參考 [llama.cpp 多模態說明](https://github.com/ggml-org/llama.cpp/blob/master/docs/multimodal.md)。

內建 Profile 的 Context Size 均為 256K（`262144`）。KV Cache Profile 使用 `--cache-type-k` 與 `--cache-type-v`；「強制關閉思考」同時設定 `--reasoning off`、`--reasoning-budget 0` 與 `--jinja`；MTP 使用主模型內建的 MTP heads，不需要 Draft GGUF；DFlash 則必須在 Profile 的「Draft GGUF」欄位選擇與主模型相容的草稿模型，啟動時後端會自動加入 `--model-draft`。參數格式以 [llama-server 選項](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)與 [llama.cpp speculative decoding 說明](https://github.com/ggml-org/llama.cpp/blob/master/docs/speculative.md)為準。

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
GET  /props
GET  /v1/models
POST /v1/chat/completions
POST /v1/completions
POST /completion
```

`/v1/chat/completions` 支援 OpenAI 格式的文字 content，以及 `image_url` 多模態 content parts。`stream: true` 會回傳合法 SSE 相容回應；目前生成完成後一次送出內容，不保證逐 Token 即時傳輸。此服務預設不含身分驗證，應只在受信任網路使用。

內建 MLX Profile 包含一般、KV Cache Q4 與強制關閉思考；Context Size 同樣預設 256K，由 Go 後端轉成 `--max-kv-size 262144`。常用原生參數包含 `--kv-bits`、`--kv-group-size`、`--kv-scheme`、`--prefill-step-size`、`--thinking` 與 `--no-thinking`。

固定版本位於 `mlx-server/Package.swift`，目前鎖定 `mlx-swift-lm 3.31.4`、`mlx-swift 0.31.6`、`swift-transformers 1.1.9` 與 `swift-nio 2.101.3`。部署時安裝至：

```text
~/services/mlx-server/versions/<mlx-server-version>/darwin-arm64/bin/mlx-server
~/services/mlx-server/current -> versions/<mlx-server-version>/darwin-arm64
```

後端會自動從部署包、上述 `current` 版本或開發專案的 `mlx-runtime/prebuilt/darwin-arm64` 尋找執行檔，並同時驗證相鄰的 MLX Metal Library；環境設定不需要也不提供 Runtime 路徑欄位。

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

服務進入點與本機登入資料，修改後需重新啟動：

```json
{
  "service_name": "Llama Loader",
  "http_host": "0.0.0.0",
  "http_port": 10082,
  "web_path": "./website",
  "settings_path": "./data/settings.json",
  "default_account": "admin",
  "default_pwd": "change-me",
  "session_hours": 24
}
```

### `data/settings.json`

由「環境設定」保存 GGUF／MLX 模型目錄與 Hugging Face 設定，可在下一次啟動模型或下載工作時直接生效。llama-server 與 mlx-server 的位置由應用程式自動解析，不寫入此設定檔。

### `data/startup_commands.json`

由「啟動命令」頁保存多組模型 Runtime 啟動參數。首次建立時會建立 llama-server 的一般、KV Cache Q8、KV Cache Q4、強制關閉思考、MTP、DFlash，以及 mlx-server 的一般、KV Cache Q4、強制關閉思考 Profile；Context 均預設 256K。舊版設定檔升級時也會補上缺少的內建 Profile。修改 Profile 不會影響正在執行的程序，下次啟動時才會套用。

## REST API

除健康檢查、登入與 Session 狀態外，其餘 API 都需要已登入的 Session Cookie。

| Method | Path | 用途 |
| --- | --- | --- |
| `GET` | `/api/health` | 服務健康檢查 |
| `POST` | `/api/login` | 使用本機設定帳密登入 |
| `POST` | `/api/logout` | 清除目前 Session |
| `GET/PUT` | `/api/settings` | 讀取或保存模型目錄與 Hugging Face 設定 |
| `POST` | `/api/system/directories` | 瀏覽服務主機的可用目錄 |
| `GET` | `/api/models?runtime=...` | 依 Runtime 掃描 GGUF 或 MLX 模型 |
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
src/startupcommand/   llama-server 啟動參數 Profile 保存
src/session/          最小化的記憶體登入 Session
src/download/         Hugging Face 背景下載與進度
src/llamacpp/         模型掃描與 llama-server／mlx-server 程序管理
src/domain/           共用資料型別
src/directorybrowser/ 服務主機目錄瀏覽資料
scripts/              llama-server／mlx-server 原生建置工具
llama-server/         鎖定版本、只供建置 llama-server 的精簡 llama.cpp 原始碼
llama-runtime/        預編譯 Runtime 暫存與封裝規格
mlx-server/            Swift／MLX 相容 API Server 原始碼
mlx-runtime/           Apple Silicon 預編譯 Runtime 暫存與封裝規格
website/              登入與主畫面靜態資源
```

## 授權

OpenLoader 中由專案著作權人擁有的原創程式碼採雙軌授權，適用範圍見 [`LICENSE-NOTICE.md`](LICENSE-NOTICE.md)：

- 開放原始碼：GNU General Public License v3.0 or later（GPL-3.0-or-later），完整條款見 [`LICENSE`](LICENSE)。
- 商業授權：不適合採用 GPL 的使用情境，可另行洽談書面商業授權，詳見 [`COMMERCIAL-LICENSE.md`](COMMERCIAL-LICENSE.md)。

第三方程式碼與相依套件維持各自的授權條款，詳見 [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md)。

## 建置

專案內的 `llama-server/` 是預設且已鎖定版本的原始碼。執行 `build.command` 時，如果目前主機缺少相同版本的 llama-server Runtime，封裝器會自動原生編譯並保存至 `llama-runtime/prebuilt/<platform>`。例如也可以手動執行：

```bash
./scripts/build-llama-server-runtime.sh \
  ./llama-server \
  ./llama-runtime/prebuilt/darwin-arm64 \
  custom-4e97ac86ebe2
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

./bin/LlamaLoader
```

`build.sh` 只會建置及收集 `llama-server` target，不會執行 Git 或更新 llama.cpp。內建快照目前鎖定官方 commit `4e97ac86ebe2c4cb8212d98d2641ad6768810896`，包含 `qwen35` 與 `qwen35moe` 架構支援。封裝內容會排除 `.git`、文件、測試、範例、既有 build 目錄等開發檔案，但保留建置 `llama-server` 所需的 `ggml`、`common`、`tools/server`、`tools/mtmd`、UI 載入器、CMake 與相依原始碼。依先前約定，部署 ZIP 由維護者需要時自行執行 `build.command` 產生。

未指定 `LLAMA_CPP_SOURCE_DIR` 時，封裝器會直接優先使用專案內的 `llama-server/`；只有該目錄不存在時，才依序檢查專案內的 `llama.cpp`、`~/services/llama.cpp`、`~/llama.cpp`，最後在 `~/Codes` 內尋找唯一的外部原始碼。內建原始碼的版本鎖定位於 `llama-runtime/SOURCE_VERSION`；外部來源可使用 `LLAMA_SERVER_VERSION`、`LLAMA_SERVER_VERSION_FILE` 或來源目錄的 `LLAMA_SERVER_VERSION` 指定，否則會讀取 `cmake/build-info.cmake` 或原始碼 commit 中繼資料。版本檔不可命名為 `VERSION` 放在 llama.cpp 原始碼根目錄，避免 macOS 不分大小寫的檔案系統攔截 C++ 標準標頭 `<version>`。

可先執行 `./build.command --check`，只檢查實際採用的原始碼、版本及 Runtime 策略，不會開始編譯或建立 ZIP。輸出會明確標示哪些平台會在封裝時編譯，以及哪些平台改由部署時原生編譯。

執行檔仍需能讀取工作目錄下的 `agent.sample.properties` 與 `website/`。如由其他目錄啟動，可使用參數指定設定範本：

```bash
./bin/LlamaLoader \
  -config /path/to/agent.properties \
  -sample-config /path/to/agent.sample.properties
```
