//
//  Qwen35.swift
//  mlx-swift-lm
//
//  Created by John Mai on 2026/2/9.
//
//  Port of https://github.com/ml-explore/mlx-lm/blob/main/mlx_lm/models/qwen3_5.py
//

import Foundation
import MLX
import MLXLMCommon
import MLXNN

// MARK: - Configuration

private enum RopeParametersCodingKey: String, CodingKey {
    case ropeParameters = "rope_parameters"
}

public struct Qwen35TextConfiguration: Codable, Sendable {
    var modelType: String = ""
    var hiddenSize: Int = 4096
    var hiddenLayers: Int = 32
    var intermediateSize: Int = 14336
    var attentionHeads: Int = 32
    var kvHeads: Int = 8
    var linearNumValueHeads: Int = 64
    var linearNumKeyHeads: Int = 16
    var linearKeyHeadDim: Int = 192
    var linearValueHeadDim: Int = 128
    var linearConvKernelDim: Int = 4
    var rmsNormEps: Float = 1e-6
    var vocabularySize: Int = 151_936
    var ropeTheta: Float = 100000.0
    var partialRotaryFactor: Float = 0.25
    var maxPositionEmbeddings: Int = 131072
    var tieWordEmbeddings: Bool = false
    var attentionBias: Bool = false
    var headDim: Int?
    var ropeScaling: [String: StringOrNumber]?
    var fullAttentionInterval: Int = 4
    var mtpNumHiddenLayers: Int = 0
    var mtpUseDedicatedEmbeddings: Bool = false

    // MoE fields
    var numExperts: Int = 0
    var numExpertsPerTok: Int = 0
    var decoderSparseStep: Int = 1
    var sharedExpertIntermediateSize: Int = 0
    var moeIntermediateSize: Int = 0
    var normTopkProb: Bool = true

    enum CodingKeys: String, CodingKey {
        case modelType = "model_type"
        case hiddenSize = "hidden_size"
        case hiddenLayers = "num_hidden_layers"
        case intermediateSize = "intermediate_size"
        case attentionHeads = "num_attention_heads"
        case kvHeads = "num_key_value_heads"
        case linearNumValueHeads = "linear_num_value_heads"
        case linearNumKeyHeads = "linear_num_key_heads"
        case linearKeyHeadDim = "linear_key_head_dim"
        case linearValueHeadDim = "linear_value_head_dim"
        case linearConvKernelDim = "linear_conv_kernel_dim"
        case rmsNormEps = "rms_norm_eps"
        case vocabularySize = "vocab_size"
        case ropeTheta = "rope_theta"
        case partialRotaryFactor = "partial_rotary_factor"
        case maxPositionEmbeddings = "max_position_embeddings"
        case tieWordEmbeddings = "tie_word_embeddings"
        case attentionBias = "attention_bias"
        case headDim = "head_dim"
        case ropeScaling = "rope_scaling"
        case fullAttentionInterval = "full_attention_interval"
        case mtpNumHiddenLayers = "mtp_num_hidden_layers"
        case mtpUseDedicatedEmbeddings = "mtp_use_dedicated_embeddings"
        case numExperts = "num_experts"
        case numExpertsPerTok = "num_experts_per_tok"
        case decoderSparseStep = "decoder_sparse_step"
        case sharedExpertIntermediateSize = "shared_expert_intermediate_size"
        case moeIntermediateSize = "moe_intermediate_size"
        case normTopkProb = "norm_topk_prob"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        let defaultRopeParameters: [String: StringOrNumber] = [
            "type": .string("default"),
            "mrope_section": .ints([11, 11, 10]),
            "rope_theta": .float(100000.0),
            "partial_rotary_factor": .float(0.25),
        ]

        self.modelType = try container.decodeIfPresent(String.self, forKey: .modelType) ?? ""
        self.hiddenSize = try container.decodeIfPresent(Int.self, forKey: .hiddenSize) ?? 4096
        self.hiddenLayers = try container.decodeIfPresent(Int.self, forKey: .hiddenLayers) ?? 32
        self.intermediateSize =
            try container.decodeIfPresent(Int.self, forKey: .intermediateSize) ?? 14336
        self.attentionHeads = try container.decodeIfPresent(Int.self, forKey: .attentionHeads) ?? 32
        self.kvHeads = try container.decodeIfPresent(Int.self, forKey: .kvHeads) ?? 8
        self.linearNumValueHeads =
            try container.decodeIfPresent(Int.self, forKey: .linearNumValueHeads) ?? 64
        self.linearNumKeyHeads =
            try container.decodeIfPresent(Int.self, forKey: .linearNumKeyHeads) ?? 16
        self.linearKeyHeadDim =
            try container.decodeIfPresent(Int.self, forKey: .linearKeyHeadDim) ?? 192
        self.linearValueHeadDim =
            try container.decodeIfPresent(Int.self, forKey: .linearValueHeadDim) ?? 128
        self.linearConvKernelDim =
            try container.decodeIfPresent(Int.self, forKey: .linearConvKernelDim) ?? 4
        self.rmsNormEps = try container.decodeIfPresent(Float.self, forKey: .rmsNormEps) ?? 1e-6
        self.vocabularySize =
            try container.decodeIfPresent(Int.self, forKey: .vocabularySize) ?? 151_936
        self.maxPositionEmbeddings =
            try container.decodeIfPresent(Int.self, forKey: .maxPositionEmbeddings) ?? 131072
        self.tieWordEmbeddings =
            try container.decodeIfPresent(Bool.self, forKey: .tieWordEmbeddings) ?? false
        self.attentionBias =
            try container.decodeIfPresent(Bool.self, forKey: .attentionBias) ?? false
        self.headDim = try container.decodeIfPresent(Int.self, forKey: .headDim)
        self.fullAttentionInterval =
            try container.decodeIfPresent(Int.self, forKey: .fullAttentionInterval) ?? 4
        self.mtpNumHiddenLayers =
            try container.decodeIfPresent(Int.self, forKey: .mtpNumHiddenLayers) ?? 0
        self.mtpUseDedicatedEmbeddings =
            try container.decodeIfPresent(Bool.self, forKey: .mtpUseDedicatedEmbeddings) ?? false

