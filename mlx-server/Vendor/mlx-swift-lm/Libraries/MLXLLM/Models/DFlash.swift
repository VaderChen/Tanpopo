// Copyright © 2026 OpenLoader contributors.

import Foundation
import MLX
import MLXLMCommon
import MLXNN

// MARK: - Configuration

public struct DFlashConfiguration: Decodable, Sendable {
    public struct DraftOptions: Decodable, Sendable {
        let blockSize: Int?
        let targetLayerIDs: [Int]
        let maskTokenID: Int
        let finalLogitSoftcapping: Float?
        let inputEmbeddingScale: Float?
        let outputMultiplier: Float?
        let convolutionKernelSize: Int?
        let convolutionGroupSize: Int?
        let selectorRank: Int?
        let selectorTopK: Int?

        enum CodingKeys: String, CodingKey {
            case blockSize = "block_size"
            case targetLayerIDs = "target_layer_ids"
            case maskTokenID = "mask_token_id"
            case finalLogitSoftcapping = "final_logit_softcapping"
            case inputEmbeddingScale = "input_embedding_scale"
            case outputMultiplier = "output_multiplier"
            case convolutionKernelSize = "conv_kernel_size"
            case convolutionGroupSize = "conv_group_size"
            case selectorRank = "selector_rank"
            case selectorTopK = "selector_top_k"
        }
    }

    let architectures: [String]
    let modelType: String
    let hiddenSize: Int
    let hiddenLayers: Int
    let intermediateSize: Int
    let attentionHeads: Int
    let kvHeads: Int
    let headDim: Int
    let vocabularySize: Int
    let rmsNormEps: Float
    let ropeTheta: Float
    let ropeScaling: [String: StringOrNumber]?
    let maximumPositions: Int
    let blockSize: Int
    let targetLayerCount: Int
    let layerTypes: [String]
    let slidingWindow: Int?
    let isCausal: Bool?
    let finalLogitSoftcapping: Float?
    let inputEmbeddingScale: Float
    let outputMultiplier: Float
    let draft: DraftOptions

    enum CodingKeys: String, CodingKey {
        case architectures
        case modelType = "model_type"
        case hiddenSize = "hidden_size"
        case hiddenLayers = "num_hidden_layers"
        case intermediateSize = "intermediate_size"
        case attentionHeads = "num_attention_heads"
        case kvHeads = "num_key_value_heads"
        case headDim = "head_dim"
        case vocabularySize = "vocab_size"
        case rmsNormEps = "rms_norm_eps"
        case ropeTheta = "rope_theta"
        case ropeScaling = "rope_scaling"
        case ropeParameters = "rope_parameters"
        case maximumPositions = "max_position_embeddings"
        case blockSize = "block_size"
        case targetLayerCount = "num_target_layers"
        case layerTypes = "layer_types"
        case slidingWindow = "sliding_window"
        case isCausal = "is_causal"
        case finalLogitSoftcapping = "final_logit_softcapping"
        case inputEmbeddingScale = "input_embedding_scale"
        case outputMultiplier = "output_multiplier"
        case draft = "dflash_config"
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        architectures = try container.decodeIfPresent([String].self, forKey: .architectures) ?? []
        modelType = try container.decodeIfPresent(String.self, forKey: .modelType) ?? "qwen3"
        hiddenSize = try container.decode(Int.self, forKey: .hiddenSize)
        hiddenLayers = try container.decode(Int.self, forKey: .hiddenLayers)
        intermediateSize = try container.decode(Int.self, forKey: .intermediateSize)
        attentionHeads = try container.decode(Int.self, forKey: .attentionHeads)
        kvHeads = try container.decode(Int.self, forKey: .kvHeads)
        headDim = try container.decode(Int.self, forKey: .headDim)
        vocabularySize = try container.decode(Int.self, forKey: .vocabularySize)
        rmsNormEps = try container.decode(Float.self, forKey: .rmsNormEps)
        let decodedRope = try container.decodeIfPresent(
            [String: StringOrNumber].self, forKey: .ropeParameters)
            ?? container.decodeIfPresent(
                [String: StringOrNumber].self, forKey: .ropeScaling)
        ropeScaling = decodedRope
        ropeTheta = try container.decodeIfPresent(Float.self, forKey: .ropeTheta)
            ?? decodedRope?["rope_theta"]?.asFloat()
            ?? 10_000
        maximumPositions = try container.decodeIfPresent(
            Int.self, forKey: .maximumPositions) ?? 32_768
        blockSize = try container.decodeIfPresent(Int.self, forKey: .blockSize) ?? 16
        targetLayerCount = try container.decode(Int.self, forKey: .targetLayerCount)
        layerTypes = try container.decodeIfPresent([String].self, forKey: .layerTypes)
            ?? Array(repeating: "full_attention", count: hiddenLayers)
        slidingWindow = try container.decodeIfPresent(Int.self, forKey: .slidingWindow)
        isCausal = try container.decodeIfPresent(Bool.self, forKey: .isCausal)
        finalLogitSoftcapping = try container.decodeIfPresent(
            Float.self, forKey: .finalLogitSoftcapping)
        inputEmbeddingScale = try container.decodeIfPresent(
            Float.self, forKey: .inputEmbeddingScale) ?? 1
        outputMultiplier = try container.decodeIfPresent(
            Float.self, forKey: .outputMultiplier) ?? 1
        draft = try container.decode(DraftOptions.self, forKey: .draft)
    }

