import Foundation
import MLX

#if canImport(Darwin)
import Darwin
#elseif canImport(Glibc)
import Glibc
#endif

/// 模型權重的載入策略。預設維持 MLX 原有的 eager 行為；memoryMapped
/// 只由上層 Runtime 明確啟用，避免改變既有呼叫端的記憶體與效能特性。
public enum ModelWeightLoadingMode: Sendable {
    case eager
    case memoryMapped
}

/// 以 TaskLocal 傳遞載入策略，讓 LLM、VLM 與 Draft 共用同一條既有
/// ModelFactory 流程，不必為每種模型複製 Factory 實作。
public enum ModelWeightLoadingContext {
    @TaskLocal public static var mode: ModelWeightLoadingMode = .eager
    @TaskLocal public static var progressHandler: (@Sendable (Int64, Int64) -> Void)? = nil
}

public enum MemoryMappedTensorError: LocalizedError, Sendable {
    case unsupportedPlatform
    case invalidRange
    case rangeTooLarge
    case openFailed(String)
    case statFailed(String)
    case mapFailed(String)
    case invalidSafetensors(String)
    case unsupportedDType(String)

    public var errorDescription: String? {
        switch self {
        case .unsupportedPlatform:
            "目前平台不支援檔案映射。"
        case .invalidRange:
            "權重檔案的映射範圍無效。"
        case .rangeTooLarge:
            "單一權重區段超出 MLX 可映射的大小。"
        case .openFailed(let name):
            "無法開啟權重檔案：\(name)"
        case .statFailed(let name):
            "無法讀取權重檔案大小：\(name)"
        case .mapFailed(let name):
            "無法映射權重檔案：\(name)"
        case .invalidSafetensors(let name):
            "safetensors 權重格式無效：\(name)"
        case .unsupportedDType(let dtype):
            "MMap 尚未支援 safetensors 權重型別：\(dtype)"
        }
    }
}

/// 追蹤所有仍存活的權重映射，用來量測 MMap 權重真正佔用的實體記憶體。
///
/// 映射為唯讀，權重頁面永遠是乾淨的。這些頁面不會計入行程的 phys_footprint，
/// 也幾乎不會出現在行程的常駐統計裡（`vmmap` 顯示 mapped file resident ≈ 0），
/// 實際上是放在系統的統一緩衝快取，由核心在記憶體壓力下自行回收。
///
/// 實測 `madvise(MADV_DONTNEED)`、`madvise(MADV_FREE_REUSABLE)`、
/// `msync(MS_INVALIDATE)` 與 `MAP_FIXED` 重新映射都無法把這些快取頁面逐出，
/// 因此使用者空間沒有強制回收的手段；能做的是準確量測並在超出預算時回報。
public final class MemoryMappedRegionRegistry: @unchecked Sendable {
    public static let shared = MemoryMappedRegionRegistry()

    private let lock = NSLock()
    private var regions: [UInt: Int] = [:]

    private init() {}

    func register(base: UnsafeMutableRawPointer, length: Int) {
        lock.lock()
        defer { lock.unlock() }
        regions[UInt(bitPattern: base)] = length
    }

    func unregister(base: UnsafeMutableRawPointer) {
        lock.lock()
        defer { lock.unlock() }
        regions.removeValue(forKey: UInt(bitPattern: base))
    }

    /// 目前映射的權重總位元組數（虛擬位址空間）。
    public func mappedBytes() -> Int {
        lock.lock()
        defer { lock.unlock() }
        return regions.values.reduce(0, +)
    }

    private func snapshot() -> [(base: UInt, length: Int)] {
        lock.lock()
        defer { lock.unlock() }
        return regions.map { (base: $0.key, length: $0.value) }
    }

    /// 以 `mincore` 統計映射區段目前實際佔用的實體記憶體。
    ///
    /// `mincore` 回報的是檔案 VM 物件的快取狀態，也就是這些權重確實佔住的
    /// 實體頁面；行程層級的 footprint 看不到它們，必須另外加總才是真實用量。
    public func residentBytes() -> Int {
        #if canImport(Darwin)
        let pageSize = Int(sysconf(Int32(_SC_PAGESIZE)))
        guard pageSize > 0 else { return 0 }
        var total = 0
        var vector = [CChar]()
        for region in snapshot() {
            let pages = (region.length + pageSize - 1) / pageSize
            if vector.count < pages {
                vector = [CChar](repeating: 0, count: pages)
            }
            guard let pointer = UnsafeMutableRawPointer(bitPattern: region.base) else { continue }
            let status = vector.withUnsafeMutableBufferPointer { buffer -> Int32 in
                guard let base = buffer.baseAddress else { return -1 }
                return mincore(pointer, region.length, base)
            }
            guard status == 0 else { continue }
            var resident = 0
            for index in 0 ..< pages where vector[index] & 0x1 != 0 {
                resident += pageSize
            }
            total += min(resident, region.length)
        }
        return total
        #else
        return 0
        #endif
    }

}

