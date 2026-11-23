// Swift port of mlx-vlm Qwen4-Exp (633757c84b48b736fb1c259683aeca80e1384834).
// Copyright © 2025 Prince Canuma. MIT; see ../../../Qwen4Exp-LICENSE.txt.
import Foundation
import MLX
import MLXLMCommon
import MLXNN

public struct Qwen4ExpConfiguration: Codable, Sendable, ModelConfigurationValidating {
    var base: Qwen35TextConfiguration
    var hcCount: Int, hcLowrank: Int
    var indexHeads: Int, indexDim: Int, indexBudget: Int, compress: Int
    var pleLayers: [Int], pleDim: Int, pleKernel: Int, ngram: Int, headsPerNgram: Int
    var ngramVocab: Int, divisor: Int, shards: Int, seed: Int, eos: Int
    var layerTypes: [String]
    var pleSizes: [Int]?, pleOffsets: [Int]?, pleMultipliers: [UInt64]?, pleRows: Int?
    var sigmoidGate: Bool
    enum Key: String, CodingKey {
        case pleSizes = "ple_head_vocab_sizes"
        case pleOffsets = "ple_head_offsets"
        case pleMultipliers = "ple_layer_multipliers"
        case pleRows = "ple_table_rows"
        case text = "text_config"
        case hcCount = "hc_count"
        case hcLowrank = "hc_lowrank"
        case indexHeads = "indexer_n_heads"
        case indexDim = "indexer_head_dim"
        case indexKV = "indexer_kv_heads"
        case indexBudget = "indexer_budget"
        case compress = "indexer_compress_ratio"
        case pleLayers = "ple_layer_ids"
        case pleDim = "ple_embed_dim"
        case pleKernel = "ple_conv_kernel_size"
        case ngram = "ngram_size"
        case headsPerNgram = "heads_per_ngram"
        case ngramVocab = "ngram_vocab_size_base"
        case divisor = "make_ngram_vocab_size_divisible_by"
        case shards = "split_ngram_parts"
        case seed
        case eos = "eos_token_id"
        case layerTypes = "layer_types"
        case gate = "output_gate_type"
    }
    public init(from decoder: Decoder) throws {
        let root = try decoder.container(keyedBy: Key.self)
        let d = root.contains(.text) ? try root.superDecoder(forKey: .text) : decoder
        let c = try d.container(keyedBy: Key.self)
        base = try Qwen35TextConfiguration(from: d)
        pleSizes = try c.decodeIfPresent([Int].self, forKey: .pleSizes)
        pleOffsets = try c.decodeIfPresent([Int].self, forKey: .pleOffsets)
        pleMultipliers = try c.decodeIfPresent([UInt64].self, forKey: .pleMultipliers)
        pleRows = try c.decodeIfPresent(Int.self, forKey: .pleRows)
        func int(_ key: Key, _ fallback: Int) throws -> Int {
            try c.decodeIfPresent(Int.self, forKey: key) ?? fallback
        }
        hcCount = try int(.hcCount, 4)
        hcLowrank = try int(.hcLowrank, 320)
        indexHeads = try int(.indexHeads, 4)
        indexDim = try int(.indexDim, 128)
        indexBudget = try int(.indexBudget, 2048)
        compress = try int(.compress, 4)
        guard try int(.indexKV, 1) == 1 else {
            throw DecodingError.dataCorruptedError(
                forKey: .indexKV, in: c, debugDescription: "QSA requires one index KV head")
        }
        pleLayers = try c.decodeIfPresent([Int].self, forKey: .pleLayers) ?? []
        pleDim = try int(.pleDim, base.hiddenSize)
        pleKernel = try int(.pleKernel, 4)
        ngram = try int(.ngram, 3)
        headsPerNgram = try int(.headsPerNgram, 8)
        ngramVocab = try int(.ngramVocab, 20_000_000)
        divisor = try int(.divisor, 128)
        shards = try int(.shards, 128)
        seed = try int(.seed, 1234)
        eos =
            (try? c.decode(Int.self, forKey: .eos))
            ?? (try? c.decode([Int].self, forKey: .eos).first) ?? -1
        let gate = try c.decodeIfPresent(String.self, forKey: .gate) ?? "sigmoid"
        guard ["sigmoid", "silu"].contains(gate) else {
            throw DecodingError.dataCorruptedError(
                forKey: .gate, in: c, debugDescription: "Unsupported gate")
        }
        sigmoidGate = gate == "sigmoid"
        guard base.fullAttentionInterval > 0 else {
            throw DecodingError.dataCorruptedError(
                forKey: .layerTypes, in: c, debugDescription: "Invalid attention interval")
        }
        let layerCount = base.hiddenLayers
        let interval = base.fullAttentionInterval
        layerTypes =
            try c.decodeIfPresent([String].self, forKey: .layerTypes)
            ?? (0 ..< layerCount).map {
                ($0 + 1) % interval == 0 ? "qwen_sparse_attention" : "linear_attention"
            }
    }
    public func encode(to encoder: Encoder) throws {
        try base.encode(to: encoder)
        var c = encoder.container(keyedBy: Key.self)
        try c.encodeIfPresent(pleSizes, forKey: .pleSizes)
        try c.encodeIfPresent(pleOffsets, forKey: .pleOffsets)
        try c.encodeIfPresent(pleMultipliers, forKey: .pleMultipliers)
        try c.encodeIfPresent(pleRows, forKey: .pleRows)
        try c.encode(hcCount, forKey: .hcCount)
        try c.encode(hcLowrank, forKey: .hcLowrank)
        try c.encode(indexHeads, forKey: .indexHeads)
        try c.encode(indexDim, forKey: .indexDim)
        try c.encode(indexBudget, forKey: .indexBudget)
        try c.encode(compress, forKey: .compress)
        try c.encode(pleLayers, forKey: .pleLayers)
        try c.encode(pleDim, forKey: .pleDim)
        try c.encode(pleKernel, forKey: .pleKernel)
        try c.encode(ngram, forKey: .ngram)
        try c.encode(headsPerNgram, forKey: .headsPerNgram)
        try c.encode(ngramVocab, forKey: .ngramVocab)
        try c.encode(divisor, forKey: .divisor)
        try c.encode(shards, forKey: .shards)
        try c.encode(seed, forKey: .seed)
        try c.encode(eos, forKey: .eos)
        try c.encode(layerTypes, forKey: .layerTypes)
        try c.encode(sigmoidGate ? "sigmoid" : "silu", forKey: .gate)
    }
    public func validateModelConfiguration() throws {
        if pleSizes != nil || pleOffsets != nil || pleMultipliers != nil || pleRows != nil {
            guard let sizes = pleSizes, let offsets = pleOffsets, let multipliers = pleMultipliers,
                let rows = pleRows,
                sizes.count == (ngram - 1) * headsPerNgram, offsets.count == sizes.count,
                multipliers.count == ngram, rows > 0, rows <= Int(Int32.max),
                zip(sizes, offsets).allSatisfy({ $0 > 0 && $1 >= 0 && $1 <= rows - $0 }),
                multipliers.allSatisfy({
                    $0 <= UInt64(Int64.max) / UInt64(max(base.vocabularySize, 1))
                })
            else {
                throw NSError(
                    domain: "Qwen4Exp", code: 2,
                    userInfo: [NSLocalizedDescriptionKey: "PLE metadata 範圍不正確"])
            }
        }
        guard hcCount > 1, hcLowrank > 0, indexHeads > 0, indexDim > 0,
            compress > 0, indexBudget >= compress, indexBudget % compress == 0,
            base.hiddenSize > 0, base.hiddenLayers > 0, base.numExperts > 0,
            base.vocabularySize > 0, base.attentionHeads > 0, base.kvHeads > 0,
            base.attentionHeads % base.kvHeads == 0, (base.headDim ?? 0) > 0,
            base.numExpertsPerTok > 0, base.numExpertsPerTok <= base.numExperts,
            base.moeIntermediateSize > 0, base.sharedExpertIntermediateSize > 0,
            base.linearNumKeyHeads > 0, base.linearNumValueHeads > 0,
            base.linearNumValueHeads % base.linearNumKeyHeads == 0, base.linearKeyHeadDim > 0,
            base.linearValueHeadDim > 0, base.linearConvKernelDim > 1, seed >= 0,
            base.ropeTheta > 0, base.rmsNormEps > 0,
            Int(Float(base.headDim!) * base.partialRotaryFactor) >= 2,
            Int(Float(base.headDim!) * base.partialRotaryFactor) % 2 == 0,
            layerTypes.count == base.hiddenLayers,
            layerTypes.allSatisfy({
                ["linear_attention", "qwen_sparse_attention", "full_attention"].contains($0)
            }),
            ngram > 1, headsPerNgram > 0, pleDim > 0,
            pleDim % ((ngram - 1) * headsPerNgram) == 0, pleKernel > 1,
            ngramVocab > 1, divisor > 0, shards > 0,
            pleLayers.allSatisfy({
                $0 > 0 && $0 <= base.hiddenLayers && layerTypes[$0 - 1] == "linear_attention"
            }),
            pleLayers.isEmpty || (eos >= 0 && eos < base.vocabularySize),
            Int(Float(base.headDim!) * base.partialRotaryFactor) <= indexDim
        else {
            throw NSError(
                domain: "Qwen4Exp", code: 1,
                userInfo: [NSLocalizedDescriptionKey: "Qwen4-Exp 配置或 PLE／QSA 維度不相容"])
        }
    }
}

