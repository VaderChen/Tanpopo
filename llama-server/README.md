# LlamaLoader llama-server 原始碼

本目錄保存 LlamaLoader 鎖定的精簡版 llama.cpp 原始碼，只保留建置 `llama-server`、GGML、common、HTTP Server、內嵌 UI 載入器與多模態 `mtmd` 所需內容。

- 上游：`ggml-org/llama.cpp`
- 來源 commit：`4e97ac86ebe2c4cb8212d98d2641ad6768810896`
- Runtime 版本：`custom-4e97ac86ebe2`
- Runtime：`llama-server`
- 目標平台：macOS Apple Silicon、Linux x64，以及部署時原生編譯的其他平台
- 支援架構包含：`qwen35`、`qwen35moe`
- 執行時不依賴 Python
- CMake 不讀取 Git，也不自動更新原始碼

上游文件、測試、範例、其他 CLI 工具、Web UI 開發原始碼與既有建置產物均未納入。更新此目錄時，必須同步更新 `llama-runtime/SOURCE_VERSION`、`cmake/build-info.cmake` 與 `cmake/git-vars.cmake` 的版本識別，並確認 `llama-server` target 可完成 CMake 設定及編譯。
