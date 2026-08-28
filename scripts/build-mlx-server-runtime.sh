#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PACKAGE_DIR="${PROJECT_DIR}/mlx-server"
OUTPUT_ROOT="${MLX_SERVER_OUTPUT_DIR:-${PROJECT_DIR}/mlx-runtime/prebuilt/darwin-arm64}"
METAL_CACHE_ROOT="${MLX_METAL_CACHE_DIR:-${PROJECT_DIR}/mlx-runtime/metal-cache/darwin-arm64}"
METAL_MANIFEST_PATCH="${PROJECT_DIR}/scripts/mlx-swift-prebuilt-metallib.patch"
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
for required_command in patch shasum xcrun; do
  if ! command -v "${required_command}" >/dev/null 2>&1; then
    echo "缺少 mlx-server 建置工具：${required_command}" >&2
    exit 1
  fi
done
if [[ ! -f "${METAL_MANIFEST_PATCH}" ]]; then
  echo "缺少 MLX Metal 快取補丁：${METAL_MANIFEST_PATCH}" >&2
  exit 1
fi

swift package resolve --package-path "${PACKAGE_DIR}"

MLX_SWIFT_CHECKOUT="${PACKAGE_DIR}/.build/checkouts/mlx-swift"
MLX_SWIFT_MANIFEST="${MLX_SWIFT_CHECKOUT}/Package.swift"
METAL_SOURCE_DIR="${MLX_SWIFT_CHECKOUT}/Source/Cmlx/mlx-generated/metal"
PREBUILT_METAL_DIR="${MLX_SWIFT_CHECKOUT}/Source/Cmlx/PrebuiltMetal"

if [[ ! -f "${MLX_SWIFT_MANIFEST}" || ! -d "${METAL_SOURCE_DIR}" ]]; then
  echo "找不到 mlx-swift checkout 或 Metal 原始碼。" >&2
  exit 1
fi

METAL_SOURCE_DIGEST="$({
  while IFS= read -r source_file; do
    relative_name="${source_file#${METAL_SOURCE_DIR}/}"
    source_digest="$(shasum -a 256 "${source_file}" | awk '{print $1}')"
    printf '%s=%s\n' "${relative_name}" "${source_digest}"
  done < <(find "${METAL_SOURCE_DIR}" -type f -print | LC_ALL=C sort)
} | shasum -a 256 | awk '{print $1}')"
METAL_TOOLCHAIN_VERSION="$(xcrun --sdk macosx metal --version 2>&1 | tr '\n' ' ')"
METAL_SDK_VERSION="$(xcrun --sdk macosx --show-sdk-version)"
METAL_FINGERPRINT="$(printf '%s\n' \
  "platform=darwin-arm64" \
  "deployment_target=14.0" \
  "sdk=${METAL_SDK_VERSION}" \
  "toolchain=${METAL_TOOLCHAIN_VERSION}" \
  "sources=${METAL_SOURCE_DIGEST}" \
  | shasum -a 256 | awk '{print $1}')"
METAL_CACHE_DIR="${METAL_CACHE_ROOT}/${METAL_FINGERPRINT}"
CACHED_METAL_LIBRARY="${METAL_CACHE_DIR}/default.metallib"

cache_existing_metallib() {
  local candidate_file=""
  local newer_source=""
  local candidate_files=(
    "${PACKAGE_DIR}/.build/out/Products/Release/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib"
    "${PACKAGE_DIR}/.build/out/Products/Debug/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib"
  )

  if [[ "${MLX_METALLIB_REBUILD:-0}" == "1" || -f "${CACHED_METAL_LIBRARY}" ]]; then
    return
  fi

  for candidate_file in "${candidate_files[@]}"; do
    if [[ ! -f "${candidate_file}" ]]; then
      continue
    fi
    newer_source="$(find "${METAL_SOURCE_DIR}" -type f -newer "${candidate_file}" -print -quit)"
    if [[ -n "${newer_source}" ]]; then
      continue
    fi
    mkdir -p "${METAL_CACHE_DIR}"
    cp "${candidate_file}" "${CACHED_METAL_LIBRARY}"
    echo "已保存既有 MLX Metal Library：${METAL_CACHE_DIR}"
    return
  done
}

