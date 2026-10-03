import MLX

/// Worker 的加總輸入始終為零；以有限容量重用，避免每層重建 Metal 張量。
/// 僅由 worker loop 存取；保留 MLXArray 參考也避免 collective 捐贈其 backing。
final class DistributedZeroBuffers {
    private struct Key: Hashable {
        let shape: [Int]
        let dtype: DType
    }
    private struct Entry {
        let array: MLXArray
        var accessed: UInt64
    }
    private let byteLimit: Int
    private let entryLimit: Int
    private var entries: [Key: Entry] = [:]
    private var clock: UInt64 = 0
    private(set) var retainedBytes = 0

    init(byteLimit: Int = 16 * 1024 * 1024, entryLimit: Int = 32) {
        self.byteLimit = max(0, byteLimit)
        self.entryLimit = max(0, entryLimit)
    }

    func zeros(shape: [Int], dtype: DType) -> MLXArray {
        clock &+= 1
        let key = Key(shape: shape, dtype: dtype)
        if var entry = entries[key] {
            entry.accessed = clock
            entries[key] = entry
            return entry.array
        }
        let array = MLXArray.zeros(shape, dtype: dtype)
        guard array.nbytes > 0, array.nbytes <= byteLimit, entryLimit > 0 else { return array }
        while retainedBytes + array.nbytes > byteLimit || entries.count >= entryLimit {
            guard let oldest = entries.min(by: { $0.value.accessed < $1.value.accessed }) else { break }
            retainedBytes -= oldest.value.array.nbytes
            entries.removeValue(forKey: oldest.key)
        }
        entries[key] = Entry(array: array, accessed: clock)
        retainedBytes += array.nbytes
        return array
    }
}