    var effectiveBlockSize: Int { draft.blockSize ?? blockSize }
    var effectiveFinalLogitSoftcapping: Float? {
        draft.finalLogitSoftcapping ?? finalLogitSoftcapping
    }
    var effectiveInputEmbeddingScale: Float {
        draft.inputEmbeddingScale ?? inputEmbeddingScale
    }
    var effectiveOutputMultiplier: Float {
        draft.outputMultiplier ?? outputMultiplier
    }
    var variant: DFlashVariant {
        architectures.contains("DFlash2DraftModel") ? .dflash2 : .dflash1
    }
    var convolutionKernelSize: Int { draft.convolutionKernelSize ?? 0 }
    var convolutionGroupSize: Int { draft.convolutionGroupSize ?? 0 }
    var selectorRank: Int { draft.selectorRank ?? 0 }
    var selectorTopK: Int { draft.selectorTopK ?? 0 }

    func validate() throws {
        let supportedArchitectures = architectures.filter {
            $0 == "DFlashDraftModel" || $0 == "DFlash2DraftModel"
        }
        guard supportedArchitectures.count == 1 else {
            throw DFlashError.invalidDraftConfiguration(
                "architectures 必須且只能包含 DFlashDraftModel 或 DFlash2DraftModel。")
        }
        guard modelType == "qwen3" || modelType == "qwen3_5" else {
            throw DFlashError.invalidDraftConfiguration(
                "目前只支援 Qwen3／Qwen3.5 系列 Draft，收到 \(modelType)。")
        }
        guard hiddenLayers > 0, layerTypes.count == hiddenLayers else {
            throw DFlashError.invalidDraftConfiguration("layer_types 數量與 draft layer 不一致。")
        }
        guard hiddenSize > 0, intermediateSize > 0, attentionHeads > 0,
              kvHeads > 0, headDim > 0, vocabularySize > 0,
              attentionHeads.isMultiple(of: kvHeads) else {
            throw DFlashError.invalidDraftConfiguration("模型維度或 Attention head 設定無效。")
        }
        guard targetLayerCount > 0 else {
            throw DFlashError.invalidDraftConfiguration("num_target_layers 必須大於 0。")
        }
        guard layerTypes.allSatisfy({
            $0 == "full_attention" || $0 == "sliding_attention"
        }) else {
            throw DFlashError.invalidDraftConfiguration(
                "Draft layer_types 只支援 full_attention 或 sliding_attention。")
        }
        guard !layerTypes.contains("sliding_attention")
                || (slidingWindow ?? 0) > 1 else {
            throw DFlashError.invalidDraftConfiguration(
                "sliding_attention 必須提供大於 1 的 sliding_window。")
        }
        guard effectiveBlockSize >= 2 else {
            throw DFlashError.invalidDraftConfiguration("block_size 必須至少為 2。")
        }
        guard !draft.targetLayerIDs.isEmpty,
              Set(draft.targetLayerIDs).count == draft.targetLayerIDs.count else {
            throw DFlashError.invalidDraftConfiguration(
                "target_layer_ids 不可為空或重複。")
        }
        guard draft.targetLayerIDs.allSatisfy({ (0 ..< targetLayerCount).contains($0) }) else {
            throw DFlashError.invalidDraftConfiguration(
                "target_layer_ids 超出 target layer 範圍。")
        }
        guard (0 ..< vocabularySize).contains(draft.maskTokenID) else {
            throw DFlashError.invalidDraftConfiguration("mask_token_id 超出 vocabulary 範圍。")
        }
        guard rmsNormEps > 0, ropeTheta > 0,
              effectiveInputEmbeddingScale.isFinite,
              effectiveOutputMultiplier.isFinite else {
            throw DFlashError.invalidDraftConfiguration("正規化、RoPE 或縮放設定無效。")
        }
        if variant == .dflash2 {
            guard convolutionKernelSize >= 2,
                  convolutionGroupSize > 0,
                  hiddenSize.isMultiple(of: convolutionGroupSize) else {
                throw DFlashError.invalidDraftConfiguration(
                    "DFlash 2 convolution kernel／group 設定無效。")
            }
            guard selectorRank > 0,
                  selectorTopK > 1,
                  selectorTopK <= vocabularySize else {
                throw DFlashError.invalidDraftConfiguration(
                    "DFlash 2 candidate selector 設定無效。")
            }
        }
    }
}

