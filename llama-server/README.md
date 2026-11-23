# LlamaLoader llama-server 原始碼

本目錄保存 LlamaLoader 鎖定的精簡版 llama.cpp 原始碼，只保留建置 `llama-server`、GGML、common、HTTP Server、內嵌 UI 載入器與多模態 `mtmd` 所需內容。

- 上游：`ggml-org/llama.cpp`
- 來源 commit：`d2bceda3de6d61429b885f4f012bc983c6dcfd50`
- Runtime 版本：`custom-d2bceda3de6d-openloader.3-qwen4mtp2-inkling2mini1`
- Runtime：`llama-server`
- 目標平台：macOS Apple Silicon、Linux x64，以及部署時原生編譯的其他平台
- 支援架構包含：`qwen35`、`qwen35moe`、`qwen4exp`、`inkling2`、`inkling2_loop`
- 執行時不依賴 Python
- CMake 不讀取 Git，也不自動更新原始碼

保留上游架構 Smoke 測試與 Server 的直接相依元件，未納入完整上游開發工具或 Web UI 開發原始碼。UI 僅保留產生空資產表所需的 CMake 與 C++ 樣板，建置時使用 `LLAMA_BUILD_UI=OFF`、`LLAMA_USE_PREBUILT_UI=OFF`。更新此目錄時，必須同步更新 `llama-runtime/SOURCE_VERSION`、`cmake/build-info.cmake`、`cmake/git-vars.cmake` 與 GGML 的版本識別，並確認 `llama-server` target 可完成 CMake 設定及編譯。

本地修補記錄見 [SOURCE_PATCHES.md](SOURCE_PATCHES.md)；模型使用方式見 [Inkling-2-Mini 指南](../docs/INKLING2-MINI.md)。