final class Qwen4ExpNorm: Module, UnaryLayer {
    var weight: MLXArray
    let group: Int?, eps: Float
    init(_ dim: Int, group: Int? = nil, eps: Float) {
        self.weight = .zeros([dim])
        self.group = group
        self.eps = eps
    }
    func callAsFunction(_ x: MLXArray) -> MLXArray {
        var y = x.asType(.float32)
        var w = weight.asType(.float32)
        if let group {
            y = y.reshaped(Array(x.shape.dropLast()) + [-1, group])
            w = w.reshaped(-1, group)
        }
        return (y * rsqrt((y * y).mean(axis: -1, keepDims: true) + eps) * (1 + w)).reshaped(x.shape)
            .asType(x.dtype)
    }
}

final class Qwen4ExpHyper: Module {
    @ModuleInfo(key: "hc_norm") var norm: Qwen4ExpNorm
    @ModuleInfo(key: "input_mix_weight_down") var down: Linear
    @ModuleInfo(key: "input_mix_weight_up") var up: Linear
    @ModuleInfo(key: "block_inject_weight") var inject: Linear?
    let count: Int, hidden: Int
    init(_ c: Qwen4ExpConfiguration, combine: Bool = true) {
        count = c.hcCount
        hidden = c.base.hiddenSize
        _norm.wrappedValue = Qwen4ExpNorm(count * hidden, group: hidden, eps: c.base.rmsNormEps)
        _down.wrappedValue = Linear(count * hidden, c.hcLowrank, bias: false)
        _up.wrappedValue = Linear(c.hcLowrank, count * hidden, bias: false)
        if combine { _inject.wrappedValue = Linear(count * hidden, count, bias: false) }
    }
    func callAsFunction(_ x: MLXArray) -> (MLXArray, MLXArray?) {
        let n = norm(x)
        let mixed = (sigmoid(up(silu(down(n) / Float(count)))) * n).reshaped(
            Array(x.shape.dropLast()) + [count, hidden]
        ).mean(axis: -2)
        return (mixed, inject.map { 2 * sigmoid($0(n) / Float(count)) })
    }
    func combine(_ x: MLXArray, _ branch: MLXArray, _ gate: MLXArray) -> MLXArray {
        x
            + (branch.expandedDimensions(axis: -2) * gate.expandedDimensions(axis: -1)).reshaped(
                x.shape)
    }
}

