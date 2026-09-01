#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SOURCE_DIR="${LLAMA_CPP_SOURCE_DIR:-}"
OUTPUT_DIR="${LLAMA_SERVER_OUTPUT_DIR:-}"
LLAMA_VERSION="${LLAMA_SERVER_VERSION:-}"
EXTRA_CMAKE_ARGS=()

usage() {
  cat <<'EOF'
用法：build-llama-server.sh [選項] [-- 額外 CMake 參數]

選項：
  --source PATH   llama.cpp 原始碼目錄
  --output PATH   Runtime 輸出目錄
  --version TEXT  Runtime 版本標記
  -h, --help      顯示說明

Linux 會檢查並自動安裝 Vulkan 建置套件，預設建立 Vulkan Runtime。
EOF
}

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --source)
      [[ "$#" -ge 2 ]] || { echo "--source 缺少路徑" >&2; exit 2; }
      SOURCE_DIR="$2"
      shift 2
      ;;
    --output)
      [[ "$#" -ge 2 ]] || { echo "--output 缺少路徑" >&2; exit 2; }
      OUTPUT_DIR="$2"
      shift 2
      ;;
    --version)
      [[ "$#" -ge 2 ]] || { echo "--version 缺少版本" >&2; exit 2; }
      LLAMA_VERSION="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      EXTRA_CMAKE_ARGS=("$@")
      break
      ;;
    *)
      echo "未知參數：$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

resolve_platform() {
  case "$(uname -s):$(uname -m)" in
    Darwin:arm64) printf 'darwin-arm64' ;;
    Linux:x86_64|Linux:amd64) printf 'linux-amd64' ;;
    Linux:aarch64|Linux:arm64) printf 'linux-arm64' ;;
    *) return 1 ;;
  esac
}

resolve_source_dir() {
  local candidate=""
  for candidate in \
    "${SOURCE_DIR}" \
    "${SCRIPT_DIR}/llama-server/source" \
    "${SCRIPT_DIR}/llama-server"; do
    if [[ -n "${candidate}" && -f "${candidate}/CMakeLists.txt" && -d "${candidate}/tools/server" ]]; then
      (cd "${candidate}" && pwd)
      return
    fi
  done
  echo "找不到可建置的 llama.cpp 原始碼；請使用 --source 指定。" >&2
  exit 1
}

resolve_builder() {
  local candidate=""
  for candidate in \
    "${SCRIPT_DIR}/llama-server/build-local.sh" \
    "${SCRIPT_DIR}/scripts/build-llama-server-runtime.sh"; do
    if [[ -x "${candidate}" ]]; then
      printf '%s' "${candidate}"
      return
    fi
  done
  echo "找不到 build-llama-server-runtime.sh。" >&2
  exit 1
}

resolve_dependency_installer() {
  local candidate=""
  for candidate in \
    "${SCRIPT_DIR}/llama-server/install-vulkan-dependencies.sh" \
    "${SCRIPT_DIR}/scripts/install-linux-vulkan-dependencies.sh"; do
    if [[ -x "${candidate}" ]]; then
      printf '%s' "${candidate}"
      return
    fi
  done
  echo "找不到 Linux Vulkan 相依套件安裝器。" >&2
  exit 1
}

resolve_version() {
  local candidate=""
  if [[ -n "${LLAMA_VERSION}" ]]; then
    printf '%s' "${LLAMA_VERSION}"
    return
  fi
  for candidate in \
    "${SCRIPT_DIR}/llama-server/VERSION" \
    "${SCRIPT_DIR}/llama-runtime/SOURCE_VERSION"; do
    if [[ -f "${candidate}" ]]; then
      tr -d '[:space:]' < "${candidate}"
      return
    fi
  done
  echo "找不到 llama-server 版本；請使用 --version 指定。" >&2
  exit 1
}

PLATFORM="$(resolve_platform)" || {
  echo "不支援目前平台：$(uname -s)/$(uname -m)" >&2
  exit 1
}
SOURCE_DIR="$(resolve_source_dir)"
BUILDER="$(resolve_builder)"
LLAMA_VERSION="$(resolve_version)"

if [[ -z "${OUTPUT_DIR}" ]]; then
  OUTPUT_DIR="${SCRIPT_DIR}/llama-runtime/prebuilt/${PLATFORM}"
