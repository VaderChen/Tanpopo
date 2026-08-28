#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "${PROJECT_DIR}"

"${PROJECT_DIR}/scripts/ensure-local-runtimes.sh"

exec go run ./src/cmd/llamaloader \
  -config "${PROJECT_DIR}/agent.properties" \
  -sample-config "${PROJECT_DIR}/agent.sample.properties" \
  "$@"
