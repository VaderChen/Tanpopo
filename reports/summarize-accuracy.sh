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
  function field(name, fallback) {
    return (name in column) ? $(column[name]) : fallback
  }
  NR == 1 {
    for (columnIndex = 1; columnIndex <= NF; columnIndex++) {
      headerName = $columnIndex
      gsub(/\r$/, "", headerName)
      column[headerName] = columnIndex
    }
    required[1] = "model"
    required[2] = "variant"
    required[3] = "case_id"
    required[4] = "predicted"
    required[5] = "correct"
    for (requiredIndex = 1; requiredIndex <= 5; requiredIndex++) {
      if (!(required[requiredIndex] in column)) {
        print "精度 CSV 缺少欄位：" required[requiredIndex] > "/dev/stderr"
        exit 2
      }
    }
    next
  }
  {
    gsub(/\r$/, "", $NF)
    model = field("model", "")
    variant = field("variant", "")
    caseID = field("case_id", "")
    prediction = field("predicted", "?")
    correct = field("correct", 0) + 0
    finishReason = field("finish_reason", "unknown")
    evaluable = field("evaluable", 1) + 0
    responseSeconds = field("response_seconds", 0) + 0
    key = model SUBSEP variant

    total[key]++
    responseTotal[key] += responseSeconds
    if (evaluable) {
      evaluated[key]++
      passed[key] += correct
    }
    if (prediction == "?") invalid[key]++
    if (!evaluable && finishReason == "length") lengthExcluded[key]++

    caseKey = model SUBSEP variant SUBSEP caseID
    predictionByCase[caseKey] = prediction
    evaluableByCase[caseKey] = evaluable
    variantSeen[model SUBSEP variant] = 1
  }
  END {
    print "model,variant,total,evaluated,correct,accuracy,invalid,length_excluded,retention_vs_native,agreement_with_native,agreement_with_llama,delta_vs_native_pp,delta_vs_llama_pp,average_response_seconds"
    for (mv in variantSeen) {
      split(mv, parts, SUBSEP)
      model = parts[1]
      variant = parts[2]
      key = model SUBSEP variant
      accuracy = evaluated[key] > 0 ? passed[key] / evaluated[key] : 0
      averageSeconds = total[key] > 0 ? responseTotal[key] / total[key] : 0
      nativeVariant = ""
      llamaVariant = ""
      for (candidate in variantSeen) {
        split(candidate, candidateParts, SUBSEP)
        if (candidateParts[1] == model && isNative(candidateParts[2])) {
          nativeVariant = candidateParts[2]
        }
        if (candidateParts[1] == model && isLlama(candidateParts[2])) {
          llamaVariant = candidateParts[2]
        }
      }

      nativeAgreement = 0
      llamaAgreement = 0
      retention = 0
      nativeDelta = 0
      llamaDelta = 0
      if (!isNative(variant) && nativeVariant != "") {
        same = 0
        compared = 0
        for (predictionKey in predictionByCase) {
          split(predictionKey, predictionParts, SUBSEP)
          if (predictionParts[1] == model && predictionParts[2] == variant) {
            caseID = predictionParts[3]
            nativeKey = model SUBSEP nativeVariant SUBSEP caseID
            if ((nativeKey in predictionByCase) &&
                evaluableByCase[predictionKey] &&
                evaluableByCase[nativeKey] &&
                predictionByCase[predictionKey] != "?" &&
                predictionByCase[nativeKey] != "?") {
              compared++
              if (predictionByCase[predictionKey] == predictionByCase[nativeKey]) same++
            }
          }
        }
        if (compared > 0) nativeAgreement = same / compared
        nativeSummaryKey = model SUBSEP nativeVariant
        if (nativeSummaryKey in total && evaluated[nativeSummaryKey] > 0) {
          nativeAccuracy = passed[nativeSummaryKey] / evaluated[nativeSummaryKey]
          nativeDelta = (accuracy - nativeAccuracy) * 100
          if (nativeAccuracy > 0) retention = accuracy / nativeAccuracy
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
            if ((llamaKey in predictionByCase) &&
                evaluableByCase[predictionKey] &&
                evaluableByCase[llamaKey] &&
                predictionByCase[predictionKey] != "?" &&
                predictionByCase[llamaKey] != "?") {
              compared++
              if (predictionByCase[predictionKey] == predictionByCase[llamaKey]) same++
            }
          }
        }
        if (compared > 0) llamaAgreement = same / compared
        llamaSummaryKey = model SUBSEP llamaVariant
        if (llamaSummaryKey in total && evaluated[llamaSummaryKey] > 0) {
          llamaAccuracy = passed[llamaSummaryKey] / evaluated[llamaSummaryKey]
          llamaDelta = (accuracy - llamaAccuracy) * 100
        }
      }

      if (isNative(variant)) {
        retention = 1
        nativeAgreement = 1
      }
      if (isLlama(variant)) llamaAgreement = 1
      outputVariant = isNative(variant) ? "native" : (isLlama(variant) ? "llama-reference" : variant)
      printf "%s,%s,%d,%d,%d,%.4f,%d,%d,%.4f,%.4f,%.4f,%.2f,%.2f,%.3f\n", \
        model, outputVariant, total[key], evaluated[key] + 0, passed[key] + 0, accuracy, \
        invalid[key] + 0, lengthExcluded[key] + 0, retention, nativeAgreement, \
        llamaAgreement, nativeDelta, llamaDelta, averageSeconds
    }
  }
' "${RESULTS}" | {
  IFS= read -r header
  printf '%s\n' "${header}"
  sort -t, -k1,1 -k2,2
}
