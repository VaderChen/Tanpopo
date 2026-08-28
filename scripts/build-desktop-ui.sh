#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SOURCE_FILE="${PROJECT_DIR}/desktop-ui/darwin/TanpopoUI.swift"
ICON_FILE="${PROJECT_DIR}/desktop-ui/assets/TanpopoIcon.png"
OUTPUT_ROOT="${DESKTOP_UI_OUTPUT_DIR:-${PROJECT_DIR}/desktop-ui/prebuilt/darwin-arm64}"
OUTPUT_FILE="${OUTPUT_ROOT}/TanpopoUI"
OUTPUT_ICON="${OUTPUT_ROOT}/TanpopoIcon.png"

if [[ "$(uname -s):$(uname -m)" != "Darwin:arm64" ]]; then
  echo "目前平台不需要建立 macOS 原生 UI：$(uname -s)/$(uname -m)"
  exit 0
fi
if [[ ! -f "${SOURCE_FILE}" ]]; then
  echo "缺少原生 UI 原始碼：${SOURCE_FILE}" >&2
  exit 1
fi
if [[ ! -f "${ICON_FILE}" ]]; then
  echo "缺少 Tanpopo APP ICON：${ICON_FILE}" >&2
  exit 1
fi
if ! command -v xcrun >/dev/null 2>&1; then
  echo "缺少 xcrun，無法建立 macOS 原生 UI。" >&2
  exit 1
fi
if [[ -x "${OUTPUT_FILE}" \
  && -f "${OUTPUT_ICON}" \
  && "${OUTPUT_FILE}" -nt "${SOURCE_FILE}" \
  && "${OUTPUT_ICON}" -nt "${ICON_FILE}" \
  && "${OUTPUT_FILE}" -nt "${BASH_SOURCE[0]}" \
  && "${DESKTOP_UI_REBUILD:-0}" != "1" ]]; then
  echo "使用既有 macOS 原生 UI：${OUTPUT_FILE}"
  exit 0
fi

mkdir -p "${OUTPUT_ROOT}"
STAGING_DIR="$(mktemp -d "${OUTPUT_ROOT}/.build.XXXXXX")"
cleanup() {
  if [[ -n "${STAGING_DIR:-}" && -d "${STAGING_DIR}" ]]; then
    rm -rf "${STAGING_DIR}"
  fi
}
trap cleanup EXIT

echo "編譯 macOS 原生 UI（AppKit／WKWebView）..."
xcrun swiftc \
  -parse-as-library \
  -O \
  -framework AppKit \
  -framework WebKit \
  "${SOURCE_FILE}" \
  -o "${STAGING_DIR}/TanpopoUI"
cp "${ICON_FILE}" "${STAGING_DIR}/TanpopoIcon.png"
chmod +x "${STAGING_DIR}/TanpopoUI"
mv -f "${STAGING_DIR}/TanpopoUI" "${OUTPUT_FILE}"
mv -f "${STAGING_DIR}/TanpopoIcon.png" "${OUTPUT_ICON}"

echo "macOS 原生 UI 已建立：${OUTPUT_FILE}"
