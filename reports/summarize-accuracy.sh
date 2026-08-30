#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 1 ]]; then
  echo "用法：$0 <accuracy-results.csv>" >&2
  exit 1
fi

RESULTS="$1"
if [[ ! -s "${RESULTS}" ]]; then
  echo "找不到精度結果：${RESULTS}" >&2
  exit 1
fi

awk -F, '
  NR == 1 { next }
  {
    model = $1
    variant = $3
    caseID = $4
    prediction = $9
    correct = $10 + 0
    key = model SUBSEP variant
    total[key]++
    passed[key] += correct
    if (prediction == "?") invalid[key]++
    predictionByCase[model SUBSEP variant SUBSEP caseID] = prediction
    modelSeen[model] = 1
    variantSeen[model SUBSEP variant] = 1
  }
  END {
    print "model,variant,total,correct,accuracy,invalid,agreement_with_native,agreement_with_llama,delta_vs_native_pp,delta_vs_llama_pp,quality_gate"
    for (mv in variantSeen) {
      split(mv, parts, SUBSEP)
      model = parts[1]
      variant = parts[2]
      key = model SUBSEP variant
      accuracy = passed[key] / total[key]
      nativeAgreement = 1
      llamaAgreement = 1
      nativeDelta = 0
      llamaDelta = 0
      gate = (variant == "native" || variant == "llama-reference") ? "baseline" : "missing-reference"
      if (variant != "native") {
        same = 0
        compared = 0
        for (predictionKey in predictionByCase) {
          split(predictionKey, predictionParts, SUBSEP)
          if (predictionParts[1] == model && predictionParts[2] == variant) {
            caseID = predictionParts[3]
            nativeKey = model SUBSEP "native" SUBSEP caseID
            if (nativeKey in predictionByCase) {
              compared++
              if (predictionByCase[predictionKey] == predictionByCase[nativeKey]) same++
            }
          }
        }
        if (compared > 0) nativeAgreement = same / compared
        nativeSummaryKey = model SUBSEP "native"
        if (nativeSummaryKey in total) {
          nativeAccuracy = passed[nativeSummaryKey] / total[nativeSummaryKey]
          nativeDelta = (accuracy - nativeAccuracy) * 100
        }
      }
      if (variant != "llama-reference") {
        same = 0
        compared = 0
        for (predictionKey in predictionByCase) {
          split(predictionKey, predictionParts, SUBSEP)
          if (predictionParts[1] == model && predictionParts[2] == variant) {
            caseID = predictionParts[3]
            llamaKey = model SUBSEP "llama-reference" SUBSEP caseID
            if (llamaKey in predictionByCase) {
              compared++
              if (predictionByCase[predictionKey] == predictionByCase[llamaKey]) same++
            }
          }
        }
        if (compared > 0) llamaAgreement = same / compared
        llamaSummaryKey = model SUBSEP "llama-reference"
        if (llamaSummaryKey in total) {
          llamaAccuracy = passed[llamaSummaryKey] / total[llamaSummaryKey]
          llamaDelta = (accuracy - llamaAccuracy) * 100
          if (variant != "native") {
            # fastGGUF 可接受最多 2 個百分點的精度下降；無效答案不得比
            # 相同 GGUF 的 llama 基準多超過 1 題。逐題一致率保留作診斷，
            # 不直接當門檻，因為不同 Runtime 可能以不同答案達成相同正確率。
            gate = (llamaDelta >= -2.000001 && invalid[key] <= invalid[llamaSummaryKey] + 1) \
              ? "pass" : "fail"
          }
        }
      }
      printf "%s,%s,%d,%d,%.4f,%d,%.4f,%.4f,%.2f,%.2f,%s\n", \
        model, variant, total[key], passed[key], accuracy, invalid[key] + 0, \
        nativeAgreement, llamaAgreement, nativeDelta, llamaDelta, gate
    }
  }
' "${RESULTS}" | {
  IFS= read -r header
  printf '%s\n' "${header}"
  sort -t, -k1,1 -k2,2
}
