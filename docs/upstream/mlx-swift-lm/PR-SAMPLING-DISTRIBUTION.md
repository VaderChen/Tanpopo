# Sampling distribution PR 草稿

建議標題：`Expose sampling log weights for lossless speculative decoding`

以下英文內容可貼入官方 PR。核取方塊刻意保留未勾選；請先檢查實際 diff、測試結果與措辭。

---

## Proposed changes

This change adds a small, sampler-level capability needed by lossless stochastic speculative decoding:

- Introduce `LogitDistributionSampler`, which exposes the unnormalized log weights used by a categorical sampler.
- Make `TopPSampler` and `CategoricalSampler` conform without changing their sampling path: both still call `categorical` with the same log weights and the same random state.
- Keep `ArgMaxSampler` outside this protocol because greedy decoding is not a categorical distribution.
- Add tests covering temperature scaling, top-k filtering, normalization through `softmax`, and capability selection from `GenerateParameters`.

The returned values are intentionally unnormalized. A speculative decoder can apply `softmax` when it needs token probabilities for an acceptance ratio or residual distribution, while the normal generation path avoids a redundant `softmax` followed by `log` before `categorical`.

This is a preparatory API only. It does not enable stochastic MTP by itself and does not change the generated-token path for existing callers.

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

Executed 35 tests, with 0 failures.
```

## AI usage

- [ ] I have read this PR description in full and approve it as my own, and it accurately describes the code changes.
- AI usage disclosure: OpenAI Codex assisted with comparing the Tanpopo prototype against current `mlx-swift-lm`, drafting the protocol refactor and tests, and preparing this PR description. I reviewed and understand every submitted line.

---

## 送出前檢查

1. 在工作副本執行完整 `pre-commit run --all-files`；目前已使用 Xcode 內建 `swift-format` 格式化兩個修改檔案，但本機尚未安裝 `pre-commit`。
2. 再跑一次完整官方 unit test；目前只跑過相關的 `SampleTests`。
3. 確認 `LogitDistributionSampler` 命名是否符合維護者偏好。若官方希望避免新增 public protocol，可改成 SPI 或先放在 speculative decoding namespace。
4. 逐字閱讀英文文案後，才自行勾選 AI 與認可欄位。
