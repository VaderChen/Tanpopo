# README 動態操作展示

展示直接載入專案的 `website/`，操作真實表單、叢集對話框與 SSE 介面，再由 FFmpeg 編碼為循環 GIF。隔離的 Node.js 示範服務只監聽 localhost，不啟動 Go／Swift Runtime、不建立 UDP 叢集、不下載模型，也不讀寫使用者設定。

畫面持續標示「DEMO · 模擬資料 · 流程已加速」。節點、模型大小、PID、時間、資源使用率與回答都是模擬資料；不代表模型相容性、網路速度、推論效能或實體 RDMA 驗證。對話不加入模擬的 tokens/sec。

## 展示流程

1. 選擇 Qwen3 safetensors 模型與一般 MLX 啟動參數。
2. 從 TCP Ring 卡片按「搜尋節點」，顯示可用及忙碌節點。
3. 勾選兩個遠端節點，本機自動加入；按「配對並啟用」，顯示交握、載入與三節點就緒狀態。
4. 從主節點的簡易對話頁輸入問題，展示思考過程及 Markdown 回答的 SSE 串流。

`fixtures.json` 集中管理示範資料，`server.cjs` 在記憶體中實作 API。`stage.html` 的章節提示與游標只屬於錄製外框；章節依實際點擊及頁面切換更新。示範服務另有 `/demo-status` 診斷資料與頁面錯誤收集器，皆不會加入正式管理服務。

## 瀏覽及重製

手動操作只需 Node.js：

```bash
node scripts/readme-demo/server.cjs
```

終端會顯示隨機 localhost URL。可用瀏覽器錄製；README 採 1280 × 900 畫面、10 FPS、循環播放。

自動錄製器另需 Playwright Chromium、`ffmpeg` 與 `ffprobe`。若已有工具環境可直接沿用；否則可把錄製依賴裝在專案外：

```bash
DEMO_TOOLS="$(mktemp -d)"
npm install --prefix "$DEMO_TOOLS" --no-save playwright
"$DEMO_TOOLS/node_modules/.bin/playwright" install chromium
export NODE_PATH="$DEMO_TOOLS/node_modules"

node --check scripts/readme-demo/server.cjs
node --check scripts/readme-demo/record.cjs
node scripts/readme-demo/record.cjs --output /tmp/tanpopo-demo-review.gif
```

`--preview` 只擷取首張；省略 `--output` 則寫入 `images/tanpopo-demo.gif`。覆寫前先建立 `.bak`，驗收通過後再移除。自動錄製器只允許連線到自身的示範服務，遇到外部請求、未實作 API 或 JavaScript 錯誤會失敗。它使用兩階段調色盤編碼，保存八張場景截圖、原始影格與診斷 JSON 至系統暫存目錄；只有驗收後的展示圖需要加入 Git。

## 驗收

- 以真實 UI 驗證模型選擇、搜尋、勾選兩個節點、一般單機忙碌節點可選取，正式 Server 會在配對時切換服務、三個成員就緒，以及完整串流回答。
- 確認沒有水平溢出，章節與 DEMO 標記可讀，對話框及主要按鈕沒有被裁切。
- 確認示範服務的 `errors` 與 `browserErrors` 為空；使用自動錄製器時亦檢查瀏覽器錯誤及外部請求清單。
- 用 `ffprobe` 核對尺寸、影格數與檔案大小（小於 8 MiB），並從編碼後的 GIF 抽查開頭、複選、交握、就緒與回答畫面。
- 確認所有 README 圖片相對路徑可解析，並執行 `git diff --check`。

2026-10-02 成品以 Codex 內建瀏覽器操作真實介面並擷取影格，再用 FFmpeg 編碼；工具呼叫間的等待已移除。GIF 為 **30.4 秒、304 個影格、1280 × 900、1,556,425 bytes（約 1.48 MiB）**。已驗證八個場景、示範 API／頁面錯誤為空、完整串流回答與編碼後影格。`images/tcp-ring-discovery.jpg` 為同次錄製的複選畫面，供分散推論指南使用。
