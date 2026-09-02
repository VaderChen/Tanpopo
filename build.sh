#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_NAME="Tanpopo"
TODAY_VERSION="$(TZ=Asia/Taipei date '+1.%y.%m%d')"
APP_VERSION="${TANPOPO_VERSION:-${TODAY_VERSION}}"
APP_BUILD="${TANPOPO_BUILD:-$(TZ=Asia/Taipei date '+%H%M')}"
UPDATE_REPOSITORY="${TANPOPO_UPDATE_REPOSITORY:-VaderChen/Tanpopo}"
BIN_DIR="${PROJECT_DIR}/bin"
DIST_DIR="${PROJECT_DIR}/dist"
BUILD_TIME="$(date +%Y%m%d_%H%M%S)"
BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LLAMA_SOURCE_DIR="${LLAMA_CPP_SOURCE_DIR:-}"
LLAMA_PREBUILT_DIR="${LLAMA_SERVER_PREBUILT_DIR:-${PROJECT_DIR}/llama-runtime/prebuilt}"
RUNTIME_BUILD_SCRIPT="${PROJECT_DIR}/scripts/build-llama-server-runtime.sh"
LLAMA_BUILD_ENTRY_SCRIPT="${PROJECT_DIR}/build-llama-server.sh"
VULKAN_INSTALL_SCRIPT="${PROJECT_DIR}/scripts/install-linux-vulkan-dependencies.sh"
LLAMA_PINNED_VERSION_FILE="${LLAMA_SERVER_VERSION_FILE:-}"
DEFAULT_LLAMA_PLATFORMS=(darwin-arm64 linux-amd64)
PACKAGED_LLAMA_PLATFORMS=()
MLX_SOURCE_DIR="${PROJECT_DIR}/mlx-server"
MLX_PREBUILT_DIR="${MLX_SERVER_PREBUILT_DIR:-${PROJECT_DIR}/mlx-runtime/prebuilt}"
MLX_BUILD_SCRIPT="${PROJECT_DIR}/scripts/build-mlx-server-runtime.sh"
MLX_VERSION_FILE="${MLX_SOURCE_DIR}/VERSION"
MLX_VERSION="${MLX_SERVER_VERSION:-}"
DESKTOP_UI_BUILD_SCRIPT="${PROJECT_DIR}/scripts/build-desktop-ui.sh"
DESKTOP_UI_PREBUILT_DIR="${PROJECT_DIR}/desktop-ui/prebuilt/darwin-arm64"

cd "${PROJECT_DIR}"

require_file() {
  local path="$1"
  if [[ ! -e "${path}" ]]; then
    echo "缺少必要檔案或目錄：${path}" >&2
    exit 1
  fi
}

require_command() {
  local name="$1"
  if ! command -v "${name}" >/dev/null 2>&1; then
    echo "缺少必要工具：${name}" >&2
    exit 1
  fi
}

current_llama_platform() {
  case "$(uname -s):$(uname -m)" in
    Darwin:arm64)
      printf 'darwin-arm64'
      ;;
    Linux:x86_64|Linux:amd64)
      printf 'linux-amd64'
      ;;
    Linux:aarch64|Linux:arm64)
      printf 'linux-arm64'
      ;;
    *)
      return 1
      ;;
  esac
}

discover_llama_source() {
  if [[ -n "${LLAMA_SOURCE_DIR}" ]]; then
    printf '%s' "${LLAMA_SOURCE_DIR}"
    return
  fi

  if [[ -f "${PROJECT_DIR}/llama-server/CMakeLists.txt" && -d "${PROJECT_DIR}/llama-server/tools/server" ]]; then
    printf '%s' "${PROJECT_DIR}/llama-server"
    return
  fi

  local candidates=()
  local candidate=""
  local resolved=""
  local existing=""
  local duplicated=""
  local common_paths=(
    "${PROJECT_DIR}/llama.cpp"
    "${HOME}/services/llama.cpp"
    "${HOME}/llama.cpp"
  )

  for candidate in "${common_paths[@]}"; do
    if [[ -f "${candidate}/CMakeLists.txt" && -d "${candidate}/tools/server" ]]; then
      candidates+=("$(cd "${candidate}" && pwd)")
    fi
  done

  if [[ -d "${HOME}/Codes" ]]; then
    while IFS= read -r candidate; do
      resolved="$(cd "$(dirname "${candidate}")" && pwd)"
      duplicated="false"
      if [[ "${#candidates[@]}" -gt 0 ]]; then
        for existing in "${candidates[@]}"; do
          if [[ "${existing}" == "${resolved}" ]]; then
            duplicated="true"
            break
          fi
        done
      fi
      if [[ "${duplicated}" == "false" && -d "${resolved}/tools/server" ]]; then
        candidates+=("${resolved}")
      fi
    done < <(find "${HOME}/Codes" -maxdepth 7 -type f -path '*/llama.cpp/CMakeLists.txt' -print 2>/dev/null)
  fi

  if [[ "${#candidates[@]}" -eq 1 ]]; then
    printf '%s' "${candidates[0]}"
    return
  fi
  if [[ "${#candidates[@]}" -eq 0 ]]; then
    echo "找不到可建置 llama-server 的 llama.cpp 原始碼；請設定 LLAMA_CPP_SOURCE_DIR" >&2
    exit 1
  fi

  echo "找到多個 llama.cpp 原始碼目錄，請用 LLAMA_CPP_SOURCE_DIR 明確指定：" >&2
  for candidate in "${candidates[@]}"; do
    echo "  ${candidate}" >&2
  done
  exit 1
}