// MARK: - Draft network

final class DFlashAttention: Module {
    let config: DFlashConfiguration
    let scale: Float
    let rope: RoPELayer
    let isSliding: Bool
    let slidingWindow: Int?
    let isCausal: Bool

    @ModuleInfo(key: "q_proj") var queryProjection: Linear
    @ModuleInfo(key: "k_proj") var keyProjection: Linear
    @ModuleInfo(key: "v_proj") var valueProjection: Linear
    @ModuleInfo(key: "o_proj") var outputProjection: Linear
    @ModuleInfo(key: "q_norm") var queryNorm: RMSNorm
    @ModuleInfo(key: "k_norm") var keyNorm: RMSNorm

    init(_ config: DFlashConfiguration, layerIndex: Int) {
        self.config = config
        isSliding = config.layerTypes[layerIndex] == "sliding_attention"
        slidingWindow = isSliding ? config.slidingWindow : nil
        isCausal = config.isCausal ?? isSliding
        scale = pow(Float(config.headDim), -0.5)
        rope = initializeRope(
            dims: config.headDim,
            base: config.ropeTheta,
            traditional: false,
            scalingConfig: config.ropeScaling,
            maxPositionEmbeddings: config.maximumPositions
        )
        _queryProjection.wrappedValue = Linear(
            config.hiddenSize, config.attentionHeads * config.headDim, bias: false)
        _keyProjection.wrappedValue = Linear(
            config.hiddenSize, config.kvHeads * config.headDim, bias: false)
        _valueProjection.wrappedValue = Linear(
            config.hiddenSize, config.kvHeads * config.headDim, bias: false)
        _outputProjection.wrappedValue = Linear(
            config.attentionHeads * config.headDim, config.hiddenSize, bias: false)
        _queryNorm.wrappedValue = RMSNorm(
            dimensions: config.headDim, eps: config.rmsNormEps)
        _keyNorm.wrappedValue = RMSNorm(
            dimensions: config.headDim, eps: config.rmsNormEps)
    }

