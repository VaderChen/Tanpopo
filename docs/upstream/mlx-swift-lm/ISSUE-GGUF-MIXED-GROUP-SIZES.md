# GGUF 混合量化 group size 技術建議草稿

建議主標題：`[Discussion] Shape-derived layouts for mixed group-size GGUF loading`

這份建議來自 Tanpopo 的 GGUF-to-MLX 最佳化工作。目標不是回報 MLX
無法混用 group 32／64；實驗反而證明 MLX 已支援逐層不同的 affine
quantization group size。真正問題是 loader 在轉換架構特定的 tensor layout
時，以模型層級的單一 `groupSize` 判斷 weight、scales 與 biases 的壓縮方式。

## 建議投遞對象

### 1. 首選：`ml-explore/mlx-swift-lm`

- 形式：GitHub `Other` Issue／設計討論，不直接送 PR。
- 原因：問題出現在模型權重載入、架構 layout 還原與 quantized module 建立之間，
  比 MLX kernel 更接近 LM loader 層。
- 目的：分享可重現的 loader 經驗，並詢問若官方未來支援 GGUF，通用的
  tensor-layout／quantization metadata 契約應放在 `mlx-swift-lm` 還是
  `mlx-swift`。
- 不應混入：Tanpopo UI、模型檔名判斷、下載策略、轉換快取或產品層 fallback。

### 2. 次選：`ml-explore/mlx-swift`

- 形式：小型文件／測試建議。
- 主題：明確記錄 `groupSize` 是 per-layer／per-operation 設定，並加入同一
  `Module` 混合 group 32／64、以及載入外部預量化 weight/scales/biases 的
  regression test。
- 不應宣稱：MLX quantization kernel 有錯。目前合成測試沒有重現 kernel 錯誤。

### 3. 暫不投遞

- Apple Feedback：目前沒有 Metal、Accelerate、Xcode 或作業系統層的最小失敗案例。
- `ml-explore/mlx` core：現有核心已接受 group 32、64、128，且每次
  `quantizedMM` 都帶自己的 group size。
- `llama.cpp`：GGUF 中的 Qwen 3.5 tiled layout 是來源格式契約；目前問題是
  MLX loader 還原該 layout 時的判斷，不是 GGUF writer 產生非法資料。

只有在有效 shape 與 `(groupSize, bits)` 傳入 `quantizedMM` 後仍產生錯誤數值、
crash 或錯誤 kernel，才應升級成 `mlx-swift`／Apple 的 bug report。

## 技術背景

為了提高 GGUF 在 MLX 上的執行效率，Tanpopo 嘗試讓可無損沿用的 Q4_K tensor
保留 group 32／INT4，同時讓需要重新量化的 tensor 使用 group 64／INT8。
這避免把整個模型降到單一保守配置，也減少不必要的重新量化與 scale/bias
metadata。

初期版本可以正確建立混合 quantized layers，但 Qwen 3.5 linear-attention
`ssm_out` 的 value-column 還原依賴一個模型層級的 resolved group size：

```swift
if dimensionLength == layout.valueRows / groupSize {
    packingFactor = groupSize
}
```

當 resolved group 是 64、但特定 Q4_K layer 實際沿用 group 32 時，packed weight
可能符合另一個分支並完成重排，該 layer 的 scales／biases 卻不符合
`valueRows / 64`，因此沒有重排。weight 與 quantization parameters 的 value-head
順序不同，模型不一定立即丟出 shape error，而可能直接產生失去語意的輸出。

## 通用修正

layout 還原真正需要的是該 tensor 在 value-column 軸上的壓縮倍率，不是模型的
預設 group size。這個倍率可由架構展開長度與 tensor 的實際 shape 推導：

```swift
func reorderValueColumns(
    _ value: MLXArray,
    layout: LinearAttentionLayout
) -> MLXArray {
    guard value.ndim >= 2 else { return value }

    let dimensionLength = value.dim(-1)
    guard dimensionLength > 0,
          layout.valueRows % dimensionLength == 0 else {
        return value
    }

    let packingFactor = layout.valueRows / dimensionLength
    guard layout.valueHeadDimension % packingFactor == 0 else {
        return value
    }

    return reorderHeadAxis(
        value,
        axis: value.ndim - 1,
        headDimension: layout.valueHeadDimension / packingFactor,
        layout: layout
    )
}
```

這會分別從每個 tensor 推導：

- 未量化 tensor：packing factor 1；
- packed weight：packing factor `32 / bits`；
- scales／biases：packing factor 等於該 tensor 實際使用的 group size。

因此 weight、scales 與 biases 都依自己的 shape 還原相同的 value-head
permutation，不需依賴模型層級的全域 group size。guard 同時確保無法證明 shape
相容時保持原值，不猜測 layout。

這個方法只負責還原已知架構軸的 permutation。建立 `QuantizedLinear` 時仍應從
weight 與 scales 的 shape 一起驗證真正的 `(groupSize, bits)`：

```text
weightPackedWidth * 32 / bits == scaleCount * groupSize
```

## 驗證狀態

Tanpopo 已有兩個不依賴外部模型的合成測試：

1. MLX 自行量化四層 linear stack，每層交替使用 group 32／4-bit 與
   group 64／8-bit。
2. 先在模型外建立混合 group 的 packed weight、scales、biases，再載入對應的
   `QuantizedLinear` layers。

執行方式：

```bash
swift test --package-path mlx-server --filter MixedGroupSizeProbeTests
```

本機結果：2 tests、0 failures。這表示 MLX 本身可以在同一模型中執行不同
group size；先前的錯誤來自 loader 的 layout 還原。

