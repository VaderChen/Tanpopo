#!/usr/bin/env bash

set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  exit 0
fi

MODE="${1:-dependencies}"
TARGET_USER="${SUDO_USER:-${USER:-$(id -un)}}"

run_as_root() {
  if [[ "${EUID}" -eq 0 ]]; then
    "$@"
    return
  fi
  if ! command -v sudo >/dev/null 2>&1; then
    echo "缺少 sudo，無法安裝 Linux Vulkan 相依套件。" >&2
    exit 1
  fi
  sudo "$@"
}

install_dependencies() {
  if command -v cmake >/dev/null 2>&1 \
    && command -v c++ >/dev/null 2>&1 \
    && command -v pkg-config >/dev/null 2>&1 \
    && command -v glslc >/dev/null 2>&1 \
    && command -v vulkaninfo >/dev/null 2>&1 \
    && pkg-config --exists vulkan \
    && [[ -r /usr/include/vulkan/vulkan.h ]]; then
    return
  fi

  echo "正在安裝 llama.cpp Vulkan 建置與執行套件…"
  if command -v apt-get >/dev/null 2>&1; then
    run_as_root apt-get update
    run_as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y \
      build-essential cmake pkg-config libvulkan-dev glslc spirv-headers vulkan-tools
  elif command -v dnf >/dev/null 2>&1; then
    run_as_root dnf install -y \
      gcc-c++ cmake pkgconf-pkg-config vulkan-loader-devel glslc spirv-headers vulkan-tools
  elif command -v pacman >/dev/null 2>&1; then
    run_as_root pacman -S --needed --noconfirm \
      base-devel cmake pkgconf vulkan-headers vulkan-icd-loader shaderc spirv-headers vulkan-tools
  else
    echo "無法辨識套件管理器；請先安裝 C/C++、CMake、Vulkan headers/loader、glslc、SPIR-V headers 與 Vulkan tools。" >&2
    exit 1
  fi

  for command_name in cmake c++ pkg-config glslc vulkaninfo; do
    if ! command -v "${command_name}" >/dev/null 2>&1; then
      echo "Vulkan 相依套件安裝後仍缺少：${command_name}" >&2
      exit 1
    fi
  done
  if [[ ! -r /usr/include/vulkan/vulkan.h ]]; then
    echo "Vulkan 相依套件安裝後仍缺少 vulkan.h" >&2
    exit 1
  fi
  if ! pkg-config --exists vulkan; then
    echo "Vulkan 相依套件安裝後仍缺少 Vulkan loader 開發資訊" >&2
    exit 1
  fi
}

ensure_device_permissions() {
  local node=""
  local group=""
  local current_groups=" $(id -nG) "
  local changed=0
  local groups=()

  for node in /dev/dri/renderD*; do
    [[ -e "${node}" ]] || continue
    if [[ -r "${node}" && -w "${node}" ]]; then
      continue
    fi
    group="$(stat -c '%G' "${node}" 2>/dev/null || true)"
    [[ -n "${group}" && "${group}" != "UNKNOWN" ]] || continue
    if [[ "${current_groups}" != *" ${group} "* ]]; then
      groups+=("${group}")
    fi
  done

  if [[ "${#groups[@]}" -eq 0 ]]; then
    return
  fi

  local unique_groups=""
  unique_groups="$(printf '%s\n' "${groups[@]}" | awk 'NF && !seen[$0]++' | paste -sd, -)"
  run_as_root usermod -aG "${unique_groups}" "${TARGET_USER}"
  changed=1

  if [[ "${changed}" -eq 1 ]]; then
    echo "已將 ${TARGET_USER} 加入 GPU 群組 ${unique_groups}。請登出後重新登入，再啟動 Tanpopo。" >&2
    return 20
  fi
}

case "${MODE}" in
  dependencies)
    install_dependencies
    ;;
  permissions)
    ensure_device_permissions
    ;;
  *)
    echo "用法：$0 [dependencies|permissions]" >&2
    exit 1
    ;;
esac