    func callAsFunction(_ block: MLXArray, context: MLXArray, cache: KVCache) -> MLXArray {
        let batch = block.dim(0)
        let blockLength = block.dim(1)
        var context = context
        var contextLength = context.dim(1)
        if let slidingWindow {
            let keepContext = slidingWindow - 1
            if contextLength > keepContext {
                let skipped = contextLength - keepContext
                context = context[0..., skipped..., 0...]
                contextLength = context.dim(1)
                if let baseCache = cache as? BaseKVCache {
                    baseCache.offset += skipped
                }
            }
        }
        let contextOffset = cache.offset

        var queries = queryProjection(block)
        var contextKeys = keyProjection(context)
        var contextValues = valueProjection(context)
        var proposalKeys = keyProjection(block)
        var proposalValues = valueProjection(block)

        queries = queryNorm(
            queries.reshaped(batch, blockLength, config.attentionHeads, -1)
        ).transposed(0, 2, 1, 3)
        contextKeys = keyNorm(
            contextKeys.reshaped(batch, contextLength, config.kvHeads, -1)
        ).transposed(0, 2, 1, 3)
        contextValues = contextValues
            .reshaped(batch, contextLength, config.kvHeads, -1)
            .transposed(0, 2, 1, 3)
        proposalKeys = keyNorm(
            proposalKeys.reshaped(batch, blockLength, config.kvHeads, -1)
        ).transposed(0, 2, 1, 3)
        proposalValues = proposalValues
            .reshaped(batch, blockLength, config.kvHeads, -1)
            .transposed(0, 2, 1, 3)

        queries = applyRotaryPosition(
            rope, to: queries, offset: .scalar(contextOffset + contextLength))
        contextKeys = applyRotaryPosition(
            rope, to: contextKeys, offset: .scalar(contextOffset))
        proposalKeys = applyRotaryPosition(
            rope, to: proposalKeys, offset: .scalar(contextOffset + contextLength))

        let (cachedKeys, cachedValues) = cache.update(
            keys: contextKeys, values: contextValues)
        let keys = concatenated([cachedKeys, proposalKeys], axis: 2)
        let values = concatenated([cachedValues, proposalValues], axis: 2)

        let attentionMask: MLXFast.ScaledDotProductAttentionMaskMode
        if let slidingWindow {
            let cachedLength = cachedKeys.dim(2)
            let queryPositions = MLXArray(
                Int32(cachedLength) ..< Int32(cachedLength + blockLength)
            )[0..., .newAxis]
            let keyPositions = MLXArray(
                Int32(0) ..< Int32(cachedLength + blockLength)
            )[.newAxis]
            let contextMask = (keyPositions .< Int32(cachedLength))
                & ((queryPositions - keyPositions) .< Int32(slidingWindow))
            var blockMask = keyPositions .>= Int32(cachedLength)
            if isCausal {
                blockMask = blockMask & (keyPositions .<= queryPositions)
            }
            attentionMask = .array(contextMask | blockMask)
        } else if isCausal {
            attentionMask = .array(
                createCausalMask(n: blockLength, offset: cachedKeys.dim(2)))
        } else {
            attentionMask = .none
        }

        return outputProjection(
            MLXFast.scaledDotProductAttention(
                queries: queries,
                keys: keys,
                values: values,
                scale: scale,
                mask: attentionMask
            )
            .transposed(0, 2, 1, 3)
            .reshaped(batch, blockLength, -1)
        )
    }
}

