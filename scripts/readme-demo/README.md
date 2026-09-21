# README 動態操作展示

這個工具直接載入專案的 `website/`，以 Playwright 操作真實按鈕、表單與串流介面，再由 FFmpeg 編碼為循環 GIF。它沒有修改正式前端，也不會啟動 Go 管理服務、載入模型、下載權重或讀寫使用者設定。

畫面持續標示「DEMO · 模擬資料 · 流程已加速」。模型大小、PID、時間、資源使用率、下載進度與回答皆為展示資料，不代表真實模型相容性、載入時間、網路速度或推論效能。對話不加入模擬的 tokens/sec 數字。常用模型清單直接讀取專案現有的 `website/assets/popular-models.json`。

## 展示流程

1. 選擇 MLX 可載入的 GGUF，查看進階設定、啟用 KV Cache Q8，再啟動示範 Runtime。
2. 在簡易對話頁輸入問題，展示思考區域與 Markdown 回答的 SSE 串流。
3. 從常用模型清單填入下載資訊，展示下載進度及完成後的已下載模型清單。

所有 API 回應由 `server.cjs` 在記憶體中提供；`fixtures.json` 集中管理示範資料。`stage.html` 的章節提示與滑鼠游標只屬於錄製外框，不會加入產品網頁。錄製器只允許連線到自己建立的 localhost 服務，遇到外部請求、瀏覽器錯誤或未實作的 API 就驗證失敗。

## 重製 GIF

需要 Node.js、Playwright Chromium，以及 PATH 中的 `ffmpeg`、`ffprobe`。可沿用既有工具環境；若尚未安裝 Playwright，可把錄製依賴裝在專案外：

```bash
DEMO_TOOLS="$(mktemp -d)"
npm install --prefix "$DEMO_TOOLS" --no-save playwright
"$DEMO_TOOLS/node_modules/.bin/playwright" install chromium
export NODE_PATH="$DEMO_TOOLS/node_modules"
```

於專案根目錄執行：

```bash
node --check scripts/readme-demo/server.cjs
node --check scripts/readme-demo/record.cjs
node scripts/readme-demo/record.cjs
```

輸出為 `images/tanpopo-demo.gif`，尺寸 1280 × 900、10 FPS、持續循環。錄製器使用兩階段調色盤編碼，避免一次把整段未壓縮影格留在記憶體。每張影格使用實際擷取間隔，以保留操作節奏；總長度會因電腦速度與前端輪詢時機略有不同。

錄製包含流程 Smoke：模型啟動與 Q8 狀態、完整串流回答、常用模型資料自動填入、下載工作出現與結束、模型清單新增、各場景無水平溢出、無瀏覽器錯誤及無外部請求。成功後才把 GIF 複製到目的路徑；檔案不得超過 8 MiB。

```bash
# 只檢查首張畫面；暫存路徑會顯示在終端。
node scripts/readme-demo/record.cjs --preview

# 先生成待驗收版本，不覆寫 README 使用中的檔案。
node scripts/readme-demo/record.cjs --output /tmp/tanpopo-demo-review.gif

# 手動瀏覽隔離示範介面；終端會顯示隨機 localhost URL。
node scripts/readme-demo/server.cjs
```

錄製過程會在系統暫存目錄保留九張場景截圖、原始影格、GIF 及 `verification.json`／`diagnostics.json`，方便目視檢查與排錯。這些錄製產物中，只有已驗收的 GIF 需要加入版本控制，暫存目錄可在驗收後自行清理。若要修改既有 README 或替換 GIF，先建立 `.bak`，完成下列驗收後才移除備份。

## 驗收

- 實際開啟 GIF，確認首張不是空白、三段流程都有動作、中文清楚且內容沒有被裁切。
- 檢查九張場景截圖，尤其是進階設定、串流回答、模型選單及下載完成清單。
- 確認 `verification.json` 的 `errors` 與 `blocked` 都為空。
- 確認 README 的相對圖片路徑可以解析到 GIF，並執行 `git diff --check`。

本次錄製使用 macOS arm64、Playwright 1.62.1 與 Chromium；其他平台的字型及排版可能略有差異。

2026-09-21 成品驗收：36.1 秒、361 個影格、1,059,937 bytes（約 1.01 MiB）、無限循環。九個場景、JavaScript 語法、README 圖片連結與 `git diff --check` 均通過；已抽查編碼後各階段影格。中英文 README 僅替換原圖片路徑，既有內容保留。