final class Qwen4ExpIndexer: Module {
    @ModuleInfo(key: "index_qk_proj") var projection: Linear
    @ModuleInfo(key: "q_layernorm") var qNorm: Qwen4ExpNorm
    @ModuleInfo(key: "k_layernorm") var kNorm: Qwen4ExpNorm
    let c: Qwen4ExpConfiguration
    init(_ c: Qwen4ExpConfiguration) {
        self.c = c
        _projection.wrappedValue = Linear(
            c.base.hiddenSize, (c.indexHeads + 1) * c.indexDim, bias: false)
        _qNorm.wrappedValue = Qwen4ExpNorm(c.indexDim, eps: c.base.rmsNormEps)
        _kNorm.wrappedValue = Qwen4ExpNorm(c.indexDim, eps: c.base.rmsNormEps)
    }
    func rotate(_ x: MLXArray, start: Int, step: Int = 1) -> MLXArray {
        let dims = Int(Float(c.base.headDim!) * c.base.partialRotaryFactor)
        let freq = exp(
            -log(MLXArray(c.base.ropeTheta)) * MLXArray(0 ..< (dims / 2)).asType(.float32)
                * (2 / Float(dims)))
        let positions = (MLXArray(0 ..< x.dim(2)) * step + start).asType(.float32)
        let angle = positions.expandedDimensions(axis: -1) * freq
        let a = x[.ellipsis, ..<(dims / 2)]
        let b = x[.ellipsis, (dims / 2) ..< dims]
        return concatenated(
            [
                a * cos(angle) - b * sin(angle), b * cos(angle) + a * sin(angle),
                x[.ellipsis, dims...],
            ], axis: -1
        ).asType(x.dtype)
    }
    func mask(_ x: MLXArray, cache: IndexedKVCache?) -> MLXArray {
        let batch = x.dim(0)
        let length = x.dim(1)
        let offset = cache?.offset ?? 0
        let projected = projection(x).reshaped(batch, length, c.indexHeads + 1, c.indexDim)
        let query = rotate(
            qNorm(projected[0..., 0..., ..<c.indexHeads, 0...]).transposed(0, 2, 1, 3),
            start: offset)
        let newKeys = projected[0..., 0..., c.indexHeads, 0...]
        let keys = cache?.appendIndexKeys(newKeys) ?? newKeys
        let total = keys.dim(1)
        let blocks = total / c.compress
        let top = c.indexBudget / c.compress
        let ends = MLXArray(offset ..< (offset + length)) + 1
        let tokens = MLXArray(0 ..< total)
        let causal = tokens.expandedDimensions(axis: 0) .< ends.expandedDimensions(axis: -1)
        guard blocks > top else { return causal.expandedDimensions(axes: [0, 1]) }
        let pooled = keys[0..., ..<(blocks * c.compress), 0...].reshaped(
            batch, blocks, c.compress, c.indexDim
        ).asType(.float32).mean(axis: 2).asType(keys.dtype)
        let k = rotate(kNorm(pooled).expandedDimensions(axis: 1), start: 0, step: c.compress)
        let scores =
            maximum(matmul(query.asType(.float32), k.asType(.float32).transposed(0, 1, 3, 2)), 0)
            .sum(axis: 1) / sqrt(Float(c.indexDim))
        let counts = ends / c.compress
        let valid =
            MLXArray(0 ..< blocks).expandedDimensions(axis: 0)
            .< counts.expandedDimensions(axis: -1)
        let selected = argPartition(
            which(valid, scores, -Float.infinity), kth: blocks - top, axis: -1)[
                .ellipsis, (blocks - top)...]
        let ids = (selected.expandedDimensions(axis: -1) * c.compress + MLXArray(0 ..< c.compress))
            .reshaped(batch, length, -1)
        var chosen = MLXArray.zeros([batch, length, total], dtype: .bool)
        chosen = putAlong(chosen, ids, values: MLXArray(true), axis: -1)
        let tail =
            tokens.expandedDimensions(axis: 0)
            .>= (counts * c.compress).expandedDimensions(axis: -1)
        return which(
            (counts .> top).expandedDimensions(axis: -1), (chosen .|| tail) .&& causal, causal
        ).expandedDimensions(axis: 1)
    }
}

