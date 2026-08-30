# MMap 實驗 Issue 草稿

建議標題：`[Discussion] Evaluate file-backed safetensors mappings for memory-pressure workloads`

MMap 不建議直接以「省記憶體功能」送 PR。macOS unified memory、檔案頁面快取與 MLX／Metal buffer 的行為會讓 RSS、phys_footprint 與真正被佔用的 file-backed resident pages 出現差異；必須先定義一致量測方法。

以下英文內容可貼入官方 `Other` Issue：

---

- [ ] I have read this issue in full and approve it as my own, however it was drafted.

## Motivation

I am experimenting with a safetensors loader that exposes aligned, read-only file mappings directly as `MLXArray` storage. The goal is not to impose a hard memory cap. The intended use case is reducing cold-load allocation pressure and allowing clean weight pages to remain file-backed and reclaimable when a model is larger than comfortable physical memory.

Current `mlx-swift-lm` main already uses lazy safetensors arrays and concurrently materializes contiguous byte-balanced groups on CPU. A mmap implementation therefore needs to demonstrate a benefit over that baseline, not over an older eager loader.

## Prototype

The Tanpopo prototype currently:

- parses safetensors headers without loading tensor payloads;
- creates one read-only `MAP_PRIVATE` region per aligned tensor;
- wraps the mapped bytes in an `MLXArray(rawPointer:)` with a lifetime callback;
- falls back to an aligned copy when the tensor data offset cannot satisfy Metal alignment;
- tracks mapped virtual bytes and samples residency with `mincore`;
- keeps product-level memory targets outside the loader because they are guardrails, not hard Runtime limits.

This is a proof of concept, not a merge-ready implementation.

## Measurement concerns

1. Process RSS and `phys_footprint` may not represent clean file-backed resident pages consistently.
2. Per-tensor mappings can overlap the same physical file page. Residency accounting must de-duplicate by file identity and page offset.
3. Safetensors data offsets are not guaranteed to satisfy the alignment needed by the Metal buffer path; fallback copies must be reported.
4. Overcommitting physical memory can turn generation into page-fault thrashing, so lower startup pressure may trade away tokens per second and latency stability.
5. `madvise` is a hint, not a memory limit or a reliable forced-eviction mechanism.

## Proposed benchmark matrix

Compare the current upstream loader and the mmap prototype with identical model, prompt, generation parameters, thermal state, and cache policy. Record:

- cold model-load time;
- time to first token;
- peak and steady process physical footprint;
- de-duplicated file-backed resident bytes;
- major/minor page faults where available;
- prompt and generation tok/s;
- latency percentiles across repeated decode steps;
- aligned mapped bytes versus fallback copied bytes;
- behavior below, near, and above physical-memory capacity.

Suggested model classes are one that comfortably fits, one near the machine limit, and one deliberately above it. Results should include total RAM and OS version.

## Questions for maintainers

1. Is direct file-backed `MLXArray` storage considered an `mlx-swift` concern rather than an `mlx-swift-lm` loader feature?
2. Would a lower-level storage/lifetime API be preferable to a second safetensors parser in `mlx-swift-lm`?
3. Which memory and page-residency metrics would maintainers accept for evaluating this tradeoff on Apple silicon?

## Non-goals

- No claim of a hard per-device memory limit.
- No automatic selection based only on model size.
- No UI or application-specific memory target in the library API.
- No promise that a model larger than RAM will retain usable generation throughput.

AI usage disclosure: OpenAI Codex assisted with comparing this prototype against the current upstream loader and drafting the experiment plan. I reviewed and understand the proposal before submission.

---

## 送出前要求

這份 Issue 必須附上 [量測規格](./BENCHMARK-PLAN.md) 產生的第一批數據。若尚未完成 near-memory 與 over-memory 至少各一組測試，先不要宣稱 mmap 能讓大模型「可用」。
