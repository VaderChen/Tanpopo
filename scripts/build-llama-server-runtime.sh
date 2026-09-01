#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -lt 3 ]]; then
  echo "用法：$0 <llama.cpp 原始碼目錄> <輸出目錄> <版本號> [額外 CMake 參數...]" >&2
  exit 1
fi

SOURCE_DIR="$(cd "$1" && pwd)"
OUTPUT_DIR="$2"
LLAMA_VERSION="$3"
shift 3

if [[ -z "${OUTPUT_DIR//[[:space:]]/}" ]]; then
  echo "llama-server 輸出目錄不可為空" >&2
  exit 1
fi

require_command() {
  local name="$1"
  if ! command -v "${name}" >/dev/null 2>&1; then
    echo "缺少必要工具：${name}" >&2
    exit 1
  fi
}

require_command cmake

if [[ ! -f "${SOURCE_DIR}/CMakeLists.txt" || ! -d "${SOURCE_DIR}/tools/server" ]]; then
  echo "指定位置不是可建置 llama-server 的 llama.cpp 原始碼：${SOURCE_DIR}" >&2
  exit 1
fi
if [[ -z "${LLAMA_VERSION//[[:space:]]/}" ]]; then
  echo "llama-server 版本號不可為空" >&2
  exit 1
fi

case "$(uname -s):$(uname -m)" in
  Darwin:arm64)
    PLATFORM="darwin-arm64"
    BACKEND="metal"
    PLATFORM_ARGS=(-DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON)
    ;;
  Linux:x86_64|Linux:amd64)
    PLATFORM="linux-amd64"
    BACKEND="vulkan"
    PLATFORM_ARGS=(-DGGML_METAL=OFF -DGGML_VULKAN=ON)
    ;;
  Linux:aarch64|Linux:arm64)
    PLATFORM="linux-arm64"
    BACKEND="vulkan"
    PLATFORM_ARGS=(-DGGML_METAL=OFF -DGGML_VULKAN=ON)
    ;;
  *)
    PLATFORM="$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | tr '[:upper:]' '[:lower:]')"
    BACKEND="cpu"
    PLATFORM_ARGS=(-DGGML_METAL=OFF)
    ;;
esac

if [[ "${BACKEND}" == "vulkan" ]]; then
  require_command glslc
  if [[ ! -r /usr/include/vulkan/vulkan.h ]]; then
    echo "缺少 Vulkan headers；請先執行 install-linux-vulkan-dependencies.sh dependencies" >&2
    exit 1
  fi
fi

BUILD_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/llama-server-build.XXXXXX")"
cleanup() {
  if [[ -n "${BUILD_ROOT:-}" && -d "${BUILD_ROOT}" ]]; then
    rm -rf "${BUILD_ROOT}"
  fi
}
trap cleanup EXIT

CPU_COUNT="${LLAMA_BUILD_JOBS:-}"
if [[ -z "${CPU_COUNT}" ]]; then
  if command -v getconf >/dev/null 2>&1; then
    CPU_COUNT="$(getconf _NPROCESSORS_ONLN 2>/dev/null || true)"
  fi
  if [[ -z "${CPU_COUNT}" ]] && command -v sysctl >/dev/null 2>&1; then
    CPU_COUNT="$(sysctl -n hw.logicalcpu 2>/dev/null || true)"
  fi
  CPU_COUNT="${CPU_COUNT:-1}"
fi

echo "建置 llama-server ${LLAMA_VERSION}（${PLATFORM}）..."
cmake -S "${SOURCE_DIR}" -B "${BUILD_ROOT}/build" \
  -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=OFF \
  -DLLAMA_BUILD_APP=OFF \
  -DLLAMA_BUILD_TESTS=OFF \
  -DLLAMA_BUILD_EXAMPLES=OFF \
  -DLLAMA_BUILD_TOOLS=ON \
  -DLLAMA_BUILD_SERVER=ON \
  -DLLAMA_BUILD_UI=OFF \
  -DLLAMA_USE_PREBUILT_UI=OFF \
  -DLLAMA_OPENSSL=OFF \
  -DLLAMA_TOOLS_INSTALL=OFF \
  -DGGML_RPC=OFF \
  "${PLATFORM_ARGS[@]}" \
  "$@"
cmake --build "${BUILD_ROOT}/build" --config Release --target llama-server -j "${CPU_COUNT}"

SERVER_BINARY="${BUILD_ROOT}/build/bin/llama-server"
if [[ ! -f "${SERVER_BINARY}" ]]; then
  echo "建置完成但找不到 llama-server：${SERVER_BINARY}" >&2
  exit 1
fi

mkdir -p "${OUTPUT_DIR}/bin"
cp "${SERVER_BINARY}" "${OUTPUT_DIR}/bin/llama-server"
chmod +x "${OUTPUT_DIR}/bin/llama-server"
printf '%s\n' "${LLAMA_VERSION}" > "${OUTPUT_DIR}/VERSION"
printf '%s\n' "${PLATFORM}" > "${OUTPUT_DIR}/PLATFORM"
printf '%s\n' "${BACKEND}" > "${OUTPUT_DIR}/BACKEND"

echo "llama-server Runtime 已輸出：${OUTPUT_DIR}"