private func dflashGroupedDynamicConvolution(
    hidden: MLXArray,
    dynamic: MLXArray,
    base: MLXArray,
    groupSize: Int
) -> MLXArray {
    let batch = hidden.dim(0)
    let length = hidden.dim(1)
    let hiddenSize = hidden.dim(2)
    let groups = hiddenSize / groupSize
    let blocks = hidden.reshaped(batch, length, groups, groupSize)
    let dynamic = dynamic.reshaped(batch, length, base.dim(0), groups, 1)
    var output = MLXArray.zeros(blocks.shape, dtype: blocks.dtype)

    for offset in 0 ..< base.dim(0) {
        let values: MLXArray
        if offset == 0 {
            values = blocks
        } else {
            let padding = MLXArray.zeros(
                [batch, offset, groups, groupSize], dtype: blocks.dtype)
            values = concatenated(
                [padding, blocks[0..., ..<(length - offset), 0..., 0...]],
                axis: 1
            )
        }
        let kernel = base[offset]
            .reshaped(1, 1, groups, groupSize)
            .asType(hidden.dtype)
        output = output
            + kernel * values
            + dynamic[0..., 0..., offset, 0..., 0...] * values
    }
    return output.reshaped(hidden.shape)
}

final class DFlashGroupedDynamicCausalConvolution: Module {
    let kernelSize: Int
    let groupSize: Int

    @ParameterInfo(key: "base_kernel") var baseKernel: MLXArray
    @ModuleInfo(key: "kernel_projection") var kernelProjection: Linear

    init(hiddenSize: Int, kernelSize: Int, groupSize: Int) {
        self.kernelSize = kernelSize
        self.groupSize = groupSize
        _baseKernel.wrappedValue = MLXArray.zeros([2, kernelSize, hiddenSize])
        _kernelProjection.wrappedValue = Linear(
            hiddenSize,
            2 * kernelSize * (hiddenSize / groupSize),
            bias: false
        )
    }

    func prepare(_ hidden: MLXArray) -> (MLXArray, MLXArray) {
        let groups = hidden.dim(-1) / groupSize
        let dynamic = kernelProjection(hidden).reshaped(
            hidden.dim(0), hidden.dim(1), 2, kernelSize, groups)
        let parts = dynamic.split(parts: 2, axis: 2)
        return (
            dflashGroupedDynamicConvolution(
                hidden: hidden,
                dynamic: parts[0].squeezed(axis: 2),
                base: baseKernel[0],
                groupSize: groupSize
            ),
            parts[1].squeezed(axis: 2)
        )
    }

    func finish(_ hidden: MLXArray, dynamic: MLXArray) -> MLXArray {
        dflashGroupedDynamicConvolution(
            hidden: hidden,
            dynamic: dynamic,
            base: baseKernel[1],
            groupSize: groupSize
        )
    }
}

final class DFlashDecoderLayer: Module {
    @ModuleInfo(key: "self_attn") var attention: DFlashAttention
    @ModuleInfo(key: "mlp") var mlp: Qwen3MLP
    @ModuleInfo(key: "input_layernorm") var inputNorm: RMSNorm
    @ModuleInfo(key: "post_attention_layernorm") var postAttentionNorm: RMSNorm
    @ModuleInfo(key: "attention_conv") var attentionConvolution:
        DFlashGroupedDynamicCausalConvolution?
    @ModuleInfo(key: "mlp_conv") var mlpConvolution:
        DFlashGroupedDynamicCausalConvolution?

    init(_ config: DFlashConfiguration, layerIndex: Int) {
        _attention.wrappedValue = DFlashAttention(config, layerIndex: layerIndex)
        _mlp.wrappedValue = Qwen3MLP(
            dimensions: config.hiddenSize,
            hiddenDimensions: config.intermediateSize
        )
        _inputNorm.wrappedValue = RMSNorm(
            dimensions: config.hiddenSize, eps: config.rmsNormEps)
        _postAttentionNorm.wrappedValue = RMSNorm(
            dimensions: config.hiddenSize, eps: config.rmsNormEps)
        if config.variant == .dflash2 {
            _attentionConvolution.wrappedValue = DFlashGroupedDynamicCausalConvolution(
                hiddenSize: config.hiddenSize,
                kernelSize: config.convolutionKernelSize,
                groupSize: config.convolutionGroupSize
            )
            _mlpConvolution.wrappedValue = DFlashGroupedDynamicCausalConvolution(
                hiddenSize: config.hiddenSize,
                kernelSize: config.convolutionKernelSize,
                groupSize: config.convolutionGroupSize
            )
        } else {
            _attentionConvolution.wrappedValue = nil
            _mlpConvolution.wrappedValue = nil
        }
    }