        // MoE fields
        self.numExperts = try container.decodeIfPresent(Int.self, forKey: .numExperts) ?? 0
        self.numExpertsPerTok =
            try container.decodeIfPresent(Int.self, forKey: .numExpertsPerTok) ?? 0
        self.decoderSparseStep =
            try container.decodeIfPresent(Int.self, forKey: .decoderSparseStep) ?? 1
        self.sharedExpertIntermediateSize =
            try container.decodeIfPresent(Int.self, forKey: .sharedExpertIntermediateSize) ?? 0
        self.moeIntermediateSize =
            try container.decodeIfPresent(Int.self, forKey: .moeIntermediateSize) ?? 0
        self.normTopkProb = try container.decodeIfPresent(Bool.self, forKey: .normTopkProb) ?? true

        let ropeContainer = try decoder.container(keyedBy: RopeParametersCodingKey.self)
        let ropeParameters = try ropeContainer.decodeIfPresent(
            [String: StringOrNumber].self, forKey: .ropeParameters)

        if var ropeParameters {
            if ropeParameters["type"] == nil, let ropeType = ropeParameters["rope_type"] {
                ropeParameters["type"] = ropeType
            }
            self.ropeTheta = ropeParameters["rope_theta"]?.asFloat() ?? 100000.0
            self.partialRotaryFactor =
                ropeParameters["partial_rotary_factor"]?.asFloat() ?? 0.25
            self.ropeScaling = ropeParameters
        } else {
            self.ropeTheta =
                try container.decodeIfPresent(Float.self, forKey: .ropeTheta) ?? 100000.0
            self.partialRotaryFactor =
                try container.decodeIfPresent(Float.self, forKey: .partialRotaryFactor) ?? 0.25
            self.ropeScaling =
                try container.decodeIfPresent([String: StringOrNumber].self, forKey: .ropeScaling)
                ?? defaultRopeParameters
        }

        if self.headDim == nil {
            self.headDim = self.hiddenSize / self.attentionHeads
        }
    }
}

// MARK: - GatedDeltaNet

final class Qwen35GatedDeltaNet: Module {
    let hiddenSize: Int
    let numVHeads: Int
    let numKHeads: Int
    let headKDim: Int
    let headVDim: Int
    let keyDim: Int
    let valueDim: Int
    let convKernelSize: Int
    let convDim: Int

    @ModuleInfo(key: "conv1d") var conv1d: Conv1d
    @ModuleInfo(key: "in_proj_qkv") var inProjQKV: Linear
    @ModuleInfo(key: "in_proj_z") var inProjZ: Linear
    @ModuleInfo(key: "in_proj_b") var inProjB: Linear
    @ModuleInfo(key: "in_proj_a") var inProjA: Linear

    @ParameterInfo(key: "dt_bias") var dtBias: MLXArray
    @ParameterInfo(key: "A_log") var aLog: MLXArray

    @ModuleInfo(key: "norm") var norm: Qwen3NextRMSNormGated
    @ModuleInfo(key: "out_proj") var outProj: Linear

    init(_ args: Qwen35TextConfiguration) {
        self.hiddenSize = args.hiddenSize
        self.numVHeads = args.linearNumValueHeads
        self.numKHeads = args.linearNumKeyHeads
        self.headKDim = args.linearKeyHeadDim
        self.headVDim = args.linearValueHeadDim
        self.keyDim = headKDim * numKHeads
        self.valueDim = headVDim * numVHeads
        self.convKernelSize = args.linearConvKernelDim
        self.convDim = keyDim * 2 + valueDim

        precondition(
            numVHeads % numKHeads == 0,
            "num_v_heads (\(numVHeads)) must be divisible by num_k_heads (\(numKHeads))"
        )

        _conv1d.wrappedValue = Conv1d(
            inputChannels: convDim,
            outputChannels: convDim,
            kernelSize: convKernelSize,
            stride: 1,
            padding: 0,
            dilation: 1,
            groups: convDim,
            bias: false
        )

        _inProjQKV.wrappedValue = Linear(hiddenSize, keyDim * 2 + valueDim, bias: false)
        _inProjZ.wrappedValue = Linear(hiddenSize, valueDim, bias: false)
        _inProjB.wrappedValue = Linear(hiddenSize, numVHeads, bias: false)
        _inProjA.wrappedValue = Linear(hiddenSize, numVHeads, bias: false)

        _dtBias.wrappedValue = MLXArray.ones([numVHeads])
        let a = MLXRandom.uniform(low: 0, high: 16, [numVHeads])
        _aLog.wrappedValue = log(a)

        _norm.wrappedValue = Qwen3NextRMSNormGated(dimensions: headVDim, eps: args.rmsNormEps)
        _outProj.wrappedValue = Linear(valueDim, hiddenSize, bias: false)

        super.init()
    }

