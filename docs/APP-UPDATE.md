# 自動下載、安裝與重新啟動

在「系統設定 → 關於」按「更新並重新啟動」，Tanpopo 會依序取得官方更新、下載並驗證、關閉目前程式、安裝新版及重新啟動。下載時顯示百分比與位元組數；其餘階段顯示處理進度。本流程使用 Go 與平台原生工具，不使用 Python。

啟動時與每小時的版本檢查仍只通知有新版本。必須按下更新按鈕才會開始安裝，不會在背景檢查時自行關閉正在使用的程式。本功能自 **1.26.1003 build 0108** 起提供；較舊版本需要先從 [GitHub Releases](https://github.com/VaderChen/Tanpopo/releases/latest) 安裝本版，之後才能使用一鍵更新。

## 共用流程

1. 重新取得編譯時指定 GitHub repository 的最新穩定 Release，比較版本與 build。拒絕同版、降版、draft、prerelease、缺少平台附件或摘要的發布。
2. 選擇精確符合版本與作業系統／架構的 DMG、MSI 或 ZIP；以 HTTPS 下載，限制 GitHub 的下載重新導向，核對完整長度與 SHA-256。下載失敗或內容不符時，原程式保持執行。
3. 在安裝目錄以外啟動獨立更新助手，持有更新鎖並再次核對官方發布資訊與下載檔案。前端不能提交下載網址、命令或安裝路徑。
4. 完成平台驗證與安裝準備後，助手才通知主程序正常關閉原生視窗、模型、叢集、下載工作及其他受管服務。等待原程序結束，最長兩分鐘；未結束就取消切換。
5. 安裝並以原設定路徑及工作目錄啟動新版。新版完成初始化與實際 HTTP health 檢查後，回報 PID、隨機 nonce 及版本，助手才判定成功。大型模型恢復可能較久，啟動確認上限十五分鐘。

重新啟動後沿用既有模型恢復機制。一般瀏覽器會暫時失去連線，完成後重新載入；未勾選「記住我」的管理工作階段可能需要重新登入。TCP Ring 依原有契約恢復探索，仍需重新選取與配對節點。

## 各平台安裝

| 平台 | 安裝方式與復原 |
| --- | --- |
| macOS | 以唯讀方式掛載 DMG，檢查 App 完整簽章、相同 Developer ID 團隊、Bundle ID、版本及 Gatekeeper。更新助手保留完整 App 結構，避免單獨複製已簽章主執行檔造成系統拒絕執行。使用者資料保留於 Application Support；安裝目錄切換前備份，啟動失敗會停止新版並嘗試還原舊版。 |
| Linux | 延用發行 ZIP 的路徑／類型／容量驗證，在暫存目錄先完成隔離 Runtime 建置。正常停止後，保留設定、data 與設定中位於安裝目錄內的資料／模型路徑，再切換目錄與重新啟動。啟動失敗會嘗試還原。 |
| Windows | 以獨立助手啟動原生 MSI，沿用安裝位置，使用被動安裝畫面並禁止安裝器自行重啟作業系統。可能出現系統 UAC 授權。取消或 MSI 失敗時，會嘗試重新啟動現有程式；MSI 本身處理交易回復。MSI 成功但新版健康檢查失敗時會回報失敗，**不宣稱可自動降版還原 MSI**。 |

只有完整發行安裝可啟用自動更新；`go run`、原始碼工作區與缺少套件結構的執行檔會停用按鈕。macOS／Linux 必須具有安裝目錄旁的寫入權限，並有足夠空間放置下載、新版及備份；Linux 安裝目錄內的使用者資料也需要複製空間。Linux 若需新增系統套件或 GPU 權限，仍需先完成相依環境準備。未提供對應平台附件時會顯示失敗原因，不會改抓其他架構。

macOS／Linux 的舊版目錄保留在安裝目錄旁的 `.Tanpopo-backups/`；更新暫存位於 `.tanpopo-update-*`。Windows 助手使用本機資料目錄中的暫存資料夾，安裝完成後保留供查核。更新狀態寫入設定檔旁 `data/automatic-update-status.json`；啟動及安裝訊息可查 `data/app-update.log`，Windows MSI 詳細日誌為 `data/app-update-msi.log`。

## macOS 更新後的區網連線觀察

2026-10-03 的 build 1227 實體測試中，自動更新後管理 HTTP 與 UDP 探索正常，但原生 MLX TCP 連線回報 `No route to host`（error 65）。完整結束 Tanpopo，再從「應用程式」開啟後恢復 TCP 連線。若遇到相同狀況，請先採此步驟再重新配對，不需再次下載更新或重設系統隱私權。

目前更新助手直接執行新版 App 內的主程式，與一般 App 開啟路徑不同；觀察結果尚未確認 macOS 內部原因。既有更新 Smoke 驗證的是套件與程序就緒，未涵蓋更新後原生子程序的區網權限。

## 管理 API

| 方法與路徑 | 用途 |
| --- | --- |
| `GET /api/app-update/status` | 回報可用性、狀態、目標版本及下載位元組數。 |
| `POST /api/app-update/start` | 開始下載與更新，回傳 `202`；必須帶 `X-Tanpopo-Update: 1`。不接受自訂來源或安裝命令。 |
| `POST /api/app-update/upload` | 保留舊版 Linux ZIP 上傳 API 的相容性，仍要求管理登入；一般操作優先使用自動更新按鈕。 |

狀態依序為 `checking`、`downloading`、`verifying`、`preparing`、`stopping`、`installing`、`restarting`、`completed`，錯誤回報 `failed`。開發環境回報 `unavailable`。跨程序鎖阻止同時執行兩個安裝工作；舊上傳路徑與自動更新共用安裝鎖。

有開啟管理登入時沿用 Session 驗證。免登入模式僅允許來源 IP 與管理網址均為 loopback 的本機更新；遠端更新必須先啟用管理登入。帶有 Origin 的請求必須與管理服務同源，自訂標頭阻止一般跨站表單觸發程式關閉。

模型 API 的「啟用 IP 白名單」只控制模型 API 存取，不能取代管理登入，也不會提供 MCP 操作入口。若遠端 Mac 只開啟模型 API 白名單，可直接在該台 Mac 的 Tanpopo 視窗，或以 `http://127.0.0.1:10082` 開啟管理頁面進行更新；使用區網 IP 開啟管理頁面仍屬遠端更新條件。需要從其他電腦更新時，請先在遠端 Mac 啟用管理登入，再以已登入的管理工作階段操作。

## Smoke 驗證

```sh
go test -race ./src/appupdate ./src/api ./src/cmd/llamaloader
node --test tests/app-update.test.cjs
node --check website/assets/app-update.js
node --check website/assets/settings.js
node --check website/assets/common.js
```

上述涵蓋平台附件選擇、來源與摘要驗證、下載截斷／取消、禁止重複更新、API 來源限制、隔離目錄切換、真正子程序的 HTTP health／PID／nonce 確認、啟動失敗還原，以及前端下載進度、斷線等待與重載。

macOS 可另外使用本機正式 DMG 測試實際掛載、簽章、Gatekeeper、完整助手 App 複製；此測試不替換真實安裝，也不啟動正式 App：

```sh
TANPOPO_UPDATE_SMOKE_DMG="/絕對路徑/Tanpopo-版本-build-編號-arm64.dmg" \
  go test ./src/appupdate -run 'TestMac.*Smoke' -v -count=1
```

若要驗證外接磁碟的安裝暫存，可另設 `TANPOPO_UPDATE_SMOKE_WORKSPACE="/外接磁碟上的測試資料夾"`。測試只建立並移除自己產生的子目錄。DMG 使用系統暫存目錄掛載，App 仍複製至指定磁碟，避免磁碟映像服務拒絕外接磁碟上的掛載點。

跨平台建置使用 `-buildvcs=false`，以相容同時有 Git／SVN 中繼資料的工作區。Windows MSI／UAC 與 Linux 發行 ZIP 的完整安裝仍需在對應作業系統驗收；交叉編譯與 macOS 隔離 Smoke 不等同這兩個平台的實機安裝測試。