    func callAsFunction(_ input: MLXArray, context: MLXArray, cache: KVCache) -> MLXArray {
        if let attentionConvolution, let mlpConvolution {
            let residual = input
            let (attentionInput, attentionKernel) = attentionConvolution.prepare(
                inputNorm(input))
            let attended = residual + attentionConvolution.finish(
                attention(attentionInput, context: context, cache: cache),
                dynamic: attentionKernel
            )
            let (mlpInput, mlpKernel) = mlpConvolution.prepare(
                postAttentionNorm(attended))
            return attended + mlpConvolution.finish(
                mlp(mlpInput),
                dynamic: mlpKernel
            )
        }
        let attended = input + attention(inputNorm(input), context: context, cache: cache)
        return attended + mlp(postAttentionNorm(attended))
    }
}

final class DFlashCandidateSelector: Module {
    let topK: Int

    @ModuleInfo(key: "predecessor_codebook") var predecessorCodebook: Embedding
    @ModuleInfo(key: "successor_codebook") var successorCodebook: Embedding
    @ModuleInfo(key: "hidden_projection") var hiddenProjection: Linear

    init(_ configuration: DFlashConfiguration) {
        topK = configuration.selectorTopK
        _predecessorCodebook.wrappedValue = Embedding(
            embeddingCount: configuration.vocabularySize,
            dimensions: configuration.selectorRank
        )
        _successorCodebook.wrappedValue = Embedding(
            embeddingCount: configuration.vocabularySize,
            dimensions: configuration.selectorRank
        )
        _hiddenProjection.wrappedValue = Linear(
            configuration.hiddenSize,
            configuration.selectorRank,
            bias: false
        )
    }

    func select(
        hidden: MLXArray,
        logits: MLXArray,
        anchorToken: MLXArray,
        sampling: DFlashSamplingParameters,
        randomState: MLXRandom.RandomState
    ) -> DFlashDraftProposal {
        let candidates = argPartition(
            -logits,
            kth: topK - 1,
            axis: -1
        )[0..., 0..., ..<topK]
        let unary = takeAlong(logits, candidates, axis: -1)
        let projectedHidden = hiddenProjection(hidden)
        var predecessor = anchorToken.flattened()
        var path = [MLXArray]()
        var probabilityRows = [MLXArray]()
        path.reserveCapacity(hidden.dim(1))
        probabilityRows.reserveCapacity(hidden.dim(1))

        for position in 0 ..< hidden.dim(1) {
            let positionCandidates = candidates[0..., position, 0...]
            let edges = (
                predecessorCodebook(predecessor)[0..., .newAxis, 0...]
                    * projectedHidden[0..., position, 0...][0..., .newAxis, 0...]
                    * successorCodebook(positionCandidates)
            ).sum(axis: -1)
            let scores = unary[0..., position, 0...] + edges
            let selected: MLXArray
            if sampling.isGreedy {
                selected = argMax(scores, axis: -1)
            } else {
                let selectorSampling = DFlashSamplingParameters(
                    temperature: sampling.temperature,
                    topP: 1,
                    topK: 0,
                    minP: 0
                )
                let probabilities = dflashSamplingProbabilities(
                    logits: scores,
                    parameters: selectorSampling
                )
                selected = dflashSample(
                    probabilities: probabilities,
                    randomState: randomState
                )
                probabilityRows.append(probabilities)
            }
            predecessor = takeAlong(
                positionCandidates,
                selected[0..., .newAxis],
                axis: -1
            ).squeezed(axis: -1)
            path.append(predecessor)
        }

        return DFlashDraftProposal(
            tokens: stacked(path, axis: 1),
            probabilities: probabilityRows.isEmpty
                ? nil
                : stacked(probabilityRows, axis: 1),
            probabilityIndices: sampling.isGreedy ? nil : candidates
        )
    }
}

