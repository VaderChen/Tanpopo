#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "$0")" || exit 1

# 開發環境會先建置 Runtime；部署包則會透過 install.sh 安裝 Runtime。
exec ./run.sh "$@"
