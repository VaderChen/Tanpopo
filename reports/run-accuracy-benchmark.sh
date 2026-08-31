#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -lt 4 ]]; then
  echo "用法：$0 <mlx|mlx-gguf|llama-gguf> <模型名稱> <模型路徑> <port> [額外 Runtime 參數...]" >&2
  echo "環境變數：ACCURACY_VARIANT、ACCURACY_DATASET、ACCURACY_RESULT_FILE、ACCURACY_SAMPLES、ACCURACY_SEED、ACCURACY_MAX_TOKENS、ACCURACY_CASE_TIMEOUT" >&2
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
VARIANT="${ACCURACY_VARIANT:-${RUNTIME}}"
SAMPLE_COUNT="${ACCURACY_SAMPLES:-100}"
SAMPLE_SEED="${ACCURACY_SEED:-0}"
MAX_TOKENS="${ACCURACY_MAX_TOKENS:-512}"
CASE_TIMEOUT="${ACCURACY_CASE_TIMEOUT:-600}"
RESULT_FILE="${ACCURACY_RESULT_FILE:-}"
RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/tanpopo-accuracy.XXXXXX")"
SERVER_LOG="${RUN_DIR}/server.log"
SERVER_PID=""

cleanup() {
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

if [[ ! -e "${MODEL_PATH}" ]]; then
  echo "找不到模型：${MODEL_PATH}" >&2
  exit 1
fi

if [[ -n "${ACCURACY_DATASET:-}" ]]; then
  DATASET="${ACCURACY_DATASET}"
else
  DATASET="${RUN_DIR}/mmlu.jsonl"
  python3 "${ROOT_DIR}/reports/prepare-accuracy-dataset.py" \
    "${DATASET}" --samples "${SAMPLE_COUNT}" --seed "${SAMPLE_SEED}" >/dev/null
fi
if [[ ! -s "${DATASET}" ]]; then
  echo "精度資料集不存在或為空：${DATASET}" >&2
  exit 1
fi

case "${RUNTIME}" in
  mlx)
    COMMAND=(
      "${MLX_SERVER}" --model "${MODEL_PATH}" --model-type text
      --host "${HOST}" --port "${PORT}" --max-kv-size 4096
      --max-tokens "${MAX_TOKENS}" --temperature 0 --top-k 1 --no-thinking
    )
    ;;
  mlx-gguf)
    COMMAND=(
      "${MLX_SERVER}" --model "${MODEL_PATH}" --model-type text
      --host "${HOST}" --port "${PORT}" --max-kv-size 4096
      --max-tokens "${MAX_TOKENS}" --temperature 0 --top-k 1 --no-thinking
      --mmap --gguf-profile auto --gguf-group-size auto
    )
    ;;
  llama-gguf)
    COMMAND=(
      "${LLAMA_SERVER}" --model "${MODEL_PATH}" --host "${HOST}" --port "${PORT}"
      --ctx-size 4096 --n-gpu-layers 999 --flash-attn on --threads 8
      --threads-batch 8 --no-webui --reasoning off --reasoning-budget 0 --jinja
    )
    ;;
  *)
    echo "不支援的精度評測 Runtime：${RUNTIME}" >&2
    exit 1
    ;;
esac
if [[ ! -x "${COMMAND[0]}" ]]; then
  echo "找不到 Runtime 執行檔：${COMMAND[0]}" >&2
  exit 1
fi
if [[ "$#" -gt 0 ]]; then
  COMMAND+=("$@")
fi

"${COMMAND[@]}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID="$!"

READY=0
for _ in {1..3600}; do
  if curl --max-time 1 --silent --fail "${BASE_URL}/v1/models" >/dev/null 2>&1; then
    READY=1
    break
  fi
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    echo "Runtime 在載入期間終止：${RUNTIME} / ${MODEL_NAME} / ${VARIANT}" >&2
    tail -100 "${SERVER_LOG}" >&2
    exit 1
  fi
  sleep 0.25
done
if [[ "${READY}" -ne 1 ]]; then
  echo "等待 Runtime 就緒逾時：${RUNTIME} / ${MODEL_NAME} / ${VARIANT}" >&2
  tail -100 "${SERVER_LOG}" >&2
  exit 1
fi

API_MODEL="$(curl --silent --fail "${BASE_URL}/v1/models" | jq -r '.data[0].id')"

request_case() {
  local record="$1"
  local response="$2"
  local question choice_a choice_b choice_c choice_d body
  question="$(jq -r '.question' <<<"${record}")"
  choice_a="$(jq -r '.choices[0]' <<<"${record}")"
  choice_b="$(jq -r '.choices[1]' <<<"${record}")"
  choice_c="$(jq -r '.choices[2]' <<<"${record}")"
  choice_d="$(jq -r '.choices[3]' <<<"${record}")"
  body="$(jq -cn \
    --arg model "${API_MODEL}" \
    --arg question "${question}" \
    --arg a "${choice_a}" --arg b "${choice_b}" --arg c "${choice_c}" --arg d "${choice_d}" \
    '{
      model: $model,
      messages: [{
        role: "user",
        content: ($question + "\n\nA. " + $a + "\nB. " + $b + "\nC. " + $c + "\nD. " + $d + "\n\nAnswer with only one letter: A, B, C, or D.")
      }],
      temperature: 0,
      top_p: 1,
      seed: 42,
      max_tokens: ($max_tokens | tonumber),
      stream: false,
      chat_template_kwargs: {enable_thinking: false}
    }' --arg max_tokens "${MAX_TOKENS}")"
  curl --max-time "${CASE_TIMEOUT}" --silent --show-error \
    --output "${response}" --write-out '%{time_total}' \
    "${BASE_URL}/v1/chat/completions" \
    --header 'Content-Type: application/json' \
    --data "${body}"
}