public final class Qwen3DFlashDraftModel: Module, DFlashDrafterModel, @unchecked Sendable {
    public let configuration: DFlashConfiguration
    public let dflashDescriptor: DFlashDrafterDescriptor

    @ModuleInfo(key: "fc") var contextProjection: Linear
    @ModuleInfo(key: "hidden_norm") var contextNorm: RMSNorm
    @ModuleInfo(key: "layers") var layers: [DFlashDecoderLayer]
    @ModuleInfo(key: "norm") var finalNorm: RMSNorm
    @ModuleInfo(key: "candidate_selector") var candidateSelector: DFlashCandidateSelector?

    public init(_ configuration: DFlashConfiguration) throws {
        try configuration.validate()
        self.configuration = configuration
        dflashDescriptor = DFlashDrafterDescriptor(
            variant: configuration.variant,
            targetModelType: configuration.modelType,
            targetLayerIDs: configuration.draft.targetLayerIDs,
            targetLayerCount: configuration.targetLayerCount,
            blockSize: configuration.effectiveBlockSize,
            maskTokenID: configuration.draft.maskTokenID
        )
        _contextProjection.wrappedValue = Linear(
            configuration.draft.targetLayerIDs.count * configuration.hiddenSize,
            configuration.hiddenSize,
            bias: false
        )
        _contextNorm.wrappedValue = RMSNorm(
            dimensions: configuration.hiddenSize,
            eps: configuration.rmsNormEps
        )
        _layers.wrappedValue = (0 ..< configuration.hiddenLayers).map { layerIndex in
            DFlashDecoderLayer(configuration, layerIndex: layerIndex)
        }
        _finalNorm.wrappedValue = RMSNorm(
            dimensions: configuration.hiddenSize,
            eps: configuration.rmsNormEps
        )
        if configuration.variant == .dflash2 {
            _candidateSelector.wrappedValue = DFlashCandidateSelector(configuration)
        } else {
            _candidateSelector.wrappedValue = nil
        }
    }

    public func validate(target: any LanguageModel) throws {
        guard let target = target as? any DFlashTargetModel else {
            throw DFlashError.unsupportedTarget(String(describing: type(of: target)))
        }
        guard target.dflashModelType == "qwen3" || target.dflashModelType == "qwen3_5" else {
            throw DFlashError.unsupportedTarget(target.dflashModelType)
        }
        guard target.dflashLayerCount == dflashDescriptor.targetLayerCount else {
            throw DFlashError.incompatibleTarget(
                "target layer 數預期 \(dflashDescriptor.targetLayerCount)，實際 \(target.dflashLayerCount)。")
        }
        guard target.dflashHiddenSize == configuration.hiddenSize else {
            throw DFlashError.incompatibleTarget(
                "hidden size 預期 \(configuration.hiddenSize)，實際 \(target.dflashHiddenSize)。")
        }
        guard target.dflashVocabularySize == configuration.vocabularySize else {
            throw DFlashError.incompatibleTarget(
                "vocabulary size 預期 \(configuration.vocabularySize)，實際 \(target.dflashVocabularySize)。")
        }
        guard dflashDescriptor.targetLayerIDs.allSatisfy({
            (0 ..< target.dflashLayerCount).contains($0)
        }) else {
            throw DFlashError.incompatibleTarget("target_layer_ids 超出 target model 範圍。")
        }
    }

    public func newCache() -> [KVCache] {
        configuration.layerTypes.map { layerType -> KVCache in
            if layerType == "sliding_attention" {
                return RotatingKVCache(
                    maxSize: configuration.slidingWindow! - 1,
                    keep: 0
                )
            }
            return KVCacheSimple()
        }
    }

