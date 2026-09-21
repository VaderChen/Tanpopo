#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd -P)"

clean_build_directory() {
  local directory_name="$1"
  local preserve_directory="${2:-false}"
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

  # 除目標本身，也驗證父目錄；mlx-server 或 desktop-ui 若是符號連結，
  # 其 .build / prebuilt 可能實際位於專案外，不可直接清除。
  local physical_parent=""
  physical_parent="$(cd "${expected_parent}" && pwd -P)"
  if [[ "${physical_parent}" != "${expected_parent}" ]]; then
    echo "拒絕穿過父目錄符號連結進行清理：${target_path}" >&2
    exit 1
  fi

  # find 預設不跟隨符號連結，並涵蓋隱藏檔與巢狀目錄。
  find "${target_path}" -mindepth 1 -depth -delete
  if [[ "${preserve_directory}" != "true" ]]; then
    rmdir "${target_path}"
  fi
  echo "已清理：${target_path}"
}

if [[ "$#" -gt 0 ]]; then
  if [[ "$#" -ne 1 || "$1" != "--dist" ]]; then
    echo "用法：$0 [--dist]" >&2
    exit 1
  fi
  # 完整建置／封裝共用此入口，不清除 Runtime 快取或其他建置目錄。
  clean_build_directory "dist" true
  exit 0
fi

echo "=== Tanpopo 清理建置產物 ==="
clean_build_directory "bin"
clean_build_directory "dist"
clean_build_directory "mlx-server/.build"
clean_build_directory "desktop-ui/prebuilt"
echo "清理完成。設定、模型、原始碼與 mlx.metallib 持久化快取均未變更。"