    func callAsFunction(
        _ inputs: MLXArray,
        mask: MLXArray? = nil,
        cache: MambaCache? = nil,
        checkpointAfter: Int? = nil
    ) -> MLXArray {
        dflashForward(
            inputs,
            mask: mask,
            cache: cache,
            captureRollback: false,
            checkpointAfter: checkpointAfter
        ).output
    }

    func dflashForward(
        _ inputs: MLXArray,
        mask: MLXArray? = nil,
        cache: MambaCache? = nil,
        captureRollback: Bool,
        checkpointAfter: Int? = nil
    ) -> (output: MLXArray, rollback: Qwen35DFlashLinearCapture?) {
        let B = inputs.dim(0)
        let S = inputs.dim(1)

        var qkv = inProjQKV(inputs)
        let z = inProjZ(inputs).reshaped(B, S, numVHeads, headVDim)
        let b = inProjB(inputs)
        let a = inProjA(inputs)

        let convState: MLXArray
        if let cacheState = cache?[0] {
            convState = cacheState
        } else {
            convState = MLXArray.zeros([B, convKernelSize - 1, convDim], dtype: inputs.dtype)
        }

        if let mask {
            qkv = MLX.where(mask[.ellipsis, .newAxis], qkv, 0)
        }

        let convInput = concatenated([convState, qkv], axis: 1)
        if let cache {
            cache[0] = contiguous(convInput[0..., (-(convKernelSize - 1))..., 0...])
        }

        let convOut = silu(conv1d(convInput))

        let convSplit = MLX.split(convOut, indices: [keyDim, 2 * keyDim], axis: -1)
        let q = convSplit[0].reshaped(B, S, numKHeads, headKDim)
        let k = convSplit[1].reshaped(B, S, numKHeads, headKDim)
        let v = convSplit[2].reshaped(B, S, numVHeads, headVDim)

        let priorDeltaState = cache?[1]
        var state = priorDeltaState
        let dtype = q.dtype
        let invScale = pow(Float(headKDim), -0.5)
        let qNormed =
            MLXArray(pow(invScale, 2)).asType(dtype)
            * MLXFast.rmsNorm(q, weight: MLXArray.mlxNone, eps: 1e-6)
        let kNormed =
            MLXArray(invScale).asType(dtype)
            * MLXFast.rmsNorm(k, weight: MLXArray.mlxNone, eps: 1e-6)

        var out: MLXArray
        var checkpoint: (conv: MLXArray, recurrent: MLXArray)?

        if let split = checkpointAfter, split > 0, split < S {
            let prefixMask = mask.map { $0[.ellipsis, ..<split] }
            let suffixMask = mask.map { $0[.ellipsis, split...] }
            let (prefixOut, prefixState) = gatedDeltaUpdate(
                q: qNormed[0..., ..<split, 0..., 0...],
                k: kNormed[0..., ..<split, 0..., 0...],
                v: v[0..., ..<split, 0..., 0...],
                a: a[0..., ..<split, 0...],
                b: b[0..., ..<split, 0...],
                aLog: aLog,
                dtBias: dtBias,
                state: state,
                mask: prefixMask)
            let (suffixOut, suffixState) = gatedDeltaUpdate(
                q: qNormed[0..., split..., 0..., 0...],
                k: kNormed[0..., split..., 0..., 0...],
                v: v[0..., split..., 0..., 0...],
                a: a[0..., split..., 0...],
                b: b[0..., split..., 0...],
                aLog: aLog,
                dtBias: dtBias,
                state: prefixState,
                mask: suffixMask)
            out = concatenated([prefixOut, suffixOut], axis: 1)
            state = suffixState
            let checkpointConv = contiguous(
                convInput[0..., split ..< (split + convKernelSize - 1), 0...])
            checkpoint = (checkpointConv, prefixState)
        } else {
            (out, state) = gatedDeltaUpdate(
                q: qNormed,
                k: kNormed,
                v: v,
                a: a,
                b: b,
                aLog: aLog,
                dtBias: dtBias,
                state: state,
                mask: mask)
        }

        if let cache {
            cache[1] = state
            if let checkpoint, let checkpointAfter {
                cache.saveSpeculativeCheckpoint(
                    convState: checkpoint.conv,
                    recurrentState: checkpoint.recurrent,
                    advancedBy: checkpointAfter)
            }
            cache.advance(S)
        }

        let rollback = captureRollback && cache != nil
            ? Qwen35DFlashLinearCapture(
                attention: self,
                cache: cache!,
                convolutionInput: convInput,
                queries: qNormed,
                keys: kNormed,
                values: v,
                a: a,
                b: b,
                priorDeltaState: priorDeltaState,
                mask: mask
            )
            : nil
        out = norm(out, gate: z)
        return (outProj(out.reshaped(B, S, -1)), rollback)
    }
}

