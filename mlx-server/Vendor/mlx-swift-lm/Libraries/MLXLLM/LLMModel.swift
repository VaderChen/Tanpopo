// Copyright © 2024 Apple Inc.

import MLX
import MLXLMCommon

/// Marker protocol for LLMModels
public protocol LLMModel: LanguageModel, LoRAModel {

    /// Models can implement this is they need a custom `MessageGenerator`.
    ///
    /// The default implementation returns `DefaultMessageGenerator`.
    func messageGenerator(tokenizer: Tokenizer) -> MessageGenerator
}

extension LLMModel {

    public func prefillChunkSize(input: LMInput, windowSize: Int) -> Int {
        min(input.text.tokens.dim(-1), max(1, windowSize))
    }

    /// Default prepare step for ``LLMModel``.
    ///
    /// 分段處理完整輸入，回傳最後位置的 logits 與需延續的 decoder state。
    public func prepare(_ input: LMInput, cache: [KVCache], windowSize: Int?) throws
        -> PrepareResult
    {
        let tokens = input.text.tokens
        let batched = tokens.ndim == 1 ? tokens[.newAxis] : tokens
        let mask = input.text.mask.map { $0.ndim == 1 ? $0[.newAxis] : $0 }
        return try prefillInChunks(
            tokenCount: batched.dim(-1), windowSize: windowSize, cache: cache
        ) { range, state in
            let text = LMInput.Text(
                tokens: batched[0..., range], mask: mask?[0..., range])
            return withPreparedCache(cache, lengths: text.sequenceLengths) {
                self(text, cache: cache.isEmpty ? nil : cache, state: state)
            }
        }
    }

    public func messageGenerator(tokenizer: Tokenizer) -> MessageGenerator {
        DefaultMessageGenerator()
    }
}
