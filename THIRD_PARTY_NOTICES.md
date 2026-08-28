# 第三方軟體聲明

OpenLoader 使用或整合多項第三方開放原始碼。這些內容不因 OpenLoader 的 GPL 或商業授權選項而改變其原授權條款。

## llama.cpp / llama-server

`llama-server/` 是鎖定版本的 llama.cpp 原始碼快照，用於建置 `llama-server`。其授權與著作權聲明保留於 [`llama-server/LICENSE`](llama-server/LICENSE)，相關 vendor 元件的授權則保留在該目錄的 `licenses/` 與各元件目錄中。

上游專案：<https://github.com/ggml-org/llama.cpp>

## MLX Server 相依套件

`mlx-server` 透過 Swift Package Manager 取得 MLX Swift、MLX Swift LM、Swift Transformers、SwiftNIO 及其遞移相依套件。本儲存庫不宣稱擁有這些套件的著作權；實際套件與精確版本以 [`mlx-server/Package.swift`](mlx-server/Package.swift) 及 [`mlx-server/Package.resolved`](mlx-server/Package.resolved) 為準，使用與再散布時應遵循各上游專案隨附的授權。

主要上游專案：

- <https://github.com/ml-explore/mlx-swift>
- <https://github.com/ml-explore/mlx-swift-lm>
- <https://github.com/huggingface/swift-transformers>
- <https://github.com/apple/swift-nio>

封裝或散布 Runtime 時，應連同實際納入版本要求的著作權與授權聲明一併提供。