/// 將檔案中的單一 tensor 區段包裝成 MLX 共用緩衝區。
///
/// mmap 起點依 VM page 對齊，MLXArray 再以 slice/view 指向真正的 tensor。
/// Metal 直接使用檔案頁面，不先複製整份權重；macOS 因此可在記憶體壓力
/// 下回收乾淨頁面，後續需要時再由檔案載入。
public enum MemoryMappedTensorArray {
    /// Metal 綁定緩衝區時使用「基底 MTLBuffer + 位元組位移」，位移必須同時滿足
    /// 元素型別對齊與向量化載入的 16 位元組對齊；safetensors 的資料區起點是任意
    /// 位元組位置，無法靠 mmap 事後修正，因此不符合的 tensor 一律改走複製路徑。
    static let requiredAlignment = 16

    /// 判斷該 tensor 能否零拷貝映射。
    public static func canMap(fileOffset: Int, dtype: DType) -> Bool {
        guard fileOffset >= 0 else { return false }
        let alignment = max(requiredAlignment, dtype.size)
        return fileOffset % alignment == 0
    }

    public static func load(
        from url: URL,
        fileOffset: Int,
        byteCount: Int,
        shape: [Int],
        dtype: DType
    ) throws -> MLXArray {
        guard fileOffset >= 0, byteCount >= 0 else {
            throw MemoryMappedTensorError.invalidRange
        }
        guard expectedByteCount(shape: shape, dtype: dtype) == byteCount else {
            throw MemoryMappedTensorError.invalidRange
        }
        if byteCount == 0 {
            return MLXArray.zeros(shape, dtype: dtype)
        }

        #if canImport(Darwin) || canImport(Glibc)
        let descriptor = url.path.withCString { open($0, O_RDONLY) }
        guard descriptor >= 0 else {
            throw MemoryMappedTensorError.openFailed(url.lastPathComponent)
        }
        defer { _ = close(descriptor) }

        var status = stat()
        guard fstat(descriptor, &status) == 0 else {
            throw MemoryMappedTensorError.statFailed(url.lastPathComponent)
        }
        let fileSize = Int64(status.st_size)
        let end = Int64(fileOffset) + Int64(byteCount)
        guard end >= 0, end <= fileSize else {
            throw MemoryMappedTensorError.invalidRange
        }

        let pageSize = Int(sysconf(Int32(_SC_PAGESIZE)))
        guard pageSize > 0 else {
            throw MemoryMappedTensorError.unsupportedPlatform
        }
        guard canMap(fileOffset: fileOffset, dtype: dtype) else {
            // 資料區未對齊時映射出來的指標會讓 Metal 讀到錯誤位址，
            // 產生看似成功但完全錯誤的權重，因此改為對齊複製。
            return try copy(
                descriptor: descriptor,
                name: url.lastPathComponent,
                fileOffset: fileOffset,
                byteCount: byteCount,
                shape: shape,
                dtype: dtype
            )
        }

        let alignedOffset = fileOffset - (fileOffset % pageSize)
        let prefix = fileOffset - alignedOffset
        let requiredLength = prefix + byteCount
        let (roundedInput, roundedOverflow) = requiredLength.addingReportingOverflow(pageSize - 1)
        guard !roundedOverflow else {
            throw MemoryMappedTensorError.rangeTooLarge
        }
        let mappedLength = (roundedInput / pageSize) * pageSize
        // MLXArray 的 shape 目前使用 Int32 維度；每個 tensor 分開映射可避免
        // 大型模型的整個檔案超過單一維度上限。
        guard mappedLength > 0, mappedLength <= Int(Int32.max) else {
            throw MemoryMappedTensorError.rangeTooLarge
        }

        let mapped = mmap(
            nil,
            mappedLength,
            PROT_READ,
            MAP_PRIVATE,
            descriptor,
            off_t(alignedOffset)
        )
        guard let mapped, mapped != UnsafeMutableRawPointer(bitPattern: -1) else {
            throw MemoryMappedTensorError.mapFailed(url.lastPathComponent)
        }

        MemoryMappedRegionRegistry.shared.register(base: mapped, length: mappedLength)
        let mappedBytes = MLXArray(
            rawPointer: mapped,
            [mappedLength],
            dtype: .uint8
        ) {
            MemoryMappedRegionRegistry.shared.unregister(base: mapped)
            _ = munmap(mapped, mappedLength)
        }
        let tensorBytes = mappedBytes[prefix..<(prefix + byteCount)]
        return tensorBytes.view(dtype: dtype).reshaped(shape)
        #else
        throw MemoryMappedTensorError.unsupportedPlatform
        #endif
    }

