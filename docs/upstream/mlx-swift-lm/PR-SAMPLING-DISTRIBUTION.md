# Sampling distribution PR 草稿

建議標題：`Expose prepared categorical distributions for samplers`

以下英文內容可貼入官方 PR。核取方塊刻意保留未勾選；請先檢查實際 diff、測試結果與措辭。

---

## Proposed changes

This change adds a small, sampler-level capability needed by lossless stochastic speculative decoding:

- Add `PreparedDistribution`, representing either unnormalized categorical log weights or no prepared distribution.
- Extend `LogitSampler` with defaulted `prepare(logits:)` and `sample(prepared:logits:)` operations, so callers can use one uniform two-phase flow without capability casts.
- Make `TopPSampler` and `CategoricalSampler` prepare the exact log weights passed to `categorical`, while preserving their existing random-state behavior.
- Keep `ArgMaxSampler` on the default one-shot path because greedy decoding is not a categorical distribution.
- Add tests covering temperature scaling, top-k filtering, normalization through `softmax`, prepared sampling equivalence, and uniform use through `LogitSampler`.

## Update since review

Following reviewer feedback, this revision replaces the separate capability
protocol with a uniform two-phase API on `LogitSampler`:

- `PreparedDistribution` represents either reusable log weights or `.none`.
- `prepare(logits:)` and `sample(prepared:logits:)` have defaults, so callers
  do not need capability casts or optional prepared values.
- `TopPSampler` and `CategoricalSampler` consume the exact prepared log weights,
  while `ArgMaxSampler` keeps the default one-shot behavior.
- Existing `sample(logits:)` call sites remain source-compatible.

The returned values are intentionally unnormalized. A speculative decoder can apply `softmax` when it needs token probabilities for an acceptance ratio or residual distribution, while the normal generation path avoids a redundant `softmax` followed by `log` before `categorical`.

This is a preparatory API only. It does not enable stochastic MTP by itself and does not change the generated-token path for existing callers. Existing `sample(logits:)` conformances and call sites remain source-compatible because the new protocol operations have defaults.

## Checklist

- [ ] I have read the [CONTRIBUTING](https://github.com/ml-explore/mlx-swift-lm/blob/main/CONTRIBUTING.md) document
- [ ] I have run `pre-commit run --all-files` to format my code / installed pre-commit prior to committing changes
- [ ] I have added tests that prove my fix is effective or that my feature works
- [ ] I have updated the necessary documentation (if needed)

## Validation performed

```text
xcodebuild test \
  -scheme mlx-swift-lm-Package \
  -destination 'platform=macOS' \
  -skipPackagePluginValidation \
  -only-testing:MLXLMTests/SampleTests

Executed 37 tests, with 0 failures.
```

The complete package test command also passed: all test targets succeeded (`** TEST SUCCEEDED **`).

`scripts/verify-docs.sh` successfully built the changed `MLXLMCommon` documentation target. The full script still reports the repository's existing MLXCXGrammar ExtractAPI failure under the local Xcode 27 SDK (`<algorithm>` not found).

## AI usage

- [ ] I have read this PR description in full and approve it as my own, and it accurately describes the code changes.
- AI usage disclosure: OpenAI Codex assisted with comparing the Tanpopo prototype against current `mlx-swift-lm`, drafting the sampler protocol refactor and tests, reviewing API compatibility and documentation, and preparing this PR description. I reviewed and understand every submitted line.

---

## 送出前檢查

1. 在工作副本執行完整 `pre-commit run --all-files`（已使用 swift-format 603.0.0，通過）。
2. 完整官方 unit test 已通過；`MLXLMCommon` DocC 已通過。`scripts/verify-docs.sh` 的 MLXCXGrammar ExtractAPI 錯誤是本機 Xcode 27 SDK 既有問題。
3. 逐字閱讀英文文案後，才自行勾選 AI 與認可欄位。