// MARK: - Attention

final class Qwen35Attention: Module {
    let attentionHeads: Int
    let kvHeads: Int
    let scale: Float

    @ModuleInfo(key: "q_proj") var qProj: Linear
    @ModuleInfo(key: "k_proj") var kProj: Linear
    @ModuleInfo(key: "v_proj") var vProj: Linear
    @ModuleInfo(key: "o_proj") var oProj: Linear

    @ModuleInfo(key: "q_norm") var qNorm: RMSNorm
    @ModuleInfo(key: "k_norm") var kNorm: RMSNorm

    let rope: RoPELayer

    init(_ args: Qwen35TextConfiguration) {
        let headDim = args.headDim ?? (args.hiddenSize / args.attentionHeads)
        self.attentionHeads = args.attentionHeads
        self.kvHeads = args.kvHeads
        self.scale = pow(Float(headDim), -0.5)

        _qProj.wrappedValue = Linear(
            args.hiddenSize, args.attentionHeads * headDim * 2, bias: args.attentionBias)
        _kProj.wrappedValue = Linear(
            args.hiddenSize, args.kvHeads * headDim, bias: args.attentionBias)
        _vProj.wrappedValue = Linear(
            args.hiddenSize, args.kvHeads * headDim, bias: args.attentionBias)
        _oProj.wrappedValue = Linear(
            args.attentionHeads * headDim, args.hiddenSize, bias: args.attentionBias)

        _qNorm.wrappedValue = RMSNorm(dimensions: headDim, eps: args.rmsNormEps)
        _kNorm.wrappedValue = RMSNorm(dimensions: headDim, eps: args.rmsNormEps)

        let ropeDims = Int(Float(headDim) * args.partialRotaryFactor)
        self.rope = initializeRope(
            dims: max(1, ropeDims),
            base: args.ropeTheta,
            traditional: false,
            scalingConfig: args.ropeScaling,
            maxPositionEmbeddings: args.maxPositionEmbeddings
        )

        super.init()
    }

    func callAsFunction(
        _ x: MLXArray,
        mask: MLXFast.ScaledDotProductAttentionMaskMode,
        cache: KVCache?,
        positionOffset: Int? = nil
    ) -> MLXArray {
        let B = x.dim(0)
        let L = x.dim(1)

        let qProjOutput = qProj(x)
        let qSplit = qProjOutput.reshaped(B, L, attentionHeads, -1).split(parts: 2, axis: -1)
        var queries = qSplit[0]
        let gate = qSplit[1].reshaped(B, L, -1)

        var keys = kProj(x)
        var values = vProj(x)

        queries = qNorm(queries).transposed(0, 2, 1, 3)
        keys = kNorm(keys.reshaped(B, L, kvHeads, -1)).transposed(0, 2, 1, 3)
        values = values.reshaped(B, L, kvHeads, -1).transposed(0, 2, 1, 3)

        let offset = positionOffset.map(RoPEOffset.scalar) ?? cache?.ropeOffset
        queries = applyRotaryPosition(rope, to: queries, offset: offset)
        keys = applyRotaryPosition(rope, to: keys, offset: offset)

        let output = attentionWithCacheUpdate(
            queries: queries,
            keys: keys,
            values: values,
            cache: cache,
            scale: scale,
            mask: mask
        )
        .transposed(0, 2, 1, 3)
        .reshaped(B, L, -1)

        return oProj(sigmoidMultiply(output, gate))
    }
}

// MARK: - SparseMoeBlock

final class Qwen35SparseMoeBlock: Module, UnaryLayer {
    let normTopkProb: Bool
    let numExperts: Int
    let topK: Int

    @ModuleInfo(key: "gate") var gate: Linear
    @ModuleInfo(key: "switch_mlp") var switchMLP: SwitchGLU

    @ModuleInfo(key: "shared_expert") var sharedExpert: Qwen3NextMLP
    @ModuleInfo(key: "shared_expert_gate") var sharedExpertGate: Linear

    init(_ args: Qwen35TextConfiguration) {
        self.normTopkProb = args.normTopkProb
        self.numExperts = args.numExperts
        self.topK = args.numExpertsPerTok

        _gate.wrappedValue = Linear(args.hiddenSize, args.numExperts, bias: false)
        _switchMLP.wrappedValue = SwitchGLU(
            inputDims: args.hiddenSize,
            hiddenDims: args.moeIntermediateSize,
            numExperts: args.numExperts
        )

        _sharedExpert.wrappedValue = Qwen3NextMLP(
            dimensions: args.hiddenSize,
            hiddenDimensions: args.sharedExpertIntermediateSize
        )
        _sharedExpertGate.wrappedValue = Linear(args.hiddenSize, 1, bias: false)
    }