    public func propose(
        target: any DFlashTargetModel,
        anchorToken: MLXArray,
        targetHiddenStates: MLXArray,
        cache: [KVCache],
        blockSize: Int,
        sampling: DFlashSamplingParameters,
        randomState: MLXRandom.RandomState
    ) throws -> DFlashDraftProposal {
        guard blockSize >= 2, blockSize <= dflashDescriptor.blockSize else {
            throw DFlashError.invalidDraftConfiguration(
                "執行 block size \(blockSize) 超過 draft 訓練值 \(dflashDescriptor.blockSize)。")
        }
        guard cache.count == layers.count else {
            throw DFlashError.invalidDraftConfiguration("draft KV Cache 層數不一致。")
        }

        let anchor = anchorToken.ndim == 1
            ? anchorToken.reshaped([anchorToken.dim(0), 1])
            : anchorToken
        guard anchor.ndim == 2, anchor.dim(0) == 1, anchor.dim(1) == 1 else {
            throw DFlashError.unsupportedGeneration("DFlash 只支援 batch size 1。")
        }
        guard targetHiddenStates.ndim == 3,
              targetHiddenStates.dim(0) == 1,
              targetHiddenStates.dim(1) > 0,
              targetHiddenStates.dim(2)
                == configuration.hiddenSize * dflashDescriptor.targetLayerIDs.count else {
            throw DFlashError.incompatibleTarget("target hidden states 的形狀與 Draft 不相容。")
        }
        let masks = MLXArray(
            Array(repeating: Int32(dflashDescriptor.maskTokenID), count: blockSize - 1)
        )[.newAxis]
        let block = concatenated([anchor, masks], axis: 1)

        var hidden = target.dflashEmbed(block)
            * configuration.effectiveInputEmbeddingScale
        let context = contextNorm(contextProjection(targetHiddenStates))
        for (index, layer) in layers.enumerated() {
            hidden = layer(hidden, context: context, cache: cache[index])
        }
        hidden = finalNorm(hidden)[0..., 1..., 0...]

        var logits = target.dflashComputeLogits(hidden)
        logits = logits * configuration.effectiveOutputMultiplier
        if let cap = configuration.effectiveFinalLogitSoftcapping, cap > 0 {
            logits = tanh(logits / cap) * cap
        }
        if let candidateSelector {
            return candidateSelector.select(
                hidden: hidden,
                logits: logits,
                anchorToken: anchor,
                sampling: sampling,
                randomState: randomState
            )
        }
        if sampling.isGreedy {
            return DFlashDraftProposal(tokens: argMax(logits, axis: -1))
        }
        let probabilities = dflashSamplingProbabilities(
            logits: logits,
            parameters: sampling
        )
        return DFlashDraftProposal(
            tokens: dflashSample(
                probabilities: probabilities,
                randomState: randomState
            ),
            probabilities: probabilities
        )
    }

    public func sanitize(weights: [String: MLXArray]) -> [String: MLXArray] {
        guard configuration.variant == .dflash2 else { return weights }
        var weights = weights
        for name in ["predecessor_codebook", "successor_codebook"] {
            let source = "candidate_selector.\(name)"
            if let value = weights.removeValue(forKey: source) {
                weights["\(source).weight"] = value
            }
        }
        return weights
    }
}

// MARK: - Local checkpoint loader

public enum DFlashModelFactory {
    public static func load(from directory: URL) throws -> any DFlashDrafterModel {
        let configurationURL = directory.appending(component: "config.json")
        let data = try Data(contentsOf: configurationURL)
        let decoder = JSONDecoder.json5()
        let configuration = try decoder.decode(DFlashConfiguration.self, from: data)
        let baseConfiguration = try decoder.decode(BaseConfiguration.self, from: data)
        let model = try Qwen3DFlashDraftModel(configuration)
        try loadWeights(
            modelDirectory: directory,
            model: model,
            perLayerQuantization: baseConfiguration.perLayerQuantization
        )
        return model
    }
}