final class Qwen4ExpAttention: Module {
    @ModuleInfo(key: "q_proj") var q: Linear
    @ModuleInfo(key: "k_proj") var k: Linear
    @ModuleInfo(key: "v_proj") var v: Linear
    @ModuleInfo(key: "o_proj") var o: Linear
    @ModuleInfo(key: "q_norm") var qNorm: Qwen4ExpNorm
    @ModuleInfo(key: "k_norm") var kNorm: Qwen4ExpNorm
    let indexer: Qwen4ExpIndexer
    let c: Qwen4ExpConfiguration
    init(_ c: Qwen4ExpConfiguration) {
        self.c = c
        let b = c.base
        let dim = b.headDim!
        _q.wrappedValue = Linear(b.hiddenSize, b.attentionHeads * dim * 2, bias: false)
        _k.wrappedValue = Linear(b.hiddenSize, b.kvHeads * dim, bias: false)
        _v.wrappedValue = Linear(b.hiddenSize, b.kvHeads * dim, bias: false)
        _o.wrappedValue = Linear(b.attentionHeads * dim, b.hiddenSize, bias: false)
        _qNorm.wrappedValue = Qwen4ExpNorm(dim, eps: b.rmsNormEps)
        _kNorm.wrappedValue = Qwen4ExpNorm(dim, eps: b.rmsNormEps)
        indexer = Qwen4ExpIndexer(c)
    }
    func callAsFunction(_ x: MLXArray, cache: IndexedKVCache?) -> MLXArray {
        let batch = x.dim(0)
        let length = x.dim(1)
        let b = c.base
        let offset = cache?.offset ?? 0
        let mask = indexer.mask(x, cache: cache)
        let parts = q(x).reshaped(batch, length, b.attentionHeads, -1).split(parts: 2, axis: -1)
        let queries = indexer.rotate(qNorm(parts[0]).transposed(0, 2, 1, 3), start: offset)
        let keys = indexer.rotate(
            kNorm(k(x).reshaped(batch, length, b.kvHeads, -1)).transposed(0, 2, 1, 3), start: offset
        )
        let values = v(x).reshaped(batch, length, b.kvHeads, -1).transposed(0, 2, 1, 3)
        let output = attentionWithCacheUpdate(
            queries: queries, keys: keys, values: values, cache: cache,
            scale: pow(Float(b.headDim!), -0.5), mask: .array(mask)
        ).transposed(0, 2, 1, 3).reshaped(batch, length, -1)
        return o(output * sigmoid(parts[1].reshaped(batch, length, -1)))
    }
}