    func callAsFunction(_ x: MLXArray) -> MLXArray {
        var gates = gate(x)
        gates = MLX.softmax(gates, axis: -1, precise: true)

        let k = topK
        let kth = gates.dim(-1) - k
        let inds = MLX.argPartition(gates, kth: kth, axis: -1)[.ellipsis, (kth)...]
        var scores = MLX.takeAlong(gates, inds, axis: -1)
        if normTopkProb {
            scores = scores / scores.sum(axis: -1, keepDims: true)
        }

        let y = switchMLP(x, inds)
        let combined = weightedExpertSum(y, scores)

        var sharedY = sharedExpert(x)
        sharedY = sigmoid(sharedExpertGate(x)) * sharedY

        return combined + sharedY
    }
}

// MARK: - Decoder Layer

final class Qwen35DecoderLayer: Module {
    let isLinear: Bool

    @ModuleInfo(key: "self_attn") var selfAttn: Qwen35Attention?
    @ModuleInfo(key: "linear_attn") var linearAttn: Qwen35GatedDeltaNet?

    @ModuleInfo(key: "input_layernorm") var inputLayerNorm: RMSNorm
    @ModuleInfo(key: "post_attention_layernorm") var postAttentionLayerNorm: RMSNorm

    @ModuleInfo(key: "mlp") var mlp: Module

    init(_ args: Qwen35TextConfiguration, layerIdx: Int, forceFullAttention: Bool = false) {
        self.isLinear =
            forceFullAttention ? false : (layerIdx + 1) % args.fullAttentionInterval != 0

        if isLinear {
            _linearAttn.wrappedValue = Qwen35GatedDeltaNet(args)
        } else {
            _selfAttn.wrappedValue = Qwen35Attention(args)
        }

        if args.numExperts > 0 {
            _mlp.wrappedValue = Qwen35SparseMoeBlock(args)
        } else {
            _mlp.wrappedValue = Qwen3NextMLP(
                dimensions: args.hiddenSize,
                hiddenDimensions: args.intermediateSize
            )
        }

        _inputLayerNorm.wrappedValue = RMSNorm(
            dimensions: args.hiddenSize,
            eps: args.rmsNormEps
        )
        _postAttentionLayerNorm.wrappedValue = RMSNorm(
            dimensions: args.hiddenSize,
            eps: args.rmsNormEps
        )

        super.init()
    }

    func callAsFunction(
        _ x: MLXArray,
        attentionMask: MLXFast.ScaledDotProductAttentionMaskMode,
        ssmMask: MLXArray?,
        cache: KVCache?,
        positionOffset: Int? = nil,
        checkpointAfter: Int? = nil
    ) -> MLXArray {
        dflashForward(
            x,
            attentionMask: attentionMask,
            ssmMask: ssmMask,
            cache: cache,
            captureRollback: false,
            positionOffset: positionOffset,
            checkpointAfter: checkpointAfter
        ).output
    }

    func dflashForward(
        _ x: MLXArray,
        attentionMask: MLXFast.ScaledDotProductAttentionMaskMode,
        ssmMask: MLXArray?,
        cache: KVCache?,
        captureRollback: Bool,
        positionOffset: Int? = nil,
        checkpointAfter: Int? = nil
    ) -> (output: MLXArray, rollback: Qwen35DFlashLinearCapture?) {
        let r: MLXArray
        var rollback: Qwen35DFlashLinearCapture?
        if isLinear {
            let normalized = inputLayerNorm(x)
            let output = linearAttn!.dflashForward(
                normalized,
                mask: ssmMask,
                cache: cache as? MambaCache,
                captureRollback: captureRollback,
                checkpointAfter: checkpointAfter
            )
            r = output.output
            rollback = output.rollback
        } else {
            r = selfAttn!(
                inputLayerNorm(x), mask: attentionMask, cache: cache,
                positionOffset: positionOffset)
        }

        let h = x + r
        return (h + (mlp as! UnaryLayer)(postAttentionLayerNorm(h)), rollback)
    }
}

final class Qwen35DFlashLinearCapture {
    let attention: Qwen35GatedDeltaNet
    let cache: MambaCache
    let convolutionInput: MLXArray
    let queries: MLXArray
    let keys: MLXArray
    let values: MLXArray
    let a: MLXArray
    let b: MLXArray
    let priorDeltaState: MLXArray?
    let mask: MLXArray?

    init(
        attention: Qwen35GatedDeltaNet,
        cache: MambaCache,
        convolutionInput: MLXArray,
        queries: MLXArray,
        keys: MLXArray,
        values: MLXArray,
        a: MLXArray,
        b: MLXArray,
        priorDeltaState: MLXArray?,
        mask: MLXArray?
    ) {
        self.attention = attention
        self.cache = cache
        self.convolutionInput = convolutionInput
        self.queries = queries
        self.keys = keys
        self.values = values
        self.a = a
        self.b = b
        self.priorDeltaState = priorDeltaState
        self.mask = mask
    }
}

final class Qwen35DFlashRollback: DFlashTargetRollback {
    let captures: [Qwen35DFlashLinearCapture]
    let targetCache: [KVCache]

    init(captures: [Qwen35DFlashLinearCapture], targetCache: [KVCache]) {
        self.captures = captures
        self.targetCache = targetCache
    }

