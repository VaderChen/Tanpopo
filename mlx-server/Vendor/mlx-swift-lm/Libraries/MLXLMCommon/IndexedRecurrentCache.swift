// Copyright © 2026 Tanpopo contributors.
import MLX

/// 注意力與索引器同步前進、回滾，避免只 trim K/V 留下已拒絕位置的索引。
public final class IndexedKVCache: KVCacheSimple {
    public var indexKeys: MLXArray?

    public func appendIndexKeys(_ keys: MLXArray) -> MLXArray {
        indexKeys = indexKeys.map { concatenated([$0, keys], axis: 1) } ?? keys
        return indexKeys!
    }

    public override func innerState() -> [MLXArray] {
        super.innerState() + [indexKeys].compactMap { $0 }
    }

    public override var state: [MLXArray] {
        get { super.state + [indexKeys].compactMap { $0 } }
        set {
            precondition(newValue.isEmpty || newValue.count == 2 || newValue.count == 3)
            if newValue.isEmpty {
                keys = nil
                values = nil
                indexKeys = nil
                offset = 0
                return
            }
            super.state = Array(newValue.prefix(2))
            indexKeys = newValue.count == 3 ? newValue[2] : nil
        }
    }

    public override func trim(_ n: Int) -> Int {
        let count = super.trim(n)
        if let keys = indexKeys { indexKeys = keys[0..., ..<offset, 0...] }
        return count
    }

    public override func copy() -> any KVCache {
        let result = IndexedKVCache()
        if !super.state.isEmpty { result.state = state.map { $0[.ellipsis] } }
        result.offset = offset
        return result
    }
}

/// Recurrent 層與額外卷積／token history 的原子 checkpoint。
/// 狀態屬於單一請求；模型本身不持有可變 cache。
public final class RecurrentAuxiliaryCache: BaseKVCache, SpeculativeCheckpointCache {
    public override init() { super.init() }
    public var recurrent = MambaCache()
    public var convolution: MLXArray?
    public var tokenHistory: MLXArray?
    private var checkpoint: (MambaCache, MLXArray?, MLXArray?, Int)?

    public func advance(_ count: Int) { offset += count }
    public func saveSpeculativeCheckpoint() {
        checkpoint = (recurrent.copy() as! MambaCache, convolution, tokenHistory, offset)
    }
    public var hasSpeculativeCheckpoint: Bool { checkpoint != nil }
    public func restoreSpeculativeCheckpoint() -> Bool {
        guard let saved = checkpoint else { return false }
        (recurrent, convolution, tokenHistory, offset) = saved
        checkpoint = nil
        return true
    }
    public func discardSpeculativeCheckpoint() { checkpoint = nil }

    public override func innerState() -> [MLXArray] {
        var arrays = recurrent.innerState() + [convolution, tokenHistory].compactMap { $0 }
        if let saved = checkpoint {
            arrays += saved.0.innerState() + [saved.1, saved.2].compactMap { $0 }
        }
        return arrays
    }
    public override var state: [MLXArray] {
        get { recurrent.state + [convolution, tokenHistory].compactMap { $0 } }
        set {
            precondition(newValue.isEmpty || newValue.count == 2 || newValue.count == 4)
            recurrent.state = Array(newValue.prefix(2))
            convolution = newValue.count == 4 ? newValue[2] : nil
            tokenHistory = newValue.count == 4 ? newValue[3] : nil
            checkpoint = nil
        }
    }
    public override var metaState: [String] {
        get { [String(offset)] + recurrent.metaState }
        set {
            precondition(!newValue.isEmpty)
            offset = Int(newValue[0]) ?? 0
            recurrent.restoreFromMetaState(
                state: recurrent.state, savedMetaState: Array(newValue.dropFirst()))
            recurrent.offset = offset
        }
    }
    public override func copy() -> any KVCache {
        let result = RecurrentAuxiliaryCache()
        result.recurrent = recurrent.copy() as! MambaCache
        result.convolution = convolution.map { $0[.ellipsis] }
        result.tokenHistory = tokenHistory.map { $0[.ellipsis] }
        result.offset = offset
        return result
    }
}
