#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "${PROJECT_DIR}"

APP_VERSION="$(tr -d '[:space:]' < "${PROJECT_DIR}/VERSION")"
APP_BUILD="${TANPOPO_BUILD:-$(date +%H%M)}"
UPDATE_REPOSITORY="${TANPOPO_UPDATE_REPOSITORY:-VaderChen/Tanpopo}"
if [[ ! "${APP_BUILD}" =~ ^[0-9]{4}$ ]]; then
  echo "Tanpopo build 編號格式錯誤：${APP_BUILD}（應為 HHmm）" >&2
  exit 1
fi
APP_LDFLAGS="-X LlamaLoader/src/appversion.Version=${APP_VERSION} -X LlamaLoader/src/appversion.Build=${APP_BUILD} -X LlamaLoader/src/appversion.Repository=${UPDATE_REPOSITORY}"

"${PROJECT_DIR}/scripts/ensure-local-runtimes.sh"

exec go run -ldflags "${APP_LDFLAGS}" ./src/cmd/llamaloader \
  -config "${PROJECT_DIR}/agent.properties" \
  -sample-config "${PROJECT_DIR}/agent.sample.properties" \
  "$@"