初步端到端量測顯示，混合 Q4_K group 32 與重新量化 group 64 的策略可正常輸出，
並比全 INT8／group 64 路徑更快。不過正式投遞前仍應補上可公開重現的模型、
commit、硬體、OS、prompt、token 數與多次量測統計，不應只用單次 tok/s 宣稱
固定效能提升。

## 英文 Issue 草稿

以下內容建議貼到 `ml-explore/mlx-swift-lm` 的 `Other` Issue：

---

- [ ] I have read this issue in full and approve it as my own, however it was drafted.

## Motivation

I am optimizing a GGUF-to-MLX loader and found a useful boundary between MLX's
quantization capability and loader-side tensor layout handling.

The optimization keeps directly reusable Q4_K tensors as 4-bit affine weights
with group size 32, while tensors that require requantization can use 8-bit
affine storage with group size 64. This allows one model to contain quantized
layers with different group sizes instead of forcing every layer through one
model-wide setting.

MLX already supports this. `quantize(model:filter:)` can return a different
`(groupSize, bits, mode)` tuple for each module, and each `QuantizedLinear`
passes its own configuration to `quantizedMM`. A synthetic four-layer test,
including externally prepared weights, scales, and biases, passes with mixed
group sizes 32 and 64.

## Loader issue

The problem appeared while reversing a Qwen 3.5 GGUF value-head permutation.
The initial loader used one resolved model-wide `groupSize` to decide whether a
tensor represented packed weights or per-group scales/biases.

That assumption breaks for mixed layouts. For example, when the model-wide
fallback is 64 but a directly reused Q4_K layer remains group 32, the packed
weight may be reordered while its scales and biases are not. The shapes can
still be accepted by later loading code, but the quantization parameters no
longer describe the same value-head order as the weight.

## Shape-derived solution

For a known architecture axis, the reordering code does not need a global group
size. It only needs the compression factor represented by the tensor's actual
shape:

```swift
let dimensionLength = value.dim(-1)
guard dimensionLength > 0,
      layout.valueRows % dimensionLength == 0 else {
    return value
}

let packingFactor = layout.valueRows / dimensionLength
guard layout.valueHeadDimension % packingFactor == 0 else {
    return value
}
```

The resulting factor is independently derived for each tensor:

- 1 for an unpacked value;
- `32 / bits` for a packed affine weight;
- the actual group size for scales and biases.

The same value-head permutation can then be applied consistently to weight,
scales, and biases without consulting a model-wide quantization setting. If the
shape does not prove compatibility, the loader leaves the tensor unchanged.

The quantized layer should still validate its actual `(groupSize, bits)` from
both weight and scale shapes before construction:

```text
packedWeightWidth * 32 / bits == scaleCount * groupSize
```

## Validation

I tested two synthetic paths with a four-layer linear stack:

1. MLX quantizes alternating layers as group-32/4-bit and group-64/8-bit.
2. The same mixed packed weights, scales, and biases are prepared externally and
   loaded into matching quantized layers.

Both tests pass, which isolates the original failure to loader-side layout
handling rather than mixed group sizes in MLX itself.

Preliminary end-to-end testing on a GGUF model also produces valid output while
retaining the faster mixed storage path. I can provide a reproducible benchmark
with model, hardware, OS, prompt, token count, and repeated measurements if this
loader direction is useful to the project.

## Questions

1. If GGUF loading is added in the future, should per-tensor quantization layout
   inference live in `mlx-swift-lm`, or should `mlx-swift` expose a lower-level
   metadata/validation helper?
2. Would maintainers accept a small `mlx-swift` regression test documenting that
   group size is a per-layer property and that externally prepared mixed-group
   weights can be loaded safely?
3. Is there an existing quantized-weight descriptor that a loader should use
   instead of separately carrying weight, scales, biases, group size, bits, and
   mode?

## Non-goals

- This is not a report that MLX quantized kernels cannot mix group sizes.
- This does not propose a Qwen-specific branch in the public API.
- This does not require one quantization setting for an entire model.
- Product-specific GGUF caching, fallback policy, and UI behavior are out of scope.

AI usage disclosure: OpenAI Codex assisted with reviewing the loader prototype,
isolating the MLX core behavior from the layout-conversion issue, running the
focused synthetic tests, and drafting this issue. I reviewed and understand the
proposal before submission.

---

## `mlx-swift` 短版建議

若 `mlx-swift-lm` 維護者建議轉往 `mlx-swift`，可改用以下較小範圍：

建議標題：`Document and test mixed per-layer affine quantization group sizes`

```text
While optimizing a GGUF-to-MLX loader, I verified that one Module can safely
contain QuantizedLinear layers using different affine group sizes. Both an
MLX-quantized four-layer stack and externally prepared weight/scales/biases pass
when alternating group-32/4-bit and group-64/8-bit layers.

This behavior is supported by the current API: quantize(model:filter:) resolves
quantization parameters per module, and QuantizedLinear stores and forwards its
own groupSize and bits. However, loader implementations can easily treat
groupSize as a model-wide setting and incorrectly transform scale/bias layouts.

Would a focused regression test and a documentation note clarifying that group
size is per layer/per operation be useful? I can contribute the synthetic test
without any GGUF- or model-specific code.

AI usage disclosure: OpenAI Codex assisted with reviewing the prototype,
running the focused tests, and drafting this proposal. I reviewed and understand
the submission.
```

## 正式送出前補充

1. 重跑端到端正確性測試，避免引用修正前的精度結果。
2. 將 tok/s 至少量測 5 次，提供平均值、標準差與 warm-up 規則。
3. 列出實際混合 tensor 數量與 storage signature。
4. 附上至少一組 weight/scales/biases shape，讓維護者可驗算 group size。
5. 提交者本人逐字確認英文內容與 AI disclosure。
6. 未取得確認前，不建立 Issue、不發表 comment，也不把此內容混入 sampler PR。