    func commit(acceptedInputCount: Int, rejectedInputCount: Int) throws {
        guard acceptedInputCount > 0, rejectedInputCount > 0 else { return }
        guard captures.allSatisfy({
            $0.queries.dim(1) == acceptedInputCount + rejectedInputCount
                && $0.convolutionInput.dim(1)
                    == acceptedInputCount + rejectedInputCount
                        + $0.attention.convKernelSize - 1
        }) else {
            throw DFlashError.unsupportedGeneration(
                "Qwen3.5 Gated Delta Net rollback snapshot 不完整。")
        }

        for cache in targetCache where !(cache is MambaCache) {
            let trimmed = cache.trim(rejectedInputCount)
            guard trimmed == rejectedInputCount else {
                throw DFlashError.unsupportedGeneration(
                    "Qwen3.5 Attention KV Cache 回滾失敗。")
            }
        }

        for capture in captures {
            let mask: MLXArray?
            if let sourceMask = capture.mask {
                if sourceMask.ndim == 1 {
                    mask = sourceMask[..<acceptedInputCount]
                } else {
                    mask = sourceMask[0..., ..<acceptedInputCount]
                }
            } else {
                mask = nil
            }
            let (_, deltaState) = gatedDeltaUpdate(
                q: capture.queries[0..., ..<acceptedInputCount, 0..., 0...],
                k: capture.keys[0..., ..<acceptedInputCount, 0..., 0...],
                v: capture.values[0..., ..<acceptedInputCount, 0..., 0...],
                a: capture.a[0..., ..<acceptedInputCount, 0...],
                b: capture.b[0..., ..<acceptedInputCount, 0...],
                aLog: capture.attention.aLog,
                dtBias: capture.attention.dtBias,
                state: capture.priorDeltaState,
                mask: mask
            )
            let convolutionEnd = acceptedInputCount + capture.attention.convKernelSize - 1
            capture.cache[0] = contiguous(
                capture.convolutionInput[
                    0..., acceptedInputCount ..< convolutionEnd, 0...])
            capture.cache[1] = deltaState
        }
        eval(targetCache)
    }
}

// MARK: - Text Model

public class Qwen35TextModelInner: Module {
    @ModuleInfo(key: "embed_tokens") var embedTokens: Embedding

    fileprivate let layers: [Qwen35DecoderLayer]
    let norm: RMSNorm

    let ssmIdx: Int
    let faIdx: Int

    init(_ args: Qwen35TextConfiguration) {
        precondition(args.vocabularySize > 0)

        _embedTokens.wrappedValue = Embedding(
            embeddingCount: args.vocabularySize,
            dimensions: args.hiddenSize
        )

        self.layers = (0 ..< args.hiddenLayers).map { layerIdx in
            Qwen35DecoderLayer(args, layerIdx: layerIdx)
        }

        self.norm = RMSNorm(dimensions: args.hiddenSize, eps: args.rmsNormEps)

        self.ssmIdx = 0
        self.faIdx = args.fullAttentionInterval - 1

        super.init()
    }

    func callAsFunction(_ inputs: MLXArray, cache: [KVCache?]? = nil) -> MLXArray {
        forward(inputs, cache: cache, applyFinalNorm: true)
    }

    /// Runs the target backbone while optionally preserving the residual
    /// representation needed by a paired MTP head.
    func forward(
        _ inputs: MLXArray,
        cache: [KVCache?]? = nil,
        applyFinalNorm: Bool,
        checkpointAfter: Int? = nil
    ) -> MLXArray {
        var hiddenStates = embedTokens(inputs)

        var cacheArray = cache
        if cacheArray == nil {
            cacheArray = Array(repeating: nil as KVCache?, count: layers.count)
        }

        let faMask = createAttentionMask(h: hiddenStates, cache: cacheArray?[faIdx])
        let ssmMask = createSSMMask(h: hiddenStates, cache: cacheArray?[ssmIdx] as? MambaCache)

        for (i, layer) in layers.enumerated() {
            let mask = layer.isLinear ? ssmMask : nil
            let attnMask =
                layer.isLinear
                ? MLXFast.ScaledDotProductAttentionMaskMode.none : faMask
            hiddenStates = layer(
                hiddenStates, attentionMask: attnMask, ssmMask: mask, cache: cacheArray?[i],
                checkpointAfter: checkpointAfter)
        }

        return applyFinalNorm ? norm(hiddenStates) : hiddenStates
    }