resolve_source_commit() {
  local git_entry="${LLAMA_SOURCE_DIR}/.git"
  local git_directory=""
  local head=""
  local ref_name=""
  local commit=""

  if [[ -d "${git_entry}" ]]; then
    git_directory="${git_entry}"
  elif [[ -f "${git_entry}" ]]; then
    IFS= read -r head < "${git_entry}" || true
    if [[ "${head}" == gitdir:* ]]; then
      git_directory="${head#gitdir: }"
      if [[ "${git_directory}" != /* ]]; then
        git_directory="${LLAMA_SOURCE_DIR}/${git_directory}"
      fi
    fi
  fi
  if [[ -z "${git_directory}" || ! -f "${git_directory}/HEAD" ]]; then
    return
  fi

  IFS= read -r head < "${git_directory}/HEAD" || true
  if [[ "${head}" == ref:* ]]; then
    ref_name="${head#ref: }"
    if [[ -f "${git_directory}/${ref_name}" ]]; then
      IFS= read -r commit < "${git_directory}/${ref_name}" || true
    elif [[ -f "${git_directory}/packed-refs" ]]; then
      commit="$(awk -v ref="${ref_name}" '$2 == ref { print $1; exit }' "${git_directory}/packed-refs")"
    fi
  else
    commit="${head}"
  fi

  if [[ "${commit}" =~ ^[0-9a-fA-F]{7,64}$ ]]; then
    printf '%s' "${commit:0:12}"
  fi
}

resolve_llama_version() {
  if [[ -n "${LLAMA_SERVER_VERSION:-}" ]]; then
    printf '%s' "${LLAMA_SERVER_VERSION}"
    return
  fi

  local version_file="${LLAMA_PINNED_VERSION_FILE}"
  local pinned_version=""
  if [[ -z "${version_file}" ]]; then
    if [[ "${LLAMA_SOURCE_DIR}" == "${PROJECT_DIR}/llama-server" ]]; then
      version_file="${PROJECT_DIR}/llama-runtime/SOURCE_VERSION"
    else
      version_file="${LLAMA_SOURCE_DIR}/LLAMA_SERVER_VERSION"
    fi
  fi
  if [[ -f "${version_file}" ]]; then
    IFS= read -r pinned_version < "${version_file}" || true
    pinned_version="${pinned_version//[[:space:]]/}"
    if [[ -n "${pinned_version}" ]]; then
      printf '%s' "${pinned_version}"
      return
    fi
  fi

  local build_info="${LLAMA_SOURCE_DIR}/cmake/build-info.cmake"
  local build_number=""
  local build_commit=""
  if [[ -f "${build_info}" ]]; then
    build_number="$(sed -nE 's/.*BUILD_NUMBER[[:space:]]+"?([^" )]+).*/\1/p' "${build_info}" | head -n 1)"
    build_commit="$(sed -nE 's/.*BUILD_COMMIT[[:space:]]+"?([^" )]+).*/\1/p' "${build_info}" | head -n 1)"
  fi
  if [[ "${build_number}" =~ ^[1-9][0-9]*$ ]]; then
    if [[ -n "${build_commit}" && "${build_commit}" != "unknown" ]]; then
      printf 'b%s-%s' "${build_number}" "${build_commit}"
    else
      printf 'b%s' "${build_number}"
    fi
    return
  fi

  local source_commit=""
  source_commit="$(resolve_source_commit)"
  if [[ -n "${source_commit}" ]]; then
    printf 'custom-%s' "${source_commit}"
    return
  fi

  echo "無法從 llama.cpp 原始碼取得版本；請設定 LLAMA_SERVER_VERSION" >&2
  exit 1
}

sanitize_version() {
  local value="$1"
  local label="${2:-Runtime}"
  value="$(printf '%s' "${value}" | tr -cs 'A-Za-z0-9._-' '-')"
  value="${value#-}"
  value="${value%-}"
  if [[ -z "${value}" ]]; then
    echo "${label} 版本號格式錯誤" >&2
    exit 1
  fi
  printf '%s' "${value}"
}

LLAMA_SOURCE_DIR="$(discover_llama_source)"

require_file "go.mod"
require_file "src/cmd/llamaloader/main.go"
require_file "website"
require_file "reports"
require_file "agent.sample.properties"
require_file "README.md"
require_file "run.command"
require_file "${RUNTIME_BUILD_SCRIPT}"
require_file "${LLAMA_BUILD_ENTRY_SCRIPT}"
require_file "${VULKAN_INSTALL_SCRIPT}"
require_file "${MLX_BUILD_SCRIPT}"
require_file "${MLX_SOURCE_DIR}/Package.swift"
require_file "${MLX_VERSION_FILE}"
require_file "${DESKTOP_UI_BUILD_SCRIPT}"
require_file "desktop-ui/darwin/TanpopoUI.swift"
require_file "${LLAMA_SOURCE_DIR}/CMakeLists.txt"
require_file "${LLAMA_SOURCE_DIR}/tools/server"
require_command "go"
require_command "tar"
require_command "rsync"
require_command "zip"
require_command "unzip"

if [[ -z "${MLX_VERSION}" ]]; then
  MLX_VERSION="$(tr -d '[:space:]' < "${MLX_VERSION_FILE}")"
fi
APP_VERSION="$(sanitize_version "${APP_VERSION}" "Tanpopo")"
if [[ ! "${APP_VERSION}" =~ ^1\.[0-9]{2}\.[0-9]{4}$ ]]; then
  echo "Tanpopo 版本號格式錯誤：${APP_VERSION}（應為 1.YY.MMDD）" >&2
  exit 1
fi
if [[ ! "${APP_BUILD}" =~ ^[0-9]{4}$ ]]; then
  echo "Tanpopo build 編號格式錯誤：${APP_BUILD}（應為 HHmm）" >&2
  exit 1
fi
if [[ ! "${UPDATE_REPOSITORY}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "Tanpopo GitHub repository 格式錯誤：${UPDATE_REPOSITORY}" >&2
  exit 1
fi
MLX_VERSION="$(sanitize_version "${MLX_VERSION}" "mlx-server")"
LLAMA_VERSION="$(sanitize_version "$(resolve_llama_version)" "llama-server")"
APP_LDFLAGS="-s -w -X LlamaLoader/src/appversion.Version=${APP_VERSION} -X LlamaLoader/src/appversion.Build=${APP_BUILD} -X LlamaLoader/src/appversion.Repository=${UPDATE_REPOSITORY}"
APP_DEV_LDFLAGS="-X LlamaLoader/src/appversion.Version=${APP_VERSION} -X LlamaLoader/src/appversion.Build=${APP_BUILD} -X LlamaLoader/src/appversion.Repository=${UPDATE_REPOSITORY}"
PACKAGE_NAME="${APP_NAME}_deploy_llama-${LLAMA_VERSION}_${BUILD_TIME}"
NATIVE_LLAMA_PLATFORM="$(current_llama_platform || true)"

llama_prebuilt_is_current() {
  local platform="$1"
  local runtime_dir="${LLAMA_PREBUILT_DIR}/${platform}"
  local runtime_version=""
  local runtime_backend=""
  if [[ -f "${runtime_dir}/VERSION" ]]; then
    runtime_version="$(tr -d '[:space:]' < "${runtime_dir}/VERSION")"
  fi
  if [[ -f "${runtime_dir}/BACKEND" ]]; then
    runtime_backend="$(tr -d '[:space:]' < "${runtime_dir}/BACKEND")"
  fi
  [[ -x "${runtime_dir}/bin/llama-server" && "${runtime_version}" == "${LLAMA_VERSION}" ]] || return 1
  [[ "${platform}" != linux-* || "${runtime_backend}" == "vulkan" ]]
}

mlx_prebuilt_is_current() {
  local runtime_dir="${MLX_PREBUILT_DIR}/darwin-arm64"
  local runtime_version=""
  if [[ -f "${runtime_dir}/VERSION" ]]; then
    runtime_version="$(tr -d '[:space:]' < "${runtime_dir}/VERSION")"
  fi
  [[ -x "${runtime_dir}/bin/mlx-server" \
    && -f "${runtime_dir}/bin/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib" \
    && "${runtime_version}" == "${MLX_VERSION}" ]]
}

if [[ "${1:-}" == "--check" ]]; then
  echo "llama.cpp 原始碼：${LLAMA_SOURCE_DIR}"
  echo "llama-server 版本：${LLAMA_VERSION}"
  for platform in "${DEFAULT_LLAMA_PLATFORMS[@]}"; do
    if llama_prebuilt_is_current "${platform}"; then
      echo "${platform}：已有相同版本的預編譯 Runtime"
    elif [[ "${platform}" == "${NATIVE_LLAMA_PLATFORM}" ]]; then
      echo "${platform}：封裝時會在目前主機自動編譯"
    else
      echo "${platform}：部署包將攜帶原始碼，安裝時在該平台原生編譯"
    fi
  done
  if mlx_prebuilt_is_current; then
    echo "darwin-arm64：已有預編譯 mlx-server ${MLX_VERSION}"
  else
    echo "darwin-arm64：尚未建立預編譯 mlx-server ${MLX_VERSION}（封裝時會在 Apple Silicon 自動編譯）"
  fi
  exit 0
fi

mkdir -p "${BIN_DIR}" "${DIST_DIR}"
WORK_DIR="$(mktemp -d "${DIST_DIR}/.package.XXXXXX")"
PACKAGE_DIR="${WORK_DIR}/${PACKAGE_NAME}"

cleanup() {
  if [[ -n "${WORK_DIR:-}" && -d "${WORK_DIR}" ]]; then
    rm -rf "${WORK_DIR}"
  fi
}
trap cleanup EXIT

mkdir -p "${PACKAGE_DIR}/bin"

build_target() {
  local target_os="$1"
  local target_arch="$2"
  local output_name="$3"

  echo "編譯 ${target_os}/${target_arch}..."
  CGO_ENABLED=0 GOOS="${target_os}" GOARCH="${target_arch}" go build \
    -buildvcs=false \
    -trimpath \
    -ldflags "${APP_LDFLAGS}" \
    -o "${BIN_DIR}/${output_name}" \
    ./src/cmd/llamaloader
  cp "${BIN_DIR}/${output_name}" "${PACKAGE_DIR}/bin/${output_name}"
}

copy_llama_source() {
  local destination="${PACKAGE_DIR}/llama-server/source"
  local required_source_file=""
  mkdir -p "${destination}"
  COPYFILE_DISABLE=1 /usr/bin/rsync -a \
    --exclude='/.git/' \
    --exclude='/.github/' \
    --exclude='/build/' \
    --exclude='/build-*/' \
    --exclude='/docs/' \
    --exclude='/examples/' \
    --exclude='/tests/' \
    --exclude='/media/' \
    --exclude='/models/' \
    --exclude='/pocs/' \
    --exclude='/prompts/' \
    "${LLAMA_SOURCE_DIR}/" "${destination}/"

  for required_source_file in \
    cmake/build-info.cmake \
    common/build-info.cpp.in \
    tools/mtmd/models/models.h; do
    if [[ ! -f "${destination}/${required_source_file}" ]]; then
      echo "llama.cpp 原始碼缺少必要檔案：${required_source_file}" >&2
      exit 1
    fi
  done
  if find "${destination}" -type f -name '._*' -print -quit | grep -q .; then
    echo "llama.cpp 原始碼不可包含 AppleDouble 中繼檔" >&2
    exit 1
  fi
}

ensure_native_llama_prebuilt_runtime() {
  if [[ -z "${NATIVE_LLAMA_PLATFORM}" ]]; then
    echo "目前主機不支援自動建立 llama-server Runtime：$(uname -s)/$(uname -m)" >&2
    exit 1
  fi
  if llama_prebuilt_is_current "${NATIVE_LLAMA_PLATFORM}"; then
    echo "使用既有 llama-server ${LLAMA_VERSION}（${NATIVE_LLAMA_PLATFORM}）"
    return
  fi

  echo "缺少目前主機的 llama-server ${LLAMA_VERSION}，開始原生編譯（${NATIVE_LLAMA_PLATFORM}）..."
  "${LLAMA_BUILD_ENTRY_SCRIPT}" \
    --source "${LLAMA_SOURCE_DIR}" \
    --output "${LLAMA_PREBUILT_DIR}/${NATIVE_LLAMA_PLATFORM}" \
    --version "${LLAMA_VERSION}"
  if ! llama_prebuilt_is_current "${NATIVE_LLAMA_PLATFORM}"; then
    echo "目前主機的 llama-server Runtime 建立失敗：${NATIVE_LLAMA_PLATFORM}" >&2
    exit 1
  fi
}

copy_available_prebuilt_runtime() {
  local platform="$1"
  local source="${LLAMA_PREBUILT_DIR}/${platform}"
  if ! llama_prebuilt_is_current "${platform}"; then
    echo "略過 ${platform} 預編譯 Runtime；部署至該平台時會由內附原始碼編譯。"
    return
  fi
  mkdir -p "${PACKAGE_DIR}/llama-server/prebuilt/${platform}"
  cp -R "${source}/." "${PACKAGE_DIR}/llama-server/prebuilt/${platform}/"
  PACKAGED_LLAMA_PLATFORMS+=("${platform}")
}

copy_mlx_source() {
  local destination="${PACKAGE_DIR}/mlx-server/source"
  mkdir -p "${destination}"
  (
    cd "${MLX_SOURCE_DIR}"
    tar --exclude='./.build' --exclude='./.swiftpm' -cf - .
  ) | (
    cd "${destination}"
    tar -xf -
  )
}

ensure_mlx_prebuilt_runtime() {
  local runtime_dir="${MLX_PREBUILT_DIR}/darwin-arm64"
  if mlx_prebuilt_is_current; then
    return
  fi
  if [[ "$(uname -s)" != "Darwin" || "$(uname -m)" != "arm64" ]]; then
    echo "缺少 darwin-arm64 mlx-server ${MLX_VERSION}；請先在 Apple Silicon 執行 ${MLX_BUILD_SCRIPT}" >&2
    exit 1
  fi
  echo "編譯 mlx-server ${MLX_VERSION}（darwin-arm64）..."
  MLX_SERVER_OUTPUT_DIR="${runtime_dir}" MLX_SERVER_VERSION="${MLX_VERSION}" "${MLX_BUILD_SCRIPT}"
}

copy_mlx_prebuilt_runtime() {
  local source="${MLX_PREBUILT_DIR}/darwin-arm64"
  require_file "${source}/bin/mlx-server"
  require_file "${source}/bin/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib"
  require_file "${source}/VERSION"
  local staged_version
  staged_version="$(tr -d '[:space:]' < "${source}/VERSION")"
  if [[ "${staged_version}" != "${MLX_VERSION}" ]]; then
    echo "darwin-arm64 mlx-server 版本不一致：預期 ${MLX_VERSION}，實際 ${staged_version}" >&2
    exit 1
  fi
  mkdir -p "${PACKAGE_DIR}/mlx-server/prebuilt/darwin-arm64"
  cp -R "${source}/." "${PACKAGE_DIR}/mlx-server/prebuilt/darwin-arm64/"
}

ensure_native_desktop_ui() {
  if [[ "$(uname -s):$(uname -m)" != "Darwin:arm64" ]]; then
    echo "部署包的 macOS 原生 UI 必須在 Apple Silicon 建立。" >&2
    exit 1
  fi
  "${DESKTOP_UI_BUILD_SCRIPT}"
  require_file "${DESKTOP_UI_PREBUILT_DIR}/TanpopoUI"
  require_file "${DESKTOP_UI_PREBUILT_DIR}/TanpopoIcon.png"
  if [[ ! -x "${DESKTOP_UI_PREBUILT_DIR}/TanpopoUI" ]]; then
    echo "macOS 原生 UI 不可執行：${DESKTOP_UI_PREBUILT_DIR}/TanpopoUI" >&2
    exit 1
  fi
}

copy_desktop_ui() {
  mkdir -p "${PACKAGE_DIR}/desktop-ui"
  cp "${DESKTOP_UI_PREBUILT_DIR}/TanpopoUI" "${PACKAGE_DIR}/desktop-ui/TanpopoUI"
  cp "${DESKTOP_UI_PREBUILT_DIR}/TanpopoIcon.png" "${PACKAGE_DIR}/desktop-ui/TanpopoIcon.png"
}

echo "=== ${APP_NAME} 建置與封裝開始 ==="
echo "專案目錄：${PROJECT_DIR}"
echo "Tanpopo 版本：${APP_VERSION} build ${APP_BUILD}"
echo "更新來源：https://github.com/${UPDATE_REPOSITORY}"
echo "llama-server 版本：${LLAMA_VERSION}"
echo "llama.cpp 原始碼：${LLAMA_SOURCE_DIR}"
echo "mlx-server 版本：${MLX_VERSION}"

go build -buildvcs=false -trimpath -ldflags "${APP_DEV_LDFLAGS}" -o "${BIN_DIR}/${APP_NAME}" ./src/cmd/llamaloader
build_target "darwin" "arm64" "${APP_NAME}_mac_arm64"
build_target "linux" "amd64" "${APP_NAME}_linux_x64"
build_target "linux" "arm64" "${APP_NAME}_linux_arm64"
ensure_native_desktop_ui
copy_desktop_ui

ensure_native_llama_prebuilt_runtime
copy_llama_source
mkdir -p "${PACKAGE_DIR}/llama-server"
printf '%s\n' "${LLAMA_VERSION}" > "${PACKAGE_DIR}/llama-server/VERSION"
cp "${RUNTIME_BUILD_SCRIPT}" "${PACKAGE_DIR}/llama-server/build-local.sh"
cp "${VULKAN_INSTALL_SCRIPT}" "${PACKAGE_DIR}/llama-server/install-vulkan-dependencies.sh"
cp "${LLAMA_BUILD_ENTRY_SCRIPT}" "${PACKAGE_DIR}/build-llama-server.sh"
for platform in "${DEFAULT_LLAMA_PLATFORMS[@]}"; do
  copy_available_prebuilt_runtime "${platform}"
done
if [[ " ${DEFAULT_LLAMA_PLATFORMS[*]} " != *" ${NATIVE_LLAMA_PLATFORM} "* ]]; then
  copy_available_prebuilt_runtime "${NATIVE_LLAMA_PLATFORM}"
fi

PACKAGED_LLAMA_TEXT="無（各平台安裝時原生編譯）"
if [[ "${#PACKAGED_LLAMA_PLATFORMS[@]}" -gt 0 ]]; then
  PACKAGED_LLAMA_TEXT="$(IFS=,; printf '%s' "${PACKAGED_LLAMA_PLATFORMS[*]}")"
fi

ensure_mlx_prebuilt_runtime
copy_mlx_source
printf '%s\n' "${MLX_VERSION}" > "${PACKAGE_DIR}/mlx-server/VERSION"
copy_mlx_prebuilt_runtime

cp -R "website" "${PACKAGE_DIR}/website"
mkdir -p "${PACKAGE_DIR}/website/reports"
cp -R "reports/." "${PACKAGE_DIR}/website/reports/"
find "${PACKAGE_DIR}/website" -type f -name '*.bak' -delete
printf '%s\n' "${APP_VERSION}" > "${PACKAGE_DIR}/VERSION"
cp "agent.sample.properties" "${PACKAGE_DIR}/agent.sample.properties"
cp README*.md "${PACKAGE_DIR}/"
cp "run.command" "${PACKAGE_DIR}/run.command"

cat > "${PACKAGE_DIR}/install.sh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_NAME="Tanpopo"
OS_NAME="$(uname -s)"
ARCH_NAME="$(uname -m)"
SOURCE_BINARY=""
PLATFORM=""

case "${OS_NAME}:${ARCH_NAME}" in
  Darwin:arm64)
    SOURCE_BINARY="${DEPLOY_DIR}/bin/${APP_NAME}_mac_arm64"
    PLATFORM="darwin-arm64"
    ;;
  Linux:x86_64|Linux:amd64)
    SOURCE_BINARY="${DEPLOY_DIR}/bin/${APP_NAME}_linux_x64"
    PLATFORM="linux-amd64"
    ;;
  Linux:aarch64|Linux:arm64)
    SOURCE_BINARY="${DEPLOY_DIR}/bin/${APP_NAME}_linux_arm64"
    PLATFORM="linux-arm64"
    ;;
  *)
    echo "Tanpopo 尚未提供此平台執行檔：${OS_NAME}/${ARCH_NAME}" >&2
    exit 1
    ;;
esac

if [[ ! -f "${SOURCE_BINARY}" ]]; then
  echo "找不到平台執行檔：${SOURCE_BINARY}" >&2
  exit 1
fi

VULKAN_INSTALL_SCRIPT="${DEPLOY_DIR}/llama-server/install-vulkan-dependencies.sh"
if [[ "${PLATFORM}" == linux-* ]]; then
  if [[ ! -x "${VULKAN_INSTALL_SCRIPT}" ]]; then
    echo "部署包缺少 Linux Vulkan 安裝腳本：${VULKAN_INSTALL_SCRIPT}" >&2
    exit 1
  fi
  "${VULKAN_INSTALL_SCRIPT}" dependencies
fi

LLAMA_BUNDLE_DIR="${DEPLOY_DIR}/llama-server"
LLAMA_BUILD_SCRIPT="${DEPLOY_DIR}/build-llama-server.sh"
LLAMA_VERSION="$(tr -d '[:space:]' < "${LLAMA_BUNDLE_DIR}/VERSION")"
LLAMA_INSTALL_ROOT="${LLAMA_CPP_INSTALL_DIR:-${HOME}/services/llama.cpp}"
LLAMA_INSTALL_VARIANT="${PLATFORM}"
if [[ "${PLATFORM}" == linux-* ]]; then
  LLAMA_INSTALL_VARIANT="${PLATFORM}-vulkan"
fi
LLAMA_VERSION_DIR="${LLAMA_INSTALL_ROOT}/versions/${LLAMA_VERSION}/${LLAMA_INSTALL_VARIANT}"
LLAMA_PREBUILT_DIR="${LLAMA_BUNDLE_DIR}/prebuilt/${PLATFORM}"

if [[ ! -x "${LLAMA_VERSION_DIR}/bin/llama-server" ]]; then
  mkdir -p "${LLAMA_INSTALL_ROOT}/versions/${LLAMA_VERSION}"
  STAGING_DIR="$(mktemp -d "${LLAMA_INSTALL_ROOT}/versions/${LLAMA_VERSION}/.install-${PLATFORM}.XXXXXX")"
  cleanup_runtime_staging() {
    if [[ -n "${STAGING_DIR:-}" && -d "${STAGING_DIR}" ]]; then
      rm -rf "${STAGING_DIR}"
    fi
  }
  trap cleanup_runtime_staging EXIT

  if [[ -x "${LLAMA_PREBUILT_DIR}/bin/llama-server" ]]; then
    echo "安裝預編譯 llama-server ${LLAMA_VERSION}（${PLATFORM}）..."
    cp -R "${LLAMA_PREBUILT_DIR}/." "${STAGING_DIR}/"
  else
    echo "沒有 ${PLATFORM} 預編譯版本，開始在目前主機編譯 llama-server..."
    "${LLAMA_BUILD_SCRIPT}" \
      --source "${LLAMA_BUNDLE_DIR}/source" \
      --output "${STAGING_DIR}" \
      --version "${LLAMA_VERSION}"
  fi

  if [[ ! -x "${STAGING_DIR}/bin/llama-server" ]]; then
    echo "llama-server 安裝結果不完整" >&2
    exit 1
  fi
  if [[ -e "${LLAMA_VERSION_DIR}" ]]; then
    echo "版本目錄已存在但內容不完整：${LLAMA_VERSION_DIR}" >&2
    exit 1
  fi
  mv "${STAGING_DIR}" "${LLAMA_VERSION_DIR}"
  STAGING_DIR=""
  trap - EXIT
fi

if [[ -e "${LLAMA_INSTALL_ROOT}/current" && ! -L "${LLAMA_INSTALL_ROOT}/current" ]]; then
  echo "無法更新 current：路徑已存在且不是符號連結 ${LLAMA_INSTALL_ROOT}/current" >&2
  exit 1
fi
CURRENT_TEMP="${LLAMA_INSTALL_ROOT}/.current.$$"
ln -s "versions/${LLAMA_VERSION}/${LLAMA_INSTALL_VARIANT}" "${CURRENT_TEMP}"
if [[ -L "${LLAMA_INSTALL_ROOT}/current" ]]; then
  rm "${LLAMA_INSTALL_ROOT}/current"
fi
mv "${CURRENT_TEMP}" "${LLAMA_INSTALL_ROOT}/current"

if [[ "${PLATFORM}" == "darwin-arm64" ]]; then
  MLX_BUNDLE_DIR="${DEPLOY_DIR}/mlx-server"
  MLX_VERSION="$(tr -d '[:space:]' < "${MLX_BUNDLE_DIR}/VERSION")"
  MLX_INSTALL_ROOT="${MLX_SERVER_INSTALL_DIR:-${HOME}/services/mlx-server}"
  MLX_VERSION_DIR="${MLX_INSTALL_ROOT}/versions/${MLX_VERSION}/${PLATFORM}"
  MLX_PREBUILT_DIR="${MLX_BUNDLE_DIR}/prebuilt/${PLATFORM}"
  if [[ ! -x "${MLX_VERSION_DIR}/bin/mlx-server" ]]; then
    if [[ ! -x "${MLX_PREBUILT_DIR}/bin/mlx-server" ]]; then
      echo "部署包缺少 ${PLATFORM} mlx-server" >&2
      exit 1
    fi
    mkdir -p "$(dirname "${MLX_VERSION_DIR}")"
    MLX_STAGING_DIR="$(mktemp -d "$(dirname "${MLX_VERSION_DIR}")/.install-${PLATFORM}.XXXXXX")"
    cp -R "${MLX_PREBUILT_DIR}/." "${MLX_STAGING_DIR}/"
    mv "${MLX_STAGING_DIR}" "${MLX_VERSION_DIR}"
  fi
  if [[ -e "${MLX_INSTALL_ROOT}/current" && ! -L "${MLX_INSTALL_ROOT}/current" ]]; then
    echo "無法更新 MLX current：路徑已存在且不是符號連結 ${MLX_INSTALL_ROOT}/current" >&2
    exit 1
  fi
  MLX_CURRENT_TEMP="${MLX_INSTALL_ROOT}/.current.$$"
  ln -s "versions/${MLX_VERSION}/${PLATFORM}" "${MLX_CURRENT_TEMP}"
  if [[ -L "${MLX_INSTALL_ROOT}/current" ]]; then
    rm "${MLX_INSTALL_ROOT}/current"
  fi
  mv "${MLX_CURRENT_TEMP}" "${MLX_INSTALL_ROOT}/current"
  echo "已啟用 mlx-server ${MLX_VERSION}：${MLX_INSTALL_ROOT}/current/bin/mlx-server"
fi

cp "${SOURCE_BINARY}" "${DEPLOY_DIR}/${APP_NAME}"
chmod +x "${DEPLOY_DIR}/${APP_NAME}"
if [[ "${PLATFORM}" == linux-* ]]; then
  PERMISSION_STATUS=0
  "${VULKAN_INSTALL_SCRIPT}" permissions || PERMISSION_STATUS=$?
  if [[ "${PERMISSION_STATUS}" -eq 20 ]]; then
    echo "Linux GPU 權限已更新；請重新登入後再次執行 run.sh。" >&2
    exit 20
  elif [[ "${PERMISSION_STATUS}" -ne 0 ]]; then
    exit "${PERMISSION_STATUS}"
  fi
fi
echo "已安裝 ${OS_NAME}/${ARCH_NAME} 執行檔：${DEPLOY_DIR}/${APP_NAME}"
echo "已啟用 llama-server ${LLAMA_VERSION}：${LLAMA_INSTALL_ROOT}/current/bin/llama-server"
EOF

cat > "${PACKAGE_DIR}/run.sh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "${DEPLOY_DIR}"

if [[ ! -x "./Tanpopo" ]]; then
  ./install.sh
fi

exec ./Tanpopo "$@"
EOF

cat > "${PACKAGE_DIR}/BUILD_INFO.txt" <<EOF
app=${APP_NAME}
app_version=${APP_VERSION}
app_build=${APP_BUILD}
app_display_version=${APP_VERSION} build ${APP_BUILD}
update_repository=${UPDATE_REPOSITORY}
built_at=${BUILT_AT}
app_targets=darwin/arm64,linux/amd64,linux/arm64
llama_server_version=${LLAMA_VERSION}
llama_server_prebuilt=${PACKAGED_LLAMA_TEXT}
llama_server_fallback=本機 CMake 原生編譯
mlx_server_version=${MLX_VERSION}
mlx_server_prebuilt=darwin/arm64（Apple Silicon 專用）
desktop_ui=darwin/arm64（AppKit／WKWebView；Linux 與 headless 使用 Shell）
entry=./run.command 或 ./run.sh
config=./agent.properties（首次啟動由 agent.sample.properties 建立）
EOF

chmod +x \
  "${PACKAGE_DIR}/install.sh" \
  "${PACKAGE_DIR}/build-llama-server.sh" \
  "${PACKAGE_DIR}/run.sh" \
  "${PACKAGE_DIR}/run.command" \
  "${PACKAGE_DIR}/desktop-ui/TanpopoUI" \
  "${PACKAGE_DIR}/llama-server/build-local.sh" \
  "${PACKAGE_DIR}/llama-server/install-vulkan-dependencies.sh" \
  "${PACKAGE_DIR}/mlx-server/prebuilt/darwin-arm64/bin/mlx-server" \
  "${PACKAGE_DIR}/bin/${APP_NAME}_mac_arm64" \
  "${PACKAGE_DIR}/bin/${APP_NAME}_linux_x64" \
  "${PACKAGE_DIR}/bin/${APP_NAME}_linux_arm64"

ARTIFACT="${DIST_DIR}/${PACKAGE_NAME}.zip"
echo "建立 ZIP：${ARTIFACT}"
(
  cd "${WORK_DIR}"
  zip -qry "${ARTIFACT}" "${PACKAGE_NAME}"
)

echo "驗證 ZIP 完整性..."
unzip -tq "${ARTIFACT}" >/dev/null

ARCHIVE_ENTRIES="$(unzip -Z1 "${ARTIFACT}")"
if grep -Eq '\.bak$' <<< "${ARCHIVE_ENTRIES}"; then
  echo "ZIP 不可包含 .bak 暫存備份" >&2
  exit 1
fi
for required_path in \
  "VERSION" \
  "README.md" \
  "agent.sample.properties" \
  "install.sh" \
  "build-llama-server.sh" \
  "run.sh" \
  "run.command" \
  "website/login.html" \
  "website/main.html" \
  "website/commands.html" \
  "website/chat.html" \
  "website/download.html" \
  "website/settings.html" \
  "website/assets/chat.js" \
  "website/assets/popular-models.json" \
  "website/assets/tanpopo-icon.png" \
  "bin/${APP_NAME}_mac_arm64" \
  "bin/${APP_NAME}_linux_x64" \
  "bin/${APP_NAME}_linux_arm64" \
  "desktop-ui/TanpopoUI" \
  "desktop-ui/TanpopoIcon.png" \
  "llama-server/VERSION" \
  "llama-server/build-local.sh" \
  "llama-server/install-vulkan-dependencies.sh" \
  "llama-server/source/CMakeLists.txt" \
  "llama-server/source/cmake/build-info.cmake" \
  "llama-server/source/common/build-info.cpp.in" \
  "llama-server/source/tools/mtmd/models/models.h" \
  "mlx-server/VERSION" \
  "mlx-server/source/Package.swift" \
  "mlx-server/prebuilt/darwin-arm64/bin/mlx-server" \
  "mlx-server/prebuilt/darwin-arm64/bin/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib"; do
  if ! grep -Fxq "${PACKAGE_NAME}/${required_path}" <<< "${ARCHIVE_ENTRIES}"; then
    echo "ZIP 缺少必要檔案：${required_path}" >&2
    exit 1
  fi
done
for platform in "${PACKAGED_LLAMA_PLATFORMS[@]}"; do
  required_path="llama-server/prebuilt/${platform}/bin/llama-server"
  if ! grep -Fxq "${PACKAGE_NAME}/${required_path}" <<< "${ARCHIVE_ENTRIES}"; then
    echo "ZIP 缺少已列入封裝的 Runtime：${required_path}" >&2
    exit 1
  fi
done

echo "=== 建置與封裝完成 ==="
echo "原生執行檔：${BIN_DIR}/${APP_NAME}"
echo "部署 ZIP：${ARTIFACT}"
