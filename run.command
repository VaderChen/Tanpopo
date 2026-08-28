#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "$0")" || exit 1

PROJECT_DIR="$(pwd)"
UI_MODE="${TANPOPO_UI:-${OPEN_LOADER_UI:-${LLAMA_LOADER_UI:-auto}}}"
UI_MODE="$(printf '%s' "${UI_MODE}" | tr '[:upper:]' '[:lower:]')"

use_macos_ui() {
  [[ "$(uname -s)" == "Darwin" \
    && -z "${SSH_CONNECTION:-}" \
    && -z "${SSH_TTY:-}" \
    && "${UI_MODE}" != "shell" \
    && "${UI_MODE}" != "cli" \
    && "${UI_MODE}" != "headless" \
    && "${UI_MODE}" != "off" \
    && "${UI_MODE}" != "0" ]]
}

if ! use_macos_ui; then
  exec ./run.sh "$@"
fi

TERMINAL_WINDOW_ID="$(osascript -e 'tell application "Terminal" to id of front window' 2>/dev/null || true)"

show_terminal_window() {
  if [[ ! "${TERMINAL_WINDOW_ID}" =~ ^[0-9]+$ ]]; then
    return
  fi
  osascript \
    -e "tell application \"Terminal\" to set visible of (first window whose id is ${TERMINAL_WINDOW_ID}) to true" \
    -e "tell application \"Terminal\" to set miniaturized of (first window whose id is ${TERMINAL_WINDOW_ID}) to false" \
    -e 'tell application "Terminal" to activate' \
    >/dev/null 2>&1 || true
}

handle_launch_error() {
  local status="${1:-1}"
  trap - ERR
  show_terminal_window
  echo "Tanpopo GUI 啟動失敗；Terminal 已恢復顯示以供檢查。" >&2
  exit "${status}"
}

trap 'handle_launch_error $?' ERR

if [[ "${TERMINAL_WINDOW_ID}" =~ ^[0-9]+$ ]]; then
  osascript \
    -e "tell application \"Terminal\" to set visible of (first window whose id is ${TERMINAL_WINDOW_ID}) to false" \
    >/dev/null 2>&1 || true
fi

if [[ -x "${PROJECT_DIR}/scripts/ensure-local-runtimes.sh" ]]; then
  "${PROJECT_DIR}/scripts/ensure-local-runtimes.sh"
  mkdir -p "${PROJECT_DIR}/bin" "${PROJECT_DIR}/data"
  go build -buildvcs=false -trimpath -o "${PROJECT_DIR}/bin/Tanpopo" ./src/cmd/llamaloader
  TANPOPO_BINARY="${PROJECT_DIR}/bin/Tanpopo"
else
  if [[ ! -x "${PROJECT_DIR}/Tanpopo" ]]; then
    "${PROJECT_DIR}/install.sh"
  fi
  mkdir -p "${PROJECT_DIR}/data"
  TANPOPO_BINARY="${PROJECT_DIR}/Tanpopo"
fi

LOG_FILE="${PROJECT_DIR}/data/tanpopo.log"
nohup env TANPOPO_UI=gui "${TANPOPO_BINARY}" \
  -config "${PROJECT_DIR}/agent.properties" \
  -sample-config "${PROJECT_DIR}/agent.sample.properties" \
  "$@" >>"${LOG_FILE}" 2>&1 </dev/null &
TANPOPO_PID=$!

UI_STARTED="false"
for _ in {1..60}; do
  if ! kill -0 "${TANPOPO_PID}" 2>/dev/null; then
    break
  fi
  if pgrep -P "${TANPOPO_PID}" -f 'TanpopoUI|OpenLoaderUI' >/dev/null 2>&1; then
    UI_STARTED="true"
    break
  fi
  sleep 0.1
done

if [[ "${UI_STARTED}" != "true" ]]; then
  if kill -0 "${TANPOPO_PID}" 2>/dev/null; then
    kill "${TANPOPO_PID}" 2>/dev/null || true
  fi
  echo "Tanpopo 原生 UI 啟動失敗，詳細紀錄：${LOG_FILE}" >&2
  tail -n 30 "${LOG_FILE}" >&2 || true
  handle_launch_error 1
fi

echo "Tanpopo UI 已啟動，Terminal 視窗即將關閉。"
if [[ "${TERMINAL_WINDOW_ID}" =~ ^[0-9]+$ ]]; then
  nohup osascript \
    -e 'delay 0.4' \
    -e "tell application \"Terminal\" to close (first window whose id is ${TERMINAL_WINDOW_ID})" \
    >/dev/null 2>&1 &
fi
exit 0