    func dflashForward(
        _ inputs: MLXArray,
        cache: [KVCache]?,
        captureLayerIDs: [Int],
        captureRollback: Bool
    ) -> (hidden: MLXArray, captured: MLXArray, rollback: Qwen35DFlashRollback?) {
        precondition(!captureLayerIDs.isEmpty)
        var hiddenStates = embedTokens(inputs)
        let cacheArray: [KVCache?] = cache?.map { $0 as KVCache? }
            ?? Array(repeating: nil as KVCache?, count: layers.count)
        let faMask = createAttentionMask(h: hiddenStates, cache: cacheArray[faIdx])
        let ssmMask = createSSMMask(
            h: hiddenStates,
            cache: cacheArray[ssmIdx] as? MambaCache
        )
        let requested = Set(captureLayerIDs)
        var capturedByLayer = [Int: MLXArray]()
        var rollbackCaptures = [Qwen35DFlashLinearCapture]()

        for (index, layer) in layers.enumerated() {
            let mask = layer.isLinear ? ssmMask : nil
            let attentionMask = layer.isLinear
                ? MLXFast.ScaledDotProductAttentionMaskMode.none
                : faMask
            let output = layer.dflashForward(
                hiddenStates,
                attentionMask: attentionMask,
                ssmMask: mask,
                cache: cacheArray[index],
                captureRollback: captureRollback
            )
            hiddenStates = output.output
            if let rollback = output.rollback {
                rollbackCaptures.append(rollback)
            }
            if requested.contains(index) {
                capturedByLayer[index] = hiddenStates
            }
        }

        let captured = captureLayerIDs.map { layerID -> MLXArray in
            guard let value = capturedByLayer[layerID] else {
                preconditionFailure("DFlash capture layer \(layerID) 不存在")
            }
            return value
        }
        let rollback = captureRollback && cache != nil
            ? Qwen35DFlashRollback(captures: rollbackCaptures, targetCache: cache!)
            : nil
        return (norm(hiddenStates), concatenated(captured, axis: -1), rollback)
    }
}

public class Qwen35TextModel: Module, LLMModel, KVCacheDimensionProvider, DFlashTargetModel {
    public let vocabularySize: Int
    public let kvHeads: [Int]

    public let model: Qwen35TextModelInner
    let configuration: Qwen35TextConfiguration

    @ModuleInfo(key: "lm_head") var lmHead: Linear?

    public init(_ args: Qwen35TextConfiguration) {
        self.configuration = args
        self.vocabularySize = args.vocabularySize
        self.kvHeads = (0 ..< args.hiddenLayers).map { _ in args.kvHeads }
        self.model = Qwen35TextModelInner(args)

        if !args.tieWordEmbeddings {
            _lmHead.wrappedValue = Linear(args.hiddenSize, args.vocabularySize, bias: false)
        }
    }

    public func callAsFunction(_ inputs: MLXArray, cache: [KVCache]?) -> MLXArray {
        var out = model(inputs, cache: cache)
        if let lmHead {
            out = lmHead(out)
        } else {
            out = model.embedTokens.asLinear(out)
        }
        return out
    }

    public func callAsFunction(
        _ input: LMInput.Text, cache: [KVCache]?, state: LMOutput.State?
    ) -> LMOutput {
        let emitDrafterState = state?[mtpEmitFlagKey] ?? false
        let hiddenStates: MLXArray
        if emitDrafterState {
            let residual = model.forward(
                input.tokens,
                cache: cache,
                applyFinalNorm: false,
                checkpointAfter: state?[mtpCacheCheckpointIndexKey])
            hiddenStates = model.norm(residual)
        } else {
            hiddenStates = model(input.tokens, cache: cache)
        }

        let logits = dflashComputeLogits(hiddenStates)
        guard emitDrafterState else {
            return LMOutput(logits: logits)
        }

        var outputState = state ?? LMOutput.State()
        outputState[mtpLastHiddenStatesKey] = hiddenStates
        outputState[mtpSharedKVStatesKey] = qwen35SharedKVState(
            cache: cache, fullAttentionIndex: model.faIdx)
        outputState[mtpSharedKVOffsetsKey] = qwen35SharedKVOffsets(
            cache: cache, fullAttentionIndex: model.faIdx)
        outputState[mtpSharedKVSourceIndicesKey] = ["full_attention": model.faIdx]
        return LMOutput(logits: logits, state: outputState)
    }

    public var dflashModelType: String { "qwen3_5" }
    public var dflashHiddenSize: Int { configuration.hiddenSize }
    public var dflashLayerCount: Int { configuration.hiddenLayers }
    public var dflashVocabularySize: Int { vocabularySize }
    public var dflashSupportsNonTrimmableCacheRollback: Bool { true }

    public func dflashEmbed(_ tokens: MLXArray) -> MLXArray {
        model.embedTokens(tokens)
    }

    public func dflashComputeLogits(_ hiddenStates: MLXArray) -> MLXArray {
        if let lmHead {
            return lmHead(hiddenStates)
        }
        return model.embedTokens.asLinear(hiddenStates)
    }

    public func dflashForward(
        _ inputs: MLXArray,
        cache: [KVCache]?,
        captureLayerIDs: [Int],
        captureRollback: Bool
    ) -> DFlashTargetOutput {
        var output = model.dflashForward(
            inputs,
            cache: cache,
            captureLayerIDs: captureLayerIDs,
            captureRollback: captureRollback
        )
        output.hidden = dflashComputeLogits(output.hidden)
        return DFlashTargetOutput(
            logits: output.hidden,
            hiddenStates: output.captured,
            rollback: output.rollback
        )
    }

    public func newCache(parameters: GenerateParameters?) -> [KVCache] {
        return model.layers.map { layer in
            if layer.isLinear {
                return MambaCache()
            }
            return KVCacheSimple()
        }
    }