final class Qwen4ExpShardedEmbedding: Module {
    var shards: [Embedding]
    let offsets: [Int], width: Int
    init(rows: Int, width: Int, count: Int) {
        self.width = width
        let sizes = (0 ..< count).map { rows / count + ($0 < rows % count ? 1 : 0) }
        var offsets = [0]
        for n in sizes { offsets.append(offsets.last! + n) }
        self.offsets = offsets
        shards = sizes.map { Embedding(embeddingCount: $0, dimensions: width) }
    }
    func callAsFunction(_ ids: [Int], shape: [Int]) -> MLXArray {
        var groups = [Int: [(Int, Int)]]()
        for (position, id) in ids.enumerated() {
            precondition(id >= 0 && id < offsets.last!)
            let shard = offsets.dropLast().lastIndex(where: { $0 <= id })!
            groups[shard, default: []].append((position, id - offsets[shard]))
        }
        var result: MLXArray?
        for shard in groups.keys.sorted() {
            let entries = groups[shard]!
            let values = shards[shard](MLXArray(entries.map { Int32($0.1) }))
            if result == nil { result = .zeros([ids.count, width], dtype: values.dtype) }
            result![MLXArray(entries.map { Int32($0.0) })] = values
        }
        return result!.reshaped(shape + [width])
    }
}
final class Qwen4ExpNGram: Module {
    @ModuleInfo(key: "ngram_embedding") var embedding: Qwen4ExpShardedEmbedding
    let c: Qwen4ExpConfiguration, sizes: [Int], offsets: [Int], multipliers: [UInt64]
    init(_ c: Qwen4ExpConfiguration, index: Int) {
        self.c = c
        let heads = (c.ngram - 1) * c.headsPerNgram
        func prime(_ n: Int) -> Bool {
            if n < 2 { return false }
            if n % 2 == 0 { return n == 2 }
            var d = 3
            while d <= n / d {
                if n % d == 0 { return false }
                d += 2
            }
            return true
        }
        var p = c.ngramVocab - 1
        var sizes = [Int]()
        var offsets = [Int]()
        var rows = 0
        for i in 0 ..< ((index + 1) * heads) {
            repeat { p += 1 } while !prime(p)
            if i >= index * heads {
                sizes.append(p)
                offsets.append(rows)
                rows += p
            }
        }
        if let explicit = c.pleSizes, let starts = c.pleOffsets {
            sizes = explicit
            offsets = starts
            rows = c.pleRows!
        }
        self.sizes = sizes
        self.offsets = offsets
        func mix(_ value: UInt64) -> UInt64 {
            var v = value &+ 0x9E37_79B9_7F4A_7C15
            v = (v ^ (v >> 30)) &* 0xBF58_476D_1CE4_E5B9
            v = (v ^ (v >> 27)) &* 0x94D0_49BB_1331_11EB
            return v ^ (v >> 31)
        }
        let bound = max(UInt64(Int64.max) / UInt64(c.base.vocabularySize) / 2, 1)
        multipliers =
            c.pleMultipliers
            ?? (0 ..< c.ngram).map {
                2
                    * (mix(
                        UInt64(c.seed) &+ UInt64(10007 * index)
                            &+ (0x9E37_79B9_7F4A_7C15 &* UInt64($0 + 1))) % bound) + 1
            }
        _embedding.wrappedValue = Qwen4ExpShardedEmbedding(
            rows: ((rows + c.divisor - 1) / c.divisor) * c.divisor, width: c.pleDim / heads,
            count: c.shards)
    }
    func callAsFunction(_ tokens: MLXArray, cache: RecurrentAuxiliaryCache?) -> MLXArray {
        let batch = tokens.dim(0)
        let length = tokens.dim(1)
        let context = c.ngram - 1
        let history =
            cache?.tokenHistory
            ?? MLXArray.full([batch, context], values: MLXArray(c.eos), dtype: .int32)
        let combined = concatenated([history, tokens], axis: 1)
        cache?.tokenHistory = combined[0..., (combined.dim(1) - context)...]
        let host = combined.asType(.int32).asArray(Int32.self)
        let stride = combined.dim(1)
        var ids = [Int]()
        ids.reserveCapacity(batch * length * sizes.count)
        for b in 0 ..< batch {
            for t in context ..< stride {
                var shifted = [UInt64]()
                var boundary = false
                for shift in 0 ..< c.ngram {
                    // EOS itself may refer to its preceding segment; the next token may not.
                    if shift > 0 && host[b * stride + t - shift] == Int32(c.eos) { boundary = true }
                    shifted.append(UInt64(boundary ? c.eos : Int(host[b * stride + t - shift])))
                }
                var hash = shifted[0] * multipliers[0]
                for n in 1 ..< c.ngram {
                    hash ^= shifted[n] * multipliers[n]
                    for h in 0 ..< c.headsPerNgram {
                        let i = (n - 1) * c.headsPerNgram + h
                        ids.append(Int(hash % UInt64(sizes[i])) + offsets[i])
                    }
                }
            }
        }
        return embedding(ids, shape: [batch, length, sizes.count]).reshaped(batch, length, c.pleDim)
    }
}
final class Qwen4ExpPLE: Module {
    @ModuleInfo(key: "ple_embedding") var embedding: Qwen4ExpNGram
    @ModuleInfo(key: "key_proj") var key: Linear
    @ModuleInfo(key: "value_proj") var value: Linear
    @ModuleInfo(key: "norm_key") var keyNorm: Qwen4ExpNorm
    @ModuleInfo(key: "norm_query") var queryNorm: Qwen4ExpNorm
    @ModuleInfo(key: "norm_conv") var convNorm: Qwen4ExpNorm
    let conv1d: Conv1d, c: Qwen4ExpConfiguration, historyLength: Int
    init(_ c: Qwen4ExpConfiguration, index: Int) {
        self.c = c
        let wide = c.hcCount * c.base.hiddenSize
        _embedding.wrappedValue = Qwen4ExpNGram(c, index: index)
        _key.wrappedValue = Linear(c.pleDim, wide, bias: false)
        _value.wrappedValue = Linear(c.pleDim, c.base.hiddenSize, bias: false)
        _keyNorm.wrappedValue = Qwen4ExpNorm(wide, group: c.base.hiddenSize, eps: c.base.rmsNormEps)
        _queryNorm.wrappedValue = Qwen4ExpNorm(
            wide, group: c.base.hiddenSize, eps: c.base.rmsNormEps)
        _convNorm.wrappedValue = Qwen4ExpNorm(
            wide, group: c.base.hiddenSize, eps: c.base.rmsNormEps)
        historyLength = (c.pleKernel - 1) * c.ngram
        conv1d = Conv1d(
            inputChannels: wide, outputChannels: wide, kernelSize: c.pleKernel, dilation: c.ngram,
            groups: wide, bias: false)
    }
    func callAsFunction(_ x: MLXArray, tokens: MLXArray, cache: RecurrentAuxiliaryCache?)
        -> MLXArray
    {
        let e = embedding(tokens, cache: cache)
        let shape = Array(x.shape.dropLast()) + [c.hcCount, c.base.hiddenSize]
        let k = keyNorm(key(e)).reshaped(shape)
        let q = queryNorm(x).reshaped(shape)
        let score = (k * q).sum(axis: -1, keepDims: true) / sqrt(Float(c.base.hiddenSize))
        let gate = sigmoid(sign(score) * sqrt(maximum(abs(score), 1e-6)))
        let gated = (gate * value(e).expandedDimensions(axis: -2)).reshaped(x.shape)
        let n = convNorm(gated)
        let previous =
            cache?.convolution
            ?? MLXArray.zeros([x.dim(0), historyLength, x.dim(-1)], dtype: x.dtype)
        let sequence = concatenated([previous, n], axis: 1)
        cache?.convolution = sequence[0..., (sequence.dim(1) - historyLength)..., 0...]
        return gated + silu(conv1d(sequence))
    }
}
final class Qwen4ExpLayer: Module {
    @ModuleInfo(key: "self_attn") var attention: Qwen4ExpAttention?
    @ModuleInfo(key: "linear_attn") var recurrent: Qwen35GatedDeltaNet?
    @ModuleInfo(key: "attn_hyper_connection") var attentionHyper: Qwen4ExpHyper
    @ModuleInfo(key: "mlp_hyper_connection") var mlpHyper: Qwen4ExpHyper
    let mlp: Qwen35SparseMoeBlock
    let ple: Qwen4ExpPLE?
    let isLinear: Bool
    init(_ c: Qwen4ExpConfiguration, index: Int, draft: Bool = false) {
        isLinear = !draft && c.layerTypes[index] == "linear_attention"
        if isLinear {
            _recurrent.wrappedValue = Qwen35GatedDeltaNet(
                c.base, l2NormalizeQK: true, sigmoidGate: c.sigmoidGate)
        } else {
            _attention.wrappedValue = Qwen4ExpAttention(c)
        }
        if !draft, let i = c.pleLayers.firstIndex(of: index + 1) {
            ple = Qwen4ExpPLE(c, index: i)
        } else {
            ple = nil
        }
        _attentionHyper.wrappedValue = Qwen4ExpHyper(c)
        _mlpHyper.wrappedValue = Qwen4ExpHyper(c)
        mlp = Qwen35SparseMoeBlock(c.base)
    }
    func callAsFunction(
        _ input: MLXArray, tokens: MLXArray, cache: KVCache?, checkpoint: Int? = nil
    ) -> MLXArray {
        if isLinear, let checkpoint, checkpoint > 0, checkpoint < input.dim(1),
            let cache = cache as? RecurrentAuxiliaryCache
        {
            let first = callAsFunction(
                input[0..., ..<checkpoint, 0...], tokens: tokens[0..., ..<checkpoint], cache: cache)
            cache.saveSpeculativeCheckpoint()
            let rest = callAsFunction(
                input[0..., checkpoint..., 0...], tokens: tokens[0..., checkpoint...], cache: cache)
            return concatenated([first, rest], axis: 1)
        }
        let auxiliary = cache as? RecurrentAuxiliaryCache
        let x = ple.map { input + $0(input, tokens: tokens, cache: auxiliary) } ?? input
        let (mixed, gate) = attentionHyper(x)
        let branch: MLXArray
        if let recurrent {
            branch = recurrent(mixed, mask: nil, cache: auxiliary?.recurrent)
            auxiliary?.advance(input.dim(1))
        } else {
            branch = attention!(mixed, cache: cache as? IndexedKVCache)
        }
        let h = attentionHyper.combine(x, branch, gate!)
        let (m, g) = mlpHyper(h)
        return mlpHyper.combine(h, mlp(m), g!)
    }
}
final class Qwen4ExpInner: Module {
    @ModuleInfo(key: "embed_tokens") var embedding: Embedding
    @ModuleInfo(key: "hyper_connection_mixer") var mixer: Qwen4ExpHyper
    let layers: [Qwen4ExpLayer], c: Qwen4ExpConfiguration
    init(_ c: Qwen4ExpConfiguration) {
        self.c = c
        _embedding.wrappedValue = Embedding(
            embeddingCount: c.base.vocabularySize, dimensions: c.base.hiddenSize)
        _mixer.wrappedValue = Qwen4ExpHyper(c, combine: false)
        layers = (0 ..< c.base.hiddenLayers).map { Qwen4ExpLayer(c, index: $0) }
    }
    func forward(_ tokens: MLXArray, cache: [KVCache]?, checkpoint: Int?) -> MLXArray {
        let e = embedding(tokens)
        var h = broadcast(
            e.expandedDimensions(axis: -2),
            to: Array(e.shape.dropLast()) + [c.hcCount, c.base.hiddenSize]
        ).reshaped(tokens.dim(0), tokens.dim(1), -1)
        for (i, layer) in layers.enumerated() {
            h = layer(h, tokens: tokens, cache: cache?[i], checkpoint: checkpoint)
        }
        return h
    }
}
public final class Qwen4ExpModel: Module, LLMModel, KVCacheDimensionProvider,
    SpeculativeCacheRewindModel
{
    let model: Qwen4ExpInner
    let configuration: Qwen4ExpConfiguration
    @ModuleInfo(key: "lm_head") var lmHead: Linear?
    public var loraLayers: [Module] { model.layers }
    public var vocabularySize: Int { configuration.base.vocabularySize }
    public var kvHeads: [Int] {
        Array(repeating: configuration.base.kvHeads, count: configuration.base.hiddenLayers)
    }
    public var maximumNativeTargetCacheRewind: Int { 1 }
    public init(_ c: Qwen4ExpConfiguration) {
        configuration = c
        model = Qwen4ExpInner(c)
        if !c.base.tieWordEmbeddings {
            _lmHead.wrappedValue = Linear(c.base.hiddenSize, c.base.vocabularySize, bias: false)
        }
    }
    func logits(_ hidden: MLXArray) -> MLXArray {
        lmHead?(hidden) ?? model.embedding.asLinear(hidden)
    }
    public func callAsFunction(_ inputs: MLXArray, cache: [KVCache]?) -> MLXArray {
        logits(model.mixer(model.forward(inputs, cache: cache, checkpoint: nil)).0)
    }
    public func callAsFunction(_ input: LMInput.Text, cache: [KVCache]?, state: LMOutput.State?)
        -> LMOutput
    {
        let wide = model.forward(
            input.tokens, cache: cache, checkpoint: state?[mtpCacheCheckpointIndexKey])
        let output = logits(model.mixer(wide).0)
        guard state?[mtpEmitFlagKey] ?? false else { return LMOutput(logits: output) }
        var state = state ?? LMOutput.State()
        state[mtpLastHiddenStatesKey] = wide
        return LMOutput(logits: output, state: state)
    }
    public func newCache(parameters: GenerateParameters?) -> [KVCache] {
        model.layers.map { $0.isLinear ? RecurrentAuxiliaryCache() : IndexedKVCache() }
    }
    public func sanitize(weights: [String: MLXArray]) -> [String: MLXArray] {
        var out = [String: MLXArray]()
        for (original, value) in weights {
            if original.contains("visual.") || original.contains("mtp.") { continue }
            var key = original
            for prefix in ["model.language_model.", "language_model."] where key.hasPrefix(prefix) {
                key = String(key.dropFirst(prefix.count))
                if !key.hasPrefix("model.") && !key.hasPrefix("lm_head.") { key = "model." + key }
                break
            }
            var v = value
            if key.hasSuffix("conv1d.weight"), value.ndim == 3, value.dim(-1) != 1 {
                v = value.transposed(0, 2, 1)
            }
            out[key] = v
        }
        for key in Array(out.keys) where key.contains(".experts.gate_up_proj") {
            let parts = out.removeValue(forKey: key)!.split(parts: 2, axis: -2)
            let suffix = key.hasSuffix("gate_up_proj") ? ".weight" : ""
            out[
                key.replacingOccurrences(of: "experts.gate_up_proj", with: "switch_mlp.gate_proj")
                    + suffix] = parts[0]
            out[
                key.replacingOccurrences(of: "experts.gate_up_proj", with: "switch_mlp.up_proj")
                    + suffix] = parts[1]
        }
        for key in Array(out.keys) where key.contains(".experts.down_proj") {
            out[
                key.replacingOccurrences(of: "experts.down_proj", with: "switch_mlp.down_proj")
                    + (key.hasSuffix("down_proj") ? ".weight" : "")] = out.removeValue(forKey: key)
        }
        if configuration.base.tieWordEmbeddings { out.removeValue(forKey: "lm_head.weight") }
        return out
    }
}
