#!/usr/bin/env bash
set -euo pipefail

# 接受未修改的 Halo 原始碼，在暫存副本移植存取控制，不下載、不執行 Git，
# 也不覆蓋目前使用中的一般 llama-server 或既有 AMD 成品。
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd -P)"
if [[ "$#" -ne 2 ]]; then
  echo "用法：bash scripts/build-amd-vulkan-runtime.sh <未修改的 strix-llama.cpp 原始碼> <上游完整 commit SHA>" >&2
  exit 1
fi
if [[ "$(uname -s):$(uname -m)" != "Linux:x86_64" ]]; then
  echo "此建置入口須在 Linux x64 執行；macOS 不會產生 AMD 成品。" >&2
  exit 1
fi
SOURCE_DIR="$(cd "$1" && pwd -P)"
SOURCE_COMMIT="$2"
VARIANT="amd-vulkan"
RUNTIME_SOURCE="halo-box/strix-llama.cpp"
if [[ ! "$SOURCE_COMMIT" =~ ^[a-f0-9]{40}$ ]]; then
  echo "請提供 Halo 原始碼對應的完整 40 位 commit SHA。" >&2
  exit 1
fi
if ! command -v node >/dev/null 2>&1; then
  echo "移植存取控制需要 Node.js。" >&2
  exit 1
fi
OUTPUT_PARENT="$PROJECT_DIR/llama-runtime/variants/$VARIANT"
DESTINATION="$OUTPUT_PARENT/linux-amd64"
if [[ -e "$DESTINATION" || -L "$DESTINATION" ]]; then
  echo "AMD 成品已存在，請先備份並移開：$DESTINATION" >&2
  exit 1
fi
mkdir -p "$OUTPUT_PARENT"
STAGING_DIR="$(mktemp -d "$OUTPUT_PARENT/.build-XXXXXX")"
cleanup() {
  if [[ -n "$STAGING_DIR" && -d "$STAGING_DIR" ]]; then
    rm -rf -- "$STAGING_DIR"
  fi
}
trap cleanup EXIT
node "$PROJECT_DIR/scripts/prepare-amd-runtime-source.mjs" "$SOURCE_DIR" "$STAGING_DIR/source"
bash "$PROJECT_DIR/scripts/build-llama-server-runtime.sh" \
  "$STAGING_DIR/source" "$STAGING_DIR/bundle" "$VARIANT-${SOURCE_COMMIT:0:12}-tanpopo.1"
HELP_OUTPUT="$("$STAGING_DIR/bundle/bin/llama-server" --help 2>&1)"
for flag in --openloader-access-control --load-mode --parallel --device --list-devices; do
  if [[ "$HELP_OUTPUT" != *"$flag"* ]]; then
    echo "成品缺少必要介面：$flag" >&2
    exit 1
  fi
done
cp "$SOURCE_DIR/LICENSE" "$STAGING_DIR/bundle/LICENSE"
printf '{"schema":2,"source":"%s","commit":"%s","platform":"linux-amd64","backend":"vulkan","modes":["vulkan","strix-halo"]}\n' \
  "$RUNTIME_SOURCE" "$SOURCE_COMMIT" > "$STAGING_DIR/bundle/runtime.json"
mv "$STAGING_DIR/bundle" "$DESTINATION"
echo "AMD Runtime 已建立：$DESTINATION"
echo "請重新整理 Tanpopo；只有實際偵測到可用 AMD Vulkan GPU 時才會顯示選項。"