    public func sanitize(weights: [String: MLXArray]) -> [String: MLXArray] {
        let hasMTPWeights = weights.keys.contains { $0.contains("mtp.") }
        let hasUnsanitizedConv1d = weights.contains { key, value in
            key.contains("conv1d.weight") && value.dim(-1) != 1
        }
        let shouldShiftNormWeights = hasMTPWeights || hasUnsanitizedConv1d

        var weights = weights.filter { !$0.key.contains("mtp.") }

        if configuration.tieWordEmbeddings {
            weights["lm_head.weight"] = nil
        }

        let normKeys = [
            ".input_layernorm.weight",
            ".post_attention_layernorm.weight",
            "model.norm.weight",
            ".q_norm.weight",
            ".k_norm.weight",
        ]

        for k in Array(weights.keys) {
            guard let v = weights[k] else { continue }
            if k.contains("conv1d.weight") && v.dim(-1) != 1 {
                weights[k] = v.movedAxis(source: 2, destination: 1)
                continue
            }
            if shouldShiftNormWeights
                && normKeys.contains(where: { k.hasSuffix($0) })
                && v.ndim == 1
            {
                weights[k] = v + MLXArray(1, dtype: v.dtype)
            }
        }

        return weights
    }
}

private func qwen35SharedKVState(
    cache: [KVCache]?,
    fullAttentionIndex: Int
) -> [String: (MLXArray, MLXArray)] {
    guard let cache, fullAttentionIndex < cache.count else {
        return [:]
    }
    let state = cache[fullAttentionIndex].state
    guard state.count == 2 else {
        return [:]
    }
    return ["full_attention": (state[0], state[1])]
}

private func qwen35SharedKVOffsets(
    cache: [KVCache]?,
    fullAttentionIndex: Int
) -> [String: Int]? {
    guard let cache, fullAttentionIndex < cache.count else {
        return nil
    }
    return ["full_attention": cache[fullAttentionIndex].offset]
}

extension Qwen35TextModel: LoRAModel {
    public var loraLayers: [Module] {
        model.layers
    }
}

extension Qwen35TextModel: SpeculativeCacheRewindModel {
    public var maximumNativeTargetCacheRewind: Int { 1 }
}

// MARK: - Top-level Model

public class Qwen35Model: Module, LLMModel, KVCacheDimensionProvider, DFlashTargetModel {
    public let vocabularySize: Int
    public let kvHeads: [Int]

    @ModuleInfo(key: "language_model") var languageModel: Qwen35TextModel

    public init(_ args: Qwen35Configuration) {
        let textModel = Qwen35TextModel(args.textConfig)
        self.vocabularySize = textModel.vocabularySize
        self.kvHeads = textModel.kvHeads
        _languageModel.wrappedValue = textModel
    }

    public func callAsFunction(_ inputs: MLXArray, cache: [KVCache]?) -> MLXArray {
        languageModel(inputs, cache: cache)
    }

    public func callAsFunction(
        _ input: LMInput.Text, cache: [KVCache]?, state: LMOutput.State?
    ) -> LMOutput {
        languageModel(input, cache: cache, state: state)
    }

    public var dflashModelType: String { languageModel.dflashModelType }
    public var dflashHiddenSize: Int { languageModel.dflashHiddenSize }
    public var dflashLayerCount: Int { languageModel.dflashLayerCount }
    public var dflashVocabularySize: Int { languageModel.dflashVocabularySize }
    public var dflashSupportsNonTrimmableCacheRollback: Bool {
        languageModel.dflashSupportsNonTrimmableCacheRollback
    }

    public func dflashEmbed(_ tokens: MLXArray) -> MLXArray {
        languageModel.dflashEmbed(tokens)
    }

    public func dflashComputeLogits(_ hiddenStates: MLXArray) -> MLXArray {
        languageModel.dflashComputeLogits(hiddenStates)
    }

    public func dflashForward(
        _ inputs: MLXArray,
        cache: [KVCache]?,
        captureLayerIDs: [Int],
        captureRollback: Bool
    ) -> DFlashTargetOutput {
        languageModel.dflashForward(
            inputs,
            cache: cache,
            captureLayerIDs: captureLayerIDs,
            captureRollback: captureRollback
        )
    }

    public func newCache(parameters: GenerateParameters?) -> [KVCache] {
        languageModel.newCache(parameters: parameters)
    }

    public func sanitize(weights: [String: MLXArray]) -> [String: MLXArray] {
        var sanitized = [String: MLXArray]()
        for (key, value) in weights {
            if key.hasPrefix("vision_tower") || key.hasPrefix("model.visual") {
                continue
            }

            var key = key
            if key.hasPrefix("model.language_model") {
                key = key.replacingOccurrences(
                    of: "model.language_model", with: "language_model.model")
            } else if !key.hasPrefix("language_model.") {
                key = "language_model." + key
            }
            sanitized[key] = value
        }

        return languageModel.sanitize(weights: sanitized)
    }
}

extension Qwen35Model: LoRAModel {
    public var loraLayers: [Module] {
        languageModel.model.layers
    }
}

extension Qwen35Model: SpeculativeCacheRewindModel {
    public var maximumNativeTargetCacheRewind: Int { 1 }
}
