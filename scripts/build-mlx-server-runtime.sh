#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PACKAGE_DIR="${PROJECT_DIR}/mlx-server"
OUTPUT_ROOT="${MLX_SERVER_OUTPUT_DIR:-${PROJECT_DIR}/mlx-runtime/prebuilt/darwin-arm64}"
VERSION_FILE="${PACKAGE_DIR}/VERSION"
VERSION="${MLX_SERVER_VERSION:-}"

if [[ -z "${VERSION}" ]]; then
  if [[ ! -f "${VERSION_FILE}" ]]; then
    echo "缺少 mlx-server 版本檔：${VERSION_FILE}" >&2
    exit 1
  fi
  VERSION="$(tr -d '[:space:]' < "${VERSION_FILE}")"
fi
if [[ -z "${VERSION}" || ! "${VERSION}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
  echo "mlx-server 版本號格式錯誤" >&2
  exit 1
fi

if [[ "$(uname -s)" != "Darwin" || "$(uname -m)" != "arm64" ]]; then
  echo "mlx-server 只能在 macOS Apple Silicon 上編譯。" >&2
  exit 1
fi
if ! command -v swift >/dev/null 2>&1; then
  echo "缺少 Swift 工具鏈，請先安裝 Xcode Command Line Tools。" >&2
  exit 1
fi

swift build -c release --package-path "${PACKAGE_DIR}"

PRODUCT_DIR="$(swift build -c release --package-path "${PACKAGE_DIR}" --show-bin-path)"
mkdir -p "${OUTPUT_ROOT}/bin"
cp "${PRODUCT_DIR}/mlx-server" "${OUTPUT_ROOT}/bin/mlx-server"
chmod +x "${OUTPUT_ROOT}/bin/mlx-server"
printf '%s\n' "${VERSION}" > "${OUTPUT_ROOT}/VERSION"

find "${PRODUCT_DIR}" -maxdepth 1 \( -name '*.bundle' -o -name '*.dylib' \) -print0 | while IFS= read -r -d '' artifact; do
  cp -R "${artifact}" "${OUTPUT_ROOT}/bin/"
done

if [[ ! -f "${OUTPUT_ROOT}/bin/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib" ]]; then
  echo "mlx-server Runtime 缺少 MLX Metal Library。" >&2
  exit 1
fi

echo "mlx-server Runtime 已建立：${OUTPUT_ROOT}"
