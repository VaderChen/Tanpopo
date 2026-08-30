#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -lt 4 ]]; then
  echo "用法：$0 <mlx|mlx-gguf|llama-gguf> <模型名稱> <模型路徑> <port> [額外 Runtime 參數...]" >&2
  exit 1
fi

RUNTIME="$1"
MODEL_NAME="$2"
MODEL_PATH="$3"
PORT="$4"
shift 4
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
MLX_SERVER="${ROOT_DIR}/mlx-server/.build/out/Products/Release/mlx-server"
LLAMA_SERVER="${ROOT_DIR}/llama-runtime/prebuilt/darwin-arm64/bin/llama-server"
HOST="127.0.0.1"
BASE_URL="http://${HOST}:${PORT}"
RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/tanpopo-benchmark.XXXXXX")"
SERVER_LOG="${RUN_DIR}/server.log"
RSS_LOG="${RUN_DIR}/rss.log"
SERVER_PID=""
MONITOR_PID=""

cleanup() {
  if [[ -n "${MONITOR_PID}" ]]; then
    kill "${MONITOR_PID}" 2>/dev/null || true
    wait "${MONITOR_PID}" 2>/dev/null || true
  fi
  if [[ -n "${SERVER_PID}" ]]; then
    kill -INT "${SERVER_PID}" 2>/dev/null || true
    for _ in {1..40}; do
      kill -0 "${SERVER_PID}" 2>/dev/null || break
      sleep 0.25
    done
    kill -TERM "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${RUN_DIR}"
}
trap cleanup EXIT INT TERM

now_seconds() {
  perl -MTime::HiRes=time -e 'printf "%.6f", time'
}

if [[ ! -e "${MODEL_PATH}" ]]; then
  echo "找不到模型：${MODEL_PATH}" >&2
  exit 1
fi

case "${RUNTIME}" in
  mlx)
    COMMAND=(
      "${MLX_SERVER}" --model "${MODEL_PATH}" --model-type text
      --host "${HOST}" --port "${PORT}" --max-kv-size 4096
      --max-tokens 128 --temperature 0 --top-k 1 --no-thinking
    )
    ;;
  mlx-gguf)
    COMMAND=(
      "${MLX_SERVER}" --model "${MODEL_PATH}" --model-type text
      --host "${HOST}" --port "${PORT}" --max-kv-size 4096
      --max-tokens 128 --temperature 0 --top-k 1 --no-thinking
      --mmap --gguf-profile auto --gguf-group-size auto
    )
    ;;
  llama-gguf)
    COMMAND=(
      "${LLAMA_SERVER}" --model "${MODEL_PATH}" --host "${HOST}" --port "${PORT}"
      --ctx-size 4096 --n-gpu-layers 999 --flash-attn on --threads 8
      --threads-batch 8 --no-webui --metrics
    )
    ;;
  *)
    echo "不支援的 Runtime：${RUNTIME}" >&2
    exit 1
    ;;
esac

if [[ "$#" -gt 0 ]]; then
  COMMAND+=("$@")
fi

STARTED_AT="$(now_seconds)"
"${COMMAND[@]}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID="$!"

(
  while kill -0 "${SERVER_PID}" 2>/dev/null; do
    ps -o rss= -p "${SERVER_PID}" 2>/dev/null | tr -d ' ' >>"${RSS_LOG}" || true
    sleep 0.2
  done
) &
MONITOR_PID="$!"

READY=0
for _ in {1..3600}; do
  if curl --max-time 1 --silent --fail "${BASE_URL}/v1/models" >/dev/null 2>&1; then
    READY=1
    break
  fi
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    echo "Runtime 在載入期間終止：${RUNTIME} / ${MODEL_NAME}" >&2
    tail -80 "${SERVER_LOG}" >&2
    exit 1
  fi
  sleep 0.25
done
if [[ "${READY}" -ne 1 ]]; then
  echo "等待 Runtime 就緒逾時：${RUNTIME} / ${MODEL_NAME}" >&2
  tail -80 "${SERVER_LOG}" >&2
  exit 1
fi

