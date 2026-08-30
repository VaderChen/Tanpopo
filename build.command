#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "$0")" || exit 1

if [[ "${1:-}" == "--runtime" ]]; then
  shift
  exec ./scripts/ensure-local-runtimes.sh "$@"
fi

exec ./build.sh "$@"
