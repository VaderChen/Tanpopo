# mlx-server Runtime

`mlx-server` 是 LlamaLoader 的 Apple Silicon 原生推論服務，模型載入方式參考 MangaKitchen：

- 文生文：`LLMModelFactory`
- 多模態：`VLMModelFactory`
- HTTP：SwiftNIO

服務提供 `/health`、`/v1/models`、`/v1/chat/completions`、`/v1/completions`、`/completion` 與 `/props`。

版本固定於 `mlx-server/Package.swift`。打包後安裝到
`~/services/mlx-server/versions/<版本>/darwin-arm64`，並由 `current` 符號連結指向目前版本。
SwiftPM 產生的 `.bundle` 資源會與 `bin/mlx-server` 放在相同目錄，確保 MLX Metal Library 可由原生執行檔載入。

第一次成功建置的 `default.metallib` 會依平台、部署版本、Metal SDK／編譯器與 kernel 原始碼產生內容指紋，並保存於 `metal-cache/darwin-arm64/<fingerprint>/`。後續 `build.command`、`run.command` 會直接復用相同指紋的成品；`clean.command` 不會移除此快取。只有指紋改變或設定 `MLX_METALLIB_REBUILD=1` 時才會重新編譯。