READY_AT="$(now_seconds)"
LOAD_SECONDS="$(awk -v start="${STARTED_AT}" -v ready="${READY_AT}" 'BEGIN { printf "%.3f", ready - start }')"
API_MODEL="$(curl --silent --fail "${BASE_URL}/v1/models" | jq -r '.data[0].id')"
REQUEST_BODY="$(jq -cn --arg model "${API_MODEL}" '{
  model: $model,
  messages: [{
    role: "user",
    content: "請以繁體中文撰寫至少四百字的連續技術說明，主題是本機大型語言模型推論的記憶體管理。不要列點，也不要提及這是效能測試。"
  }],
  temperature: 0,
  top_p: 1,
  max_tokens: 128,
  stream: false,
  chat_template_kwargs: {enable_thinking: false}
}')"

request_once() {
  local index="$1"
  local response="${RUN_DIR}/response-${index}.json"
  local timing
  timing="$(curl --max-time 600 --silent --show-error \
    --output "${response}" --write-out '%{time_total}' \
    "${BASE_URL}/v1/chat/completions" \
    --header 'Content-Type: application/json' \
    --data "${REQUEST_BODY}")"
  if ! jq -e '.choices[0].message.content != null' "${response}" >/dev/null; then
    echo "推論失敗：${RUNTIME} / ${MODEL_NAME}" >&2
    cat "${response}" >&2
    return 1
  fi
  local tokens server_tps wall_tps finish_reason
  tokens="$(jq -r '.usage.completion_tokens // .timings.predicted_n // 0' "${response}")"
  server_tps="$(jq -r '.usage.tokens_per_second // .timings.predicted_per_second // 0' "${response}")"
  wall_tps="$(awk -v tokens="${tokens}" -v seconds="${timing}" \
    'BEGIN { if (seconds > 0) printf "%.3f", tokens / seconds; else print "0" }')"
  finish_reason="$(jq -r '.choices[0].finish_reason // "unknown"' "${response}")"
  printf '%s,%s,%s,%s,%s,%s\n' \
    "${index}" "${tokens}" "${timing}" "${server_tps}" "${wall_tps}" "${finish_reason}"
}

# 第一次只做 Metal kernel、tokenizer 與 KV cache 暖機，不列入統計。
request_once warmup >/dev/null

RUN_OUTPUT="${RUN_DIR}/runs.csv"
for index in 1 2 3; do
  request_once "${index}" >>"${RUN_OUTPUT}"
done

PEAK_RSS_KIB="$(awk 'BEGIN { max = 0 } $1 > max { max = $1 } END { print max }' "${RSS_LOG}")"
PEAK_RSS_GIB="$(awk -v kib="${PEAK_RSS_KIB}" 'BEGIN { printf "%.3f", kib / 1048576 }')"
AVERAGE_SECONDS="$(awk -F, '{ total += $3 } END { printf "%.3f", total / NR }' "${RUN_OUTPUT}")"
MEDIAN_SERVER_TPS="$(awk -F, '{ print $4 }' "${RUN_OUTPUT}" | sort -n | sed -n '2p')"
MEDIAN_WALL_TPS="$(awk -F, '{ print $5 }' "${RUN_OUTPUT}" | sort -n | sed -n '2p')"
AVERAGE_TOKENS="$(awk -F, '{ total += $2 } END { printf "%.1f", total / NR }' "${RUN_OUTPUT}")"
SUMMARY="${AVERAGE_SECONDS},${MEDIAN_SERVER_TPS},${MEDIAN_WALL_TPS},${AVERAGE_TOKENS}"

printf 'RESULT,%s,%s,%s,%s,%s,%s\n' \
  "${MODEL_NAME}" "${RUNTIME}" "${LOAD_SECONDS}" "${PEAK_RSS_GIB}" \
  "${SUMMARY}" "$(tr '\n' ' ' <"${SERVER_LOG}" | tail -c 320)"
printf 'RUNS,%s,%s,' "${MODEL_NAME}" "${RUNTIME}"
tr '\n' ';' <"${RUN_OUTPUT}"
printf '\n'