    /// 以 MLX 自行配置的對齊緩衝區讀入 tensor，語意等同 eager 載入。
    private static func copy(
        descriptor: Int32,
        name: String,
        fileOffset: Int,
        byteCount: Int,
        shape: [Int],
        dtype: DType
    ) throws -> MLXArray {
        var bytes = [UInt8](repeating: 0, count: byteCount)
        let read = bytes.withUnsafeMutableBytes { buffer -> Int in
            guard let base = buffer.baseAddress else { return -1 }
            var total = 0
            while total < byteCount {
                let count = pread(
                    descriptor,
                    base.advanced(by: total),
                    byteCount - total,
                    off_t(fileOffset + total)
                )
                if count <= 0 { return -1 }
                total += count
            }
            return total
        }
        guard read == byteCount else {
            throw MemoryMappedTensorError.openFailed(name)
        }
        return MLXArray(bytes).view(dtype: dtype).reshaped(shape)
    }

    private static func expectedByteCount(shape: [Int], dtype: DType) -> Int? {
        var count = 1
        for dimension in shape {
            guard dimension >= 0 else { return nil }
            let (next, overflow) = count.multipliedReportingOverflow(by: dimension)
            guard !overflow else { return nil }
            count = next
        }
        let (bytes, overflow) = count.multipliedReportingOverflow(by: dtype.size)
        return overflow ? nil : bytes
    }
}

public enum MemoryMappedSafetensors {
    public static func loadArraysAndMetadata(from url: URL) throws -> (
        [String: MLXArray], [String: String]
    ) {
        let name = url.lastPathComponent
        let handle: FileHandle
        do {
            handle = try FileHandle(forReadingFrom: url)
        } catch {
            throw MemoryMappedTensorError.openFailed(name)
        }
        defer { try? handle.close() }

        guard let lengthData = try handle.read(upToCount: 8), lengthData.count == 8 else {
            throw MemoryMappedTensorError.invalidSafetensors(name)
        }
        let headerLength = lengthData.enumerated().reduce(UInt64(0)) { partial, item in
            partial | (UInt64(item.element) << UInt64(item.offset * 8))
        }
        guard headerLength > 0,
              headerLength <= 100_000_000,
              headerLength <= UInt64(Int.max),
              let headerData = try handle.read(upToCount: Int(headerLength)),
              headerData.count == Int(headerLength),
              let root = try JSONSerialization.jsonObject(with: headerData) as? [String: Any]
        else {
            throw MemoryMappedTensorError.invalidSafetensors(name)
        }

        let dataStart64 = UInt64(8) + headerLength
        guard dataStart64 <= UInt64(Int.max) else {
            throw MemoryMappedTensorError.invalidSafetensors(name)
        }
        let dataStart = Int(dataStart64)
        var mappedCount = 0
        var copiedCount = 0
        var copiedBytes = 0
        var arrays: [String: MLXArray] = [:]
        arrays.reserveCapacity(max(0, root.count - 1))

        for key in root.keys.sorted() where key != "__metadata__" {
            guard let descriptor = root[key] as? [String: Any],
                  let dtypeName = descriptor["dtype"] as? String,
                  let shapeValues = descriptor["shape"] as? [NSNumber],
                  let offsets = descriptor["data_offsets"] as? [NSNumber],
                  offsets.count == 2
            else {
                throw MemoryMappedTensorError.invalidSafetensors(name)
            }
            let shape = shapeValues.map(\.intValue)
            let start = offsets[0].intValue
            let end = offsets[1].intValue
            guard start >= 0, end >= start else {
                throw MemoryMappedTensorError.invalidSafetensors(name)
            }
            let (fileOffset, offsetOverflow) = dataStart.addingReportingOverflow(start)
            guard !offsetOverflow else {
                throw MemoryMappedTensorError.invalidSafetensors(name)
            }
            let dtype = try mlxDType(dtypeName)
            if MemoryMappedTensorArray.canMap(fileOffset: fileOffset, dtype: dtype) {
                mappedCount += 1
            } else {
                copiedCount += 1
                copiedBytes += end - start
            }
            arrays[key] = try MemoryMappedTensorArray.load(
                from: url,
                fileOffset: fileOffset,
                byteCount: end - start,
                shape: shape,
                dtype: dtype
            )
        }

        fputs(
            "MLX MMap file=\(name) mapped=\(mappedCount) copied=\(copiedCount) copied_mib=\(String(format: "%.1f", Double(copiedBytes) / (1_024 * 1_024)))\n",
            stderr
        )

        let metadata = (root["__metadata__"] as? [String: Any])?.reduce(
            into: [String: String]()
        ) { result, item in
            if let value = item.value as? String {
                result[item.key] = value
            }
        } ?? [:]
        return (arrays, metadata)
    }

    private static func mlxDType(_ value: String) throws -> DType {
        switch value.uppercased() {
        case "BOOL": .bool
        case "U8": .uint8
        case "U16": .uint16
        case "U32": .uint32
        case "U64": .uint64
        case "I8": .int8
        case "I16": .int16
        case "I32": .int32
        case "I64": .int64
        case "F16": .float16
        case "BF16": .bfloat16
        case "F32": .float32
        case "F64": .float64
        case "C64": .complex64
        default: throw MemoryMappedTensorError.unsupportedDType(value)
        }
    }
}
