# DFlash 設計 Issue 草稿

建議標題：`[Feature] Generalize MTP speculative decoding for DFlash-style block proposals`

這一題建議先開設計 Issue，不直接送出完整 DFlash PR。官方 main 已有成熟的 `MTPSpeculativeTokenIterator`、staged cache round 與 Qwen hybrid rewind；應擴充現有架構，而不是再引入一套平行 iterator。

以下英文內容可貼入官方 `Other` Issue：

---

- [ ] I have read this issue in full and approve it as my own, however it was drafted.

## Motivation

`mlx-swift-lm` now has a capable MTP speculative decoding path, including staged KV-cache rounds, target-emitted drafter state, stateful Qwen drafters, telemetry, and bounded native rewind for hybrid recurrent caches.

I have a working Tanpopo prototype for DFlash 1 and DFlash 2 on Qwen-family text models. The prototype shows that most of the runtime mechanics can be expressed as extensions of the existing MTP path, but four generic capabilities are still needed:

1. A draft proposal that can carry tokens plus either dense or sparse proposal probabilities.
2. Exact probability-ratio acceptance and residual sampling for non-greedy generation.
3. Target-side, opt-in capture of the intermediate hidden layers requested by a drafter.
4. A cache transaction that commits an accepted prefix across both trimmable KV caches and recurrent GDN/Mamba-style state.

The current `requiresGreedySampling` fallback is a useful safety boundary. The proposal below would preserve that boundary until a drafter provides all probability and rollback capabilities required for lossless stochastic decoding.

## Proposed staged design

### 1. Sampling distribution capability

Expose the exact categorical log weights used by `TopPSampler` and `CategoricalSampler`. This lets verifier code derive the target distribution after the normal `LogitProcessor` chain without duplicating top-p, min-p, top-k, temperature, or bfloat16 handling.

### 2. Probability-bearing draft proposals

Add a proposal value with:

- tokens shaped `[B, K - 1]`;
- optional dense probabilities `[B, K - 1, vocabulary]`;
- optional sparse probabilities and matching token indices `[B, K - 1, S]`.

Dense proposals cover DFlash 1. Sparse proposals avoid expanding DFlash 2 selector top-k values to a full vocabulary tensor.

Existing token-only MTP drafters should remain source-compatible and continue using greedy token equality unless they opt into the probability-bearing capability.

### 3. Exact acceptance

For each proposed token `x`, accept with `min(1, p(x) / q(x))`. At the first rejection, sample the correction from normalized `max(p - q, 0)`. If all draft tokens are accepted, sample the bonus row from the target distribution.

The target distribution must be computed after the same stateful `LogitProcessor` sequence as ordinary generation. Processor state should be committed only for emitted tokens.

### 4. Target feature capture

Let a drafter descriptor request target layer IDs. The target should emit only those hidden states through `LMOutput.State`; ordinary generation remains unchanged when the request is absent.

This should be capability-driven rather than a switch on a concrete Qwen model class.

### 5. Prefix cache transaction

Keep the existing staged commit for attention-only caches. For hybrid targets, generalize the existing native rewind depth into a transaction/checkpoint contract that can retain the always-committed anchor plus an accepted prefix. A target may implement this through checkpoints or by replaying only its recurrent layers.

## Initial scope

- Text-only, batch size 1.
- Qwen 3 and Qwen 3.5 target families.
- DFlash 1 and DFlash 2 checkpoints.
- Greedy decoding first; stochastic decoding only after exact acceptance tests pass.
- No rotating or quantized target KV cache in the first model-support PR unless the cache transaction can prove correctness for them.

## Test plan

- Synthetic dense and sparse proposal distributions with deterministic acceptance/rejection cases.
- Monte Carlo distribution comparison against ordinary target sampling.
- Stateful `LogitProcessor` tests proving verifier-only rows are not committed.
- Partial-prefix commit tests for simple KV, staged rotating KV, and Qwen hybrid recurrent cache.
- Checkpoint configuration and weight-sanitization tests for DFlash 1 and DFlash 2.
- Integration parity tests with a small published checkpoint before enabling a model in the registry.

## Questions for maintainers

1. Should the proposal-probability capability extend `MTPDrafterModel`, or live in a more general speculative decoding protocol?
2. Should requested intermediate target layers use new `LMOutput.State` keys, or should the current MTP hidden-state key become a structured capture object?
3. Is a generalized cache transaction preferred over expanding `SpeculativeCacheRewindModel` beyond its current rewind-depth contract?

## Prototype disclosure

The Tanpopo prototype includes separate DFlash model types, dense/sparse exact rejection sampling, Qwen intermediate-layer capture, and hybrid recurrent rollback. I would split that code into small PRs and adapt it to the interfaces agreed in this issue rather than submit the product-specific implementation wholesale.

AI usage disclosure: OpenAI Codex assisted with comparing the prototype to current upstream MTP code and drafting this proposal. I reviewed and understand the proposed design before submission.

---

## 人工補充資料

正式送出前，建議加上至少一組可公開重現的 checkpoint、prompt、block size、接受率與 tok/s。沒有數據時可先討論 API，但不應宣稱效能提升幅度。
