// Copyright © 2026 Tanpopo contributors. MTP lifecycle follows Qwen35MTP.swift.
import Foundation
import MLX
import MLXLMCommon
import MLXNN

final class Qwen4ExpMTPPredictor: Module {
    @ModuleInfo(key: "pre_fc_norm_embedding") var embeddingNorm: Qwen4ExpNorm
    @ModuleInfo(key: "pre_fc_norm_hidden") var hiddenNorm: Qwen4ExpNorm
    @ModuleInfo(key: "fc_embedding") var embeddingFC: Linear
    @ModuleInfo(key: "fc_hidden") var hiddenFC: Linear
    @ModuleInfo(key: "hyper_connection_mixer") var mixer: Qwen4ExpHyper
    let layers: [Qwen4ExpLayer], c: Qwen4ExpConfiguration
    init(_ c: Qwen4ExpConfiguration) {
        self.c = c
        let h = c.base.hiddenSize
        _embeddingNorm.wrappedValue = Qwen4ExpNorm(h, eps: c.base.rmsNormEps)
        _hiddenNorm.wrappedValue = Qwen4ExpNorm(h * c.hcCount, eps: c.base.rmsNormEps)
        _embeddingFC.wrappedValue = Linear(h, h, bias: false)
        _hiddenFC.wrappedValue = Linear(h, h, bias: false)
        _mixer.wrappedValue = Qwen4ExpHyper(c, combine: false)
        layers = [Qwen4ExpLayer(c, index: 0, draft: true)]
    }
    func newCache() -> [KVCache] { [IndexedKVCache()] }
    func callAsFunction(
        inputsEmbeds: MLXArray, hiddenStates: MLXArray, cache: [KVCache], positionOffset: Int
    ) -> MLXArray {
        precondition(hiddenStates.dim(-1) == c.hcCount * c.base.hiddenSize)
        precondition(cache.count == 1 && cache[0].offset == positionOffset)
        let e = embeddingFC(embeddingNorm(inputsEmbeds)).expandedDimensions(axis: -2)
        let h = hiddenFC(
            hiddenNorm(hiddenStates).reshaped(
                Array(hiddenStates.shape.dropLast()) + [c.hcCount, c.base.hiddenSize]))
        let wide = (e + h).reshaped(hiddenStates.shape)
        return mixer(
            layers[0](
                wide, tokens: MLXArray.zeros([wide.dim(0), wide.dim(1)], dtype: .int32),
                cache: cache[0])
        ).0
    }
}
public final class Qwen4ExpMTPDraftModel: Module, StatefulMTPDrafterModel {
    public let configuration: Qwen4ExpConfiguration
    public let maximumBlockSize: Int? = 2
    public let requiresSharedTargetKV = false
    public let requiresPromptPrefill = true
    public let requiresGreedySampling = true
    @ModuleInfo(key: "mtp") var mtp: Qwen4ExpMTPPredictor
    public init(_ c: Qwen4ExpConfiguration) {
        configuration = c
        _mtp.wrappedValue = Qwen4ExpMTPPredictor(c)
    }
    public func validate(target: any LanguageModel) throws {
        guard let target = target as? Qwen4ExpModel else {
            throw MTPCompatibilityError.unsupportedTarget(String(describing: type(of: target)))
        }
        let a = configuration
        let b = target.configuration
        guard a.hcCount == b.hcCount, a.hcLowrank == b.hcLowrank,
            a.base.hiddenSize == b.base.hiddenSize, a.base.vocabularySize == b.base.vocabularySize,
            a.base.attentionHeads == b.base.attentionHeads, a.base.kvHeads == b.base.kvHeads,
            a.base.headDim == b.base.headDim, a.base.ropeTheta == b.base.ropeTheta,
            a.base.partialRotaryFactor == b.base.partialRotaryFactor,
            a.indexHeads == b.indexHeads, a.indexDim == b.indexDim,
            a.indexBudget == b.indexBudget, a.compress == b.compress,
            a.base.numExperts == b.base.numExperts,
            a.base.moeIntermediateSize == b.base.moeIntermediateSize
        else {
            throw MTPCompatibilityError.incompatibleConfiguration(
                "Qwen4-Exp MTP 與 Target 的維度或注意力設定不一致")
        }
    }
    public func makeState(parameters: GenerateParameters?) -> MTPDrafterState {
        MTPDrafterState(cache: mtp.newCache())
    }

    public func prepareDrafterState(
        target: any LanguageModel,
        promptTokens: MLXArray,
        targetHidden: MLXArray,
        firstBonus: MLXArray,
        positionDeltas _: MLXArray?,
        state: inout MTPDrafterState,
        sampler: any LogitSampler
    ) {
        let (targetEmbedTokens, lmHead) = targetEmbeddingAndHead(target)
        let inputEmbedding = targetEmbedTokens
        let prompt = normalizedMTPTokenBatch(promptTokens)
        let bonus = normalizedMTPColumn(firstBonus)
        guard prompt.dim(-1) > 0 else { return }

        let shifted = concatenated([prompt[0..., 1...], bonus], axis: 1)
        let hidden = targetHidden[0..., ..<shifted.dim(1), 0...]
        state.nextPosition = 0
        let mtpHidden = mtp(
            inputsEmbeds: inputEmbedding(shifted),
            hiddenStates: hidden,
            cache: state.cache,
            positionOffset: 0)
        state.nextPosition = shifted.dim(1)
        state.seedHidden = mtpHidden[0..., (-1)..., 0...]
        state.seedToken = sampleMTPSeed(
            hidden: state.seedHidden!, targetEmbedTokens: targetEmbedTokens,
            lmHead: lmHead, sampler: sampler)
        state.proposalAppended = 0
    }