fi
if [[ "${OUTPUT_DIR}" != /* ]]; then
  OUTPUT_DIR="${SCRIPT_DIR}/${OUTPUT_DIR}"
fi

PERMISSION_STATUS=0
if [[ "${PLATFORM}" == linux-* ]]; then
  DEPENDENCY_INSTALLER="$(resolve_dependency_installer)"
  "${DEPENDENCY_INSTALLER}" dependencies
fi

OUTPUT_PARENT="$(dirname "${OUTPUT_DIR}")"
mkdir -p "${OUTPUT_PARENT}"
STAGING_DIR="$(mktemp -d "${OUTPUT_PARENT}/.llama-server-${PLATFORM}.XXXXXX")"
BACKUP_DIR="${OUTPUT_DIR}.bak"

cleanup() {
  if [[ -n "${STAGING_DIR:-}" && -d "${STAGING_DIR}" ]]; then
    rm -rf "${STAGING_DIR}"
  fi
}
trap cleanup EXIT

if [[ -e "${BACKUP_DIR}" ]]; then
  echo "既有備份尚未清除：${BACKUP_DIR}" >&2
  exit 1
fi

echo "正在建立 llama-server ${LLAMA_VERSION}（${PLATFORM}）…"
"${BUILDER}" \
  "${SOURCE_DIR}" \
  "${STAGING_DIR}" \
  "${LLAMA_VERSION}" \
  "${EXTRA_CMAKE_ARGS[@]}"

SERVER_BINARY="${STAGING_DIR}/bin/llama-server"
if [[ ! -x "${SERVER_BINARY}" ]]; then
  echo "建置結果缺少 llama-server：${SERVER_BINARY}" >&2
  exit 1
fi
if [[ "$(tr -d '[:space:]' < "${STAGING_DIR}/VERSION")" != "${LLAMA_VERSION}" ]]; then
  echo "建置結果版本不一致" >&2
  exit 1
fi
if [[ "$(tr -d '[:space:]' < "${STAGING_DIR}/PLATFORM")" != "${PLATFORM}" ]]; then
  echo "建置結果平台不一致" >&2
  exit 1
fi

if [[ "${PLATFORM}" == linux-* ]]; then
  if [[ "$(tr -d '[:space:]' < "${STAGING_DIR}/BACKEND")" != "vulkan" ]]; then
    echo "Linux 建置結果不是 Vulkan Runtime" >&2
    exit 1
  fi
  if ! ldd "${SERVER_BINARY}" | grep -F 'libvulkan.so.1' >/dev/null; then
    echo "Linux llama-server 未連結 Vulkan loader" >&2
    exit 1
  fi
  "${DEPENDENCY_INSTALLER}" permissions || PERMISSION_STATUS=$?
  if [[ "${PERMISSION_STATUS}" -ne 0 && "${PERMISSION_STATUS}" -ne 20 ]]; then
    exit "${PERMISSION_STATUS}"
  fi
  if [[ "${PERMISSION_STATUS}" -eq 0 ]] && compgen -G '/dev/dri/renderD*' >/dev/null; then
    if ! "${SERVER_BINARY}" --list-devices 2>&1 | grep -Eq 'Vulkan[0-9]+:'; then
      echo "Vulkan Runtime 無法取得可用 GPU；請檢查驅動與 /dev/dri 權限。" >&2
      exit 1
    fi
  fi
fi

if [[ -e "${OUTPUT_DIR}" ]]; then
  mv "${OUTPUT_DIR}" "${BACKUP_DIR}"
fi
if ! mv "${STAGING_DIR}" "${OUTPUT_DIR}"; then
  if [[ -e "${BACKUP_DIR}" ]]; then
    mv "${BACKUP_DIR}" "${OUTPUT_DIR}"
  fi
  exit 1
fi
STAGING_DIR=""

if [[ ! -x "${OUTPUT_DIR}/bin/llama-server" ]]; then
  echo "Runtime 切換後驗證失敗" >&2
  if [[ -e "${BACKUP_DIR}" ]]; then
    mv "${OUTPUT_DIR}" "${OUTPUT_DIR}.failed"
    mv "${BACKUP_DIR}" "${OUTPUT_DIR}"
  fi
  exit 1
fi
if [[ -e "${BACKUP_DIR}" ]]; then
  rm -rf "${BACKUP_DIR}"
fi

echo "llama-server Runtime 已更新：${OUTPUT_DIR}"
if [[ "${PERMISSION_STATUS}" -eq 20 ]]; then
  echo "GPU 群組權限已更新；請登出後重新登入，再啟動 Tanpopo。" >&2
  exit 20
fi
