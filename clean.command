#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

clean_build_directory() {
  local directory_name="$1"
  local target_path="${PROJECT_DIR}/${directory_name}"

  local expected_parent=""
  case "${directory_name}" in
    bin|dist)
      expected_parent="${PROJECT_DIR}"
      ;;
    mlx-server/.build)
      expected_parent="${PROJECT_DIR}/mlx-server"
      ;;
    desktop-ui/prebuilt)
      expected_parent="${PROJECT_DIR}/desktop-ui"
      ;;
    *)
      echo "拒絕清理未允許的目錄：${directory_name}" >&2
      exit 1
      ;;
  esac

  if [[ "$(dirname "${target_path}")" != "${expected_parent}" ]]; then
    echo "清理路徑驗證失敗：${target_path}" >&2
    exit 1
  fi
  if [[ -L "${target_path}" ]]; then
    echo "拒絕清理符號連結：${target_path}" >&2
    exit 1
  fi
  if [[ ! -e "${target_path}" ]]; then
    echo "略過不存在的目錄：${target_path}"
    return
  fi
  if [[ ! -d "${target_path}" ]]; then
    echo "清理目標不是目錄：${target_path}" >&2
    exit 1
  fi

  find "${target_path}" -mindepth 1 -depth -delete
  rmdir "${target_path}"
  echo "已清理：${target_path}"
}

echo "=== Tanpopo 清理建置產物 ==="
clean_build_directory "bin"
clean_build_directory "dist"
clean_build_directory "mlx-server/.build"
clean_build_directory "desktop-ui/prebuilt"
echo "清理完成。設定、模型、原始碼與 mlx.metallib 持久化快取均未變更。"