    public func draftBlock(
        target: any LanguageModel,
        lastToken: MLXArray,
        lastHidden: MLXArray,
        sharedKV: [String: (MLXArray, MLXArray)],
        positionDeltas: MLXArray?,
        queryOffset: Int,
        blockSize: Int,
        sampler: any LogitSampler
    ) -> MLXArray {
        var state = makeState(parameters: nil)
        return draftBlock(
            target: target,
            lastToken: lastToken,
            lastHidden: lastHidden,
            sharedKV: sharedKV,
            positionDeltas: positionDeltas,
            queryOffset: queryOffset,
            blockSize: blockSize,
            state: &state,
            sampler: sampler)
    }

    public func draftBlock(
        target: any LanguageModel,
        lastToken: MLXArray,
        lastHidden: MLXArray,
        sharedKV _: [String: (MLXArray, MLXArray)],
        positionDeltas _: MLXArray?,
        queryOffset: Int,
        blockSize: Int,
        state: inout MTPDrafterState,
        sampler: any LogitSampler
    ) -> MLXArray {
        let (targetEmbedTokens, lmHead) = targetEmbeddingAndHead(target)
        let inputEmbedding = targetEmbedTokens

        if let seed = state.seedToken {
            state.seedToken = nil
            state.seedHidden = nil
            state.proposalAppended = 0
            return seed
        }

        state.proposalAppended = blockSize - 1
        let proposed = draftMTPTokenBlock(
            targetEmbedTokens: targetEmbedTokens,
            lmHead: lmHead,
            inputEmbedding: inputEmbedding,
            lastToken: lastToken,
            lastHidden: lastHidden,
            queryOffset: queryOffset,
            blockSize: blockSize,
            sampler: sampler,
            cache: state.cache
        ) { inputsEmbeds, hiddenStates, cache, positionOffset in
            mtp(
                inputsEmbeds: inputsEmbeds,
                hiddenStates: hiddenStates,
                cache: cache,
                positionOffset: positionOffset)
        }
        state.nextPosition += state.proposalAppended
        return proposed
    }

    public func commitDrafterState(
        target: any LanguageModel,
        targetHidden: MLXArray,
        draftTokens: MLXArray,
        acceptedCount: Int,
        finalToken: MLXArray,
        positionDeltas _: MLXArray?,
        state: inout MTPDrafterState,
        sampler: any LogitSampler
    ) {
        let keepAppended = Swift.min(acceptedCount, state.proposalAppended)
        let trim = state.proposalAppended - keepAppended
        if trim > 0 {
            trimPromptCache(state.cache, numTokens: trim)
            state.nextPosition -= trim
        }

        var tokens = [MLXArray]()
        var hiddens = [MLXArray]()
        for index in keepAppended ..< acceptedCount {
            tokens.append(draftTokens[0..., index ..< (index + 1)])
            hiddens.append(targetHidden[0..., index ..< (index + 1), 0...])
        }
        tokens.append(normalizedMTPColumn(finalToken))
        hiddens.append(targetHidden[0..., acceptedCount ..< (acceptedCount + 1), 0...])

        let (targetEmbedTokens, lmHead) = targetEmbeddingAndHead(target)
        let inputEmbedding = targetEmbedTokens
        let committedTokens = concatenated(tokens, axis: 1)
        let committedHidden = concatenated(hiddens, axis: 1)
        let mtpHidden = mtp(
            inputsEmbeds: inputEmbedding(committedTokens),
            hiddenStates: committedHidden,
            cache: state.cache,
            positionOffset: state.nextPosition)
        state.nextPosition += committedTokens.dim(1)
        state.seedHidden = mtpHidden[0..., (-1)..., 0...]
        state.seedToken = sampleMTPSeed(
            hidden: state.seedHidden!, targetEmbedTokens: targetEmbedTokens,
            lmHead: lmHead, sampler: sampler)
        state.proposalAppended = 0
    }

    public func sanitize(weights: [String: MLXArray]) -> [String: MLXArray] {
        var out = [String: MLXArray]()
        for (original, value) in weights {
            var key = original
            for prefix in ["language_model.", "model."] where key.hasPrefix(prefix) {
                key = String(key.dropFirst(prefix.count))
                break
            }
            if !key.hasPrefix("mtp.") { key = "mtp." + key }
            out[key] = value
        }
        for key in Array(out.keys) where key.contains(".experts.gate_up_proj") {
            let split = out.removeValue(forKey: key)!.split(parts: 2, axis: -2)
            let suffix = key.hasSuffix("gate_up_proj") ? ".weight" : ""
            out[
                key.replacingOccurrences(of: "experts.gate_up_proj", with: "switch_mlp.gate_proj")
                    + suffix] = split[0]
            out[
                key.replacingOccurrences(of: "experts.gate_up_proj", with: "switch_mlp.up_proj")
                    + suffix] = split[1]
        }
        for key in Array(out.keys) where key.contains(".experts.down_proj") {
            out[
                key.replacingOccurrences(of: "experts.down_proj", with: "switch_mlp.down_proj")
                    + (key.hasSuffix("down_proj") ? ".weight" : "")] = out.removeValue(forKey: key)
        }
        return out
    }
    private func targetEmbeddingAndHead(_ target: any LanguageModel) -> (Embedding, Linear?) {
        let target = target as! Qwen4ExpModel
        return (target.model.embedding, target.lmHead)
    }
}
