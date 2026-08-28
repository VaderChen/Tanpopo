#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LLAMA_SOURCE_DIR="${PROJECT_DIR}/llama-server"
LLAMA_VERSION_FILE="${LLAMA_SERVER_VERSION_FILE:-${PROJECT_DIR}/llama-runtime/SOURCE_VERSION}"
LLAMA_BUILD_SCRIPT="${PROJECT_DIR}/scripts/build-llama-server-runtime.sh"
LLAMA_RUNTIME_ROOT="${LLAMA_SERVER_PREBUILT_DIR:-${PROJECT_DIR}/llama-runtime/prebuilt}"
MLX_SOURCE_DIR="${PROJECT_DIR}/mlx-server"
MLX_BUILD_SCRIPT="${PROJECT_DIR}/scripts/build-mlx-server-runtime.sh"
MLX_RUNTIME_ROOT="${MLX_SERVER_PREBUILT_DIR:-${PROJECT_DIR}/mlx-runtime/prebuilt}"
FORCE_BUILD="${LLAMA_LOADER_REBUILD_RUNTIMES:-0}"

require_file() {
  local path="$1"
  if [[ ! -e "${path}" ]]; then
    echo "缺少必要檔案或目錄：${path}" >&2
    exit 1
  fi
}

read_version() {
  local path="$1"
  local label="$2"
  local version=""
  version="$(tr -d '[:space:]' < "${path}")"
  if [[ -z "${version}" || ! "${version}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
    echo "${label} VERSION 格式錯誤：${path}" >&2
    exit 1
  fi
  printf '%s' "${version}"
}

current_platform() {
  case "$(uname -s):$(uname -m)" in
    Darwin:arm64)
      printf 'darwin-arm64'
      ;;
    Darwin:x86_64|Darwin:amd64)
      printf 'darwin-amd64'
      ;;
    Linux:x86_64|Linux:amd64)
      printf 'linux-amd64'
      ;;
    Linux:aarch64|Linux:arm64)
      printf 'linux-arm64'
      ;;
    *)
      echo "目前平台尚未定義 Runtime 目錄名稱：$(uname -s)/$(uname -m)" >&2
      exit 1
      ;;
  esac
}

force_build_enabled() {
  local normalized=""
  normalized="$(printf '%s' "${FORCE_BUILD}" | tr '[:upper:]' '[:lower:]')"
  case "${normalized}" in
    1|true|yes|on)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

runtime_version() {
  local runtime_dir="$1"
  if [[ -f "${runtime_dir}/VERSION" ]]; then
    tr -d '[:space:]' < "${runtime_dir}/VERSION"
  fi
}

ensure_llama_server() {
  local platform="$1"
  local version="$2"
  local runtime_dir="${LLAMA_RUNTIME_ROOT}/${platform}"
  local installed_version=""
  installed_version="$(runtime_version "${runtime_dir}")"

  if ! force_build_enabled \
    && [[ -x "${runtime_dir}/bin/llama-server" ]] \
    && [[ "${installed_version}" == "${version}" ]]; then
    echo "使用既有 llama-server ${version}（${platform}）"
    return
  fi

  echo "編譯 llama-server ${version}（${platform}）..."
  "${LLAMA_BUILD_SCRIPT}" "${LLAMA_SOURCE_DIR}" "${runtime_dir}" "${version}"

  installed_version="$(runtime_version "${runtime_dir}")"
  if [[ ! -x "${runtime_dir}/bin/llama-server" || "${installed_version}" != "${version}" ]]; then
    echo "llama-server Runtime 建立結果不完整：${runtime_dir}" >&2
    exit 1
  fi
}

ensure_mlx_server() {
  local platform="$1"
  local version="$2"
  if [[ "${platform}" != "darwin-arm64" ]]; then
    echo "略過 mlx-server：目前平台不是 macOS Apple Silicon。"
    return
  fi

  local runtime_dir="${MLX_RUNTIME_ROOT}/${platform}"
  local installed_version=""
  local metal_library="${runtime_dir}/bin/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib"
  installed_version="$(runtime_version "${runtime_dir}")"

  if ! force_build_enabled \
    && [[ -x "${runtime_dir}/bin/mlx-server" ]] \
    && [[ -f "${metal_library}" ]] \
    && [[ "${installed_version}" == "${version}" ]]; then
    echo "使用既有 mlx-server ${version}（${platform}）"
    return
  fi

  echo "編譯 mlx-server ${version}（${platform}）..."
  MLX_SERVER_OUTPUT_DIR="${runtime_dir}" \
    MLX_SERVER_VERSION="${version}" \
    "${MLX_BUILD_SCRIPT}"

  installed_version="$(runtime_version "${runtime_dir}")"
  if [[ ! -x "${runtime_dir}/bin/mlx-server" \
    || ! -f "${metal_library}" \
    || "${installed_version}" != "${version}" ]]; then
    echo "mlx-server Runtime 建立結果不完整：${runtime_dir}" >&2
    exit 1
  fi
}

require_file "${LLAMA_SOURCE_DIR}/CMakeLists.txt"
require_file "${LLAMA_SOURCE_DIR}/tools/server"
require_file "${LLAMA_VERSION_FILE}"
require_file "${LLAMA_BUILD_SCRIPT}"
require_file "${MLX_SOURCE_DIR}/Package.swift"
require_file "${MLX_SOURCE_DIR}/VERSION"
require_file "${MLX_BUILD_SCRIPT}"

PLATFORM="$(current_platform)"
LLAMA_VERSION="${LLAMA_SERVER_VERSION:-$(read_version "${LLAMA_VERSION_FILE}" "llama-server")}"
MLX_VERSION="${MLX_SERVER_VERSION:-$(read_version "${MLX_SOURCE_DIR}/VERSION" "mlx-server")}"

ensure_llama_server "${PLATFORM}" "${LLAMA_VERSION}"
ensure_mlx_server "${PLATFORM}" "${MLX_VERSION}"