enable_prebuilt_metallib_manifest() {
  local manifest_backup="${MLX_SWIFT_MANIFEST}.llamaloader-backup"

  mkdir -p "${PREBUILT_METAL_DIR}"
  if grep -Fq 'LLAMA_LOADER_PREBUILT_METALLIB' "${MLX_SWIFT_MANIFEST}"; then
    return
  fi

  cp "${MLX_SWIFT_MANIFEST}" "${manifest_backup}"
  chmod u+w "${MLX_SWIFT_MANIFEST}"
  if ! patch -s -d "${MLX_SWIFT_CHECKOUT}" -p1 < "${METAL_MANIFEST_PATCH}"; then
    mv -f "${manifest_backup}" "${MLX_SWIFT_MANIFEST}"
    echo "目前 mlx-swift 版本無法套用 Metal 快取補丁。" >&2
    exit 1
  fi
  rm -f "${manifest_backup}"
  chmod a-w "${MLX_SWIFT_MANIFEST}"
}

write_cache_metadata() {
  mkdir -p "${METAL_CACHE_DIR}"
  {
    printf 'fingerprint=%s\n' "${METAL_FINGERPRINT}"
    printf 'platform=darwin-arm64\n'
    printf 'deployment_target=14.0\n'
    printf 'sdk=%s\n' "${METAL_SDK_VERSION}"
    printf 'source_digest=%s\n' "${METAL_SOURCE_DIGEST}"
    printf 'metal_toolchain=%s\n' "${METAL_TOOLCHAIN_VERSION}"
  } > "${METAL_CACHE_DIR}/METADATA"
}

cache_existing_metallib
enable_prebuilt_metallib_manifest

USE_PREBUILT_METALLIB="0"
if [[ -f "${CACHED_METAL_LIBRARY}" && "${MLX_METALLIB_REBUILD:-0}" != "1" ]]; then
  cp "${CACHED_METAL_LIBRARY}" "${PREBUILT_METAL_DIR}/default.metallib"
  USE_PREBUILT_METALLIB="1"
  echo "使用已保存的 MLX Metal Library：${METAL_FINGERPRINT}"
else
  echo "Metal 快取未命中，這次會編譯一次並保存供後續使用。"
fi

LLAMA_LOADER_PREBUILT_METALLIB="${USE_PREBUILT_METALLIB}" \
  swift build -c release --package-path "${PACKAGE_DIR}" \
    -Xswiftc -suppress-warnings

PRODUCT_DIR="$(LLAMA_LOADER_PREBUILT_METALLIB="${USE_PREBUILT_METALLIB}" \
  swift build -c release --package-path "${PACKAGE_DIR}" --show-bin-path)"
PRODUCT_METAL_LIBRARY="${PRODUCT_DIR}/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib"
if [[ ! -f "${PRODUCT_METAL_LIBRARY}" ]]; then
  echo "mlx-server 建置結果缺少 MLX Metal Library。" >&2
  exit 1
fi
if [[ ! -f "${CACHED_METAL_LIBRARY}" || "${MLX_METALLIB_REBUILD:-0}" == "1" ]]; then
  mkdir -p "${METAL_CACHE_DIR}"
  cp "${PRODUCT_METAL_LIBRARY}" "${CACHED_METAL_LIBRARY}"
  echo "MLX Metal Library 已保存：${METAL_CACHE_DIR}"
fi
write_cache_metadata

mkdir -p "${OUTPUT_ROOT}/bin"
cp "${PRODUCT_DIR}/mlx-server" "${OUTPUT_ROOT}/bin/mlx-server"
chmod +x "${OUTPUT_ROOT}/bin/mlx-server"
printf '%s\n' "${VERSION}" > "${OUTPUT_ROOT}/VERSION"
printf '%s\n' "${METAL_FINGERPRINT}" > "${OUTPUT_ROOT}/METALLIB_FINGERPRINT"

find "${PRODUCT_DIR}" -maxdepth 1 \( -name '*.bundle' -o -name '*.dylib' \) -print0 | while IFS= read -r -d '' artifact; do
  cp -R "${artifact}" "${OUTPUT_ROOT}/bin/"
done

if [[ ! -f "${OUTPUT_ROOT}/bin/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib" ]]; then
  echo "mlx-server Runtime 缺少 MLX Metal Library。" >&2
  exit 1
fi
if ! cmp -s \
  "${CACHED_METAL_LIBRARY}" \
  "${OUTPUT_ROOT}/bin/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib"; then
  echo "mlx-server Runtime 的 Metal Library 與持久化快取不一致。" >&2
  exit 1
fi

echo "mlx-server Runtime 已建立：${OUTPUT_ROOT}"