# 先以第一題暖機；正式評分仍會重新送出完整資料集。
FIRST_RECORD="$(head -1 "${DATASET}")"
request_case "${FIRST_RECORD}" "${RUN_DIR}/warmup.json" >/dev/null

ROWS_FILE="${RUN_DIR}/rows.csv"
TOTAL=0
EVALUATED=0
CORRECT=0
INVALID=0
LENGTH_EXCLUDED=0
SECONDS_TOTAL=0

while IFS= read -r record; do
  [[ -n "${record}" ]] || continue
  TOTAL=$((TOTAL + 1))
  response="${RUN_DIR}/response-${TOTAL}.json"
  timing="$(request_case "${record}" "${response}")"
  if ! jq -e '.choices[0].message.content != null' "${response}" >/dev/null; then
    echo "第 ${TOTAL} 題推論失敗：${MODEL_NAME} / ${VARIANT}" >&2
    cat "${response}" >&2
    exit 1
  fi

  id="$(jq -r '.id' <<<"${record}")"
  source_row="$(jq -r '.source_row' <<<"${record}")"
  category="$(jq -r '.category' <<<"${record}")"
  subject="$(jq -r '.subject' <<<"${record}")"
  expected="$(jq -r '.answer' <<<"${record}")"
  content="$(jq -r '.choices[0].message.content' "${response}")"
  finish_reason="$(jq -r '.choices[0].finish_reason // "unknown"' "${response}")"
  # 已出現 A～D 的回覆照常評分。只有在輸出因 Token 上限截斷且
  # 尚未產生可解析答案時，才將該題標示為不可評分，不當作錯答。
  predicted="$(perl -CS -e '
    $text = join("", <>);
    if ($text =~ /(?:^|[^A-Za-z])([ABCD])(?:[^A-Za-z]|$)/s) { print $1 }
  ' <<<"${content}")"
  if [[ -z "${predicted}" ]]; then
    predicted="?"
    INVALID=$((INVALID + 1))
  fi
  evaluable=1
  if [[ "${predicted}" == "?" && "${finish_reason}" == "length" ]]; then
    evaluable=0
    LENGTH_EXCLUDED=$((LENGTH_EXCLUDED + 1))
  fi
  is_correct=0
  if [[ "${evaluable}" -eq 1 ]]; then
    EVALUATED=$((EVALUATED + 1))
    if [[ "${predicted}" == "${expected}" ]]; then
      is_correct=1
      CORRECT=$((CORRECT + 1))
    fi
  fi
  SECONDS_TOTAL="$(awk -v total="${SECONDS_TOTAL}" -v value="${timing}" 'BEGIN { printf "%.6f", total + value }')"
  printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "${MODEL_NAME}" "${RUNTIME}" "${VARIANT}" "${id}" "${source_row}" \
    "${category}" "${subject}" "${expected}" "${predicted}" "${is_correct}" "${timing}" \
    "${finish_reason}" "${evaluable}" \
    >>"${ROWS_FILE}"
  printf 'CASE,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "${MODEL_NAME}" "${VARIANT}" "${id}" "${expected}" "${predicted}" \
    "${is_correct}" "${finish_reason}" "${evaluable}"
done <"${DATASET}"

ACCURACY="$(awk -v correct="${CORRECT}" -v total="${EVALUATED}" \
  'BEGIN { if (total > 0) printf "%.4f", correct / total; else print "0.0000" }')"
AVERAGE_SECONDS="$(awk -v seconds="${SECONDS_TOTAL}" -v total="${TOTAL}" 'BEGIN { printf "%.3f", seconds / total }')"

if [[ -n "${RESULT_FILE}" ]]; then
  if [[ ! -e "${RESULT_FILE}" ]]; then
    printf 'model,runtime,variant,case_id,source_row,category,subject,expected,predicted,correct,response_seconds,finish_reason,evaluable\n' \
      >"${RESULT_FILE}"
  fi
  cat "${ROWS_FILE}" >>"${RESULT_FILE}"
fi

printf 'ACCURACY,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
  "${MODEL_NAME}" "${RUNTIME}" "${VARIANT}" "${TOTAL}" "${EVALUATED}" \
  "${CORRECT}" "${ACCURACY}" "${INVALID}" "${LENGTH_EXCLUDED}"
printf 'LATENCY,%s,%s,%s,%s\n' "${MODEL_NAME}" "${VARIANT}" "${AVERAGE_SECONDS}" "${TOTAL}"
