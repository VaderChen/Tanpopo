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
  function isNative(variant) {
    return variant == "native" || variant == "native-mlx"
  }
  function isLlama(variant) {
    return variant == "llama-reference" || variant == "llama-gguf"
  }
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
    print "model,variant,total,correct,accuracy,invalid,retention_vs_native,agreement_with_native,agreement_with_llama,delta_vs_native_pp,delta_vs_llama_pp,quality_gate"
    for (mv in variantSeen) {
      split(mv, parts, SUBSEP)
      model = parts[1]
      variant = parts[2]
      key = model SUBSEP variant
      accuracy = passed[key] / total[key]
      nativeVariant = ""
      llamaVariant = ""
      for (candidate in variantSeen) {
        split(candidate, candidateParts, SUBSEP)
        if (candidateParts[1] == model && isNative(candidateParts[2])) nativeVariant = candidateParts[2]
        if (candidateParts[1] == model && isLlama(candidateParts[2])) llamaVariant = candidateParts[2]
      }
      nativeAgreement = 0
      llamaAgreement = 0
      retention = 0
      nativeDelta = 0
      llamaDelta = 0
      gate = (isNative(variant) || isLlama(variant)) ? "baseline" : "missing-reference"
      if (!isNative(variant) && nativeVariant != "") {
        same = 0
        compared = 0
        for (predictionKey in predictionByCase) {
          split(predictionKey, predictionParts, SUBSEP)
          if (predictionParts[1] == model && predictionParts[2] == variant) {
            caseID = predictionParts[3]
            nativeKey = model SUBSEP nativeVariant SUBSEP caseID
            if (nativeKey in predictionByCase) {
              compared++
              if (predictionByCase[predictionKey] == predictionByCase[nativeKey]) same++
            }
          }
        }
        if (compared > 0) nativeAgreement = same / compared
        nativeSummaryKey = model SUBSEP nativeVariant
        if (nativeSummaryKey in total) {
          nativeAccuracy = passed[nativeSummaryKey] / total[nativeSummaryKey]
          nativeDelta = (accuracy - nativeAccuracy) * 100
          if (nativeAccuracy > 0) retention = accuracy / nativeAccuracy
          # 使用者接受少量精度犧牲，但快速策略至少必須保留同模型
          # 原生 MLX 基準的 90%；無效答案已自然計入 accuracy。
          if (!isLlama(variant)) {
            gate = (retention >= 0.899999) ? "pass" : "fail"
          }
        }
      }
      if (!isLlama(variant) && llamaVariant != "") {
        same = 0
        compared = 0
        for (predictionKey in predictionByCase) {
          split(predictionKey, predictionParts, SUBSEP)
          if (predictionParts[1] == model && predictionParts[2] == variant) {
            caseID = predictionParts[3]
            llamaKey = model SUBSEP llamaVariant SUBSEP caseID
            if (llamaKey in predictionByCase) {
              compared++
              if (predictionByCase[predictionKey] == predictionByCase[llamaKey]) same++
            }
          }
        }
        if (compared > 0) llamaAgreement = same / compared
        llamaSummaryKey = model SUBSEP llamaVariant
        if (llamaSummaryKey in total) {
          llamaAccuracy = passed[llamaSummaryKey] / total[llamaSummaryKey]
          llamaDelta = (accuracy - llamaAccuracy) * 100
        }
      }
      if (isNative(variant)) retention = 1
      if (isNative(variant)) nativeAgreement = 1
      if (isLlama(variant)) llamaAgreement = 1
      outputVariant = isNative(variant) ? "native" : (isLlama(variant) ? "llama-reference" : variant)
      printf "%s,%s,%d,%d,%.4f,%d,%.4f,%.4f,%.4f,%.2f,%.2f,%s\n", \
        model, outputVariant, total[key], passed[key], accuracy, invalid[key] + 0, \
        retention, nativeAgreement, llamaAgreement, nativeDelta, llamaDelta, gate
    }
  }
' "${RESULTS}" | {
  IFS= read -r header
  printf '%s\n' "${header}"
  sort -t, -k1,1 -k2,2
}
