# Tanpopo 安全政策

## 支援範圍

安全修正以最新正式 Release 與 `main` 分支為優先；舊版可能不再個別回補。回報前請先確認問題可在目前版本重現，並移除模型內容、對話內容、帳號、金鑰、Token、主機名稱、IP、絕對路徑與其他可識別資訊。

## 私密回報

請使用 GitHub Repository 的 Private vulnerability reporting 功能回報尚未公開的弱點。請勿在公開 Issue、Discussion、Pull Request、日誌或螢幕截圖中提供可用的帳號密碼、Access Key、Hugging Face Token、NetPass API Key、簽章憑證或私鑰。

建議回報內容包含：受影響版本、作業系統、可重現步驟、預期與實際結果、風險說明，以及已完成去識別化的最小化證據。收到回報後會先確認影響範圍，再協調修正與揭露時間。

## 本機資料與網路邊界

- `agent.properties` 與 `data/` 只供本機執行使用，不應提交至版本控制。
- Hugging Face Token、模型 API Access Key 與 NetPass API Key 不應出現在 URL、原始碼、README、Release note 或公開日誌。
- 模型 API Access Key 只保存雜湊；可回傳明文的金鑰只在核發當下顯示一次。
- NetPass 反向代理會讓本機管理介面與 API 呼叫可由公共網路存取。啟用前必須開啟管理登入與模型 API Access Key 驗證、閱讀使用政策，並自行確認防火牆、金鑰輪替與存取範圍。
- 公開資訊頁不列出 `127.0.0.1` 等 loopback 管理網址；系統資訊與日誌仍應在分享前人工檢查並去識別化。

## 發行與供應鏈

正式安裝包必須使用平台信任的程式碼簽章並完成相應驗證。簽章憑證、私鑰、密碼、公證認證資料、閉源 NetPassClient 與私有封裝流程均不得加入本公開儲存庫。公開 Release 只提供已驗證的安裝成品與 SHA-256 摘要。
