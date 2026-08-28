# llama-server Runtime Bundle

這個目錄只管理 `llama-server`，不會把 llama.cpp 以 CGO 方式連結進 Go 程序。

## 預編譯平台

封裝時預設必須準備：

- `prebuilt/darwin-arm64/bin/llama-server`
- `prebuilt/linux-amd64/bin/llama-server`

每個平台目錄都必須包含 `VERSION`，而且內容必須與本次封裝的 llama.cpp 原始碼版本一致。請在對應的原生作業系統執行：

```bash
./scripts/build-llama-server-runtime.sh \
  /path/to/our-llama.cpp \
  ./llama-runtime/prebuilt/darwin-arm64 \
  b12345-custom.1
```

Linux x64 的成品必須在 Linux x64 執行相同命令產生。建置固定使用靜態 llama/ggml 元件，輸出只保留 `llama-server` 執行檔。

## 非預編譯平台

部署包會包含相同版本的可建置原始碼與這支建置腳本。安裝時若找不到目前平台的預編譯成品，會在目標主機原生建置 `llama-server`；該主機必須具備 C/C++ toolchain 與 CMake。

封裝器不會執行 Git，也不會自行切換版本。版本來源由 `LLAMA_SERVER_VERSION` 指定，或從原始碼的 `cmake/build-info.cmake` 讀取。
