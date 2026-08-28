# mlx-server Runtime

`mlx-server` 是 LlamaLoader 的 Apple Silicon 原生推論服務，模型載入方式參考 MangaKitchen：

- 文生文：`LLMModelFactory`
- 多模態：`VLMModelFactory`
- HTTP：SwiftNIO

服務提供 `/health`、`/v1/models`、`/v1/chat/completions`、`/v1/completions`、`/completion` 與 `/props`。

版本固定於 `mlx-server/Package.swift`。打包後安裝到
`~/services/mlx-server/versions/<版本>/darwin-arm64`，並由 `current` 符號連結指向目前版本。
SwiftPM 產生的 `.bundle` 資源會與 `bin/mlx-server` 放在相同目錄，確保 MLX Metal Library 可由原生執行檔載入。
