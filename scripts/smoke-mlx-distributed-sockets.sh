#!/usr/bin/env bash
set -euo pipefail
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
MLX_SOURCE="${PROJECT_DIR}/mlx-server/.build/checkouts/mlx-swift/Source/Cmlx/mlx"
if ! grep -Fq 'TANPOPO_MLX_TCP_CONNECT_ERRORS' "${MLX_SOURCE}/mlx/distributed/utils.cpp"; then
  echo "請先執行 scripts/build-mlx-server-runtime.sh 套用連線補丁。" >&2
  exit 1
fi
SMOKE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/tanpopo-sockets.XXXXXX")"
trap 'rm -rf "${SMOKE_DIR}"' EXIT
"${CXX:-c++}" -std=c++17 -pthread -I "${MLX_SOURCE}" \
  "${PROJECT_DIR}/tests/native/distributed_sockets.cpp" \
  "${MLX_SOURCE}/mlx/distributed/utils.cpp" -o "${SMOKE_DIR}/smoke"
"${SMOKE_DIR}/smoke"
MLX_RING="${MLX_SOURCE}/mlx/distributed/ring/ring.cpp"
if ! grep -Fq 'TANPOPO_MLX_TCP_QUEUE_POLL' "${MLX_RING}"; then
  echo "請先重新建置 Runtime，套用 TCP 事件等待補丁。" >&2
  exit 1
fi
# 直接編譯目前依賴中的真實 SocketThread，避免另寫一份實作來測自己。
awk '/^class SocketThread \{/ {copying=1} /^class CommunicationThreads \{/ {copying=0} copying {print}' \
  "${MLX_RING}" > "${SMOKE_DIR}/ring-socket-thread.h"
"${CXX:-c++}" -std=c++17 -pthread -O1 -g -fsanitize=thread -I "${SMOKE_DIR}" \
  "${PROJECT_DIR}/tests/native/ring_queues.cpp" -o "${SMOKE_DIR}/queues"
TSAN_OPTIONS=halt_on_error=1 "${SMOKE_DIR}/queues"
TSAN_OPTIONS=halt_on_error=1 "${SMOKE_DIR}/queues" --waiting
for direction in recv send; do
  result=0
  TSAN_OPTIONS=halt_on_error=1 "${SMOKE_DIR}/queues" "--peer-${direction}" > "${SMOKE_DIR}/peer-${direction}.log" 2>&1 || result=$?
  if [[ "${result}" != 70 ]] || ! grep -Fq 'mlx-server error: [ring]' "${SMOKE_DIR}/peer-${direction}.log"; then
    cat "${SMOKE_DIR}/peer-${direction}.log" >&2
    echo "斷線 Smoke 失敗：${direction}，狀態 ${result}" >&2
    exit 1
  fi
done
echo "ThreadSanitizer 佇列同步與雙向斷線 Smoke 通過"
