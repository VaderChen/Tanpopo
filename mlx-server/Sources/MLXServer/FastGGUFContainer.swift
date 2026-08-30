import Compression
import Foundation
import MLX
import MLXLMCommon

/// Tanpopo 專用的轉換權重容器。
///
/// `.fgguf` 不是標準 GGUF 的替代品，而是 GGUF 經 MLX 轉換後的永久快取格式。
/// 每個 tensor 個別決定是否使用 LZFSE 無損壓縮；未壓縮的 tensor 會維持
/// 16-byte 對齊，因此仍能走既有的 MMap 零拷貝路徑。
enum FastGGUFContainer {
    private static let magic = Data("FGGUF001".utf8)
    private static let formatVersion = 1
    private static let headerReserveBytes = 1_048_576
    private static let tensorAlignment: Int64 = 16
    private static let minimumCompressionBytes = 4_096
    private static let maximumCompressionBytes = 256 * 1_024 * 1_024
    private static let sampleSegmentBytes = 128 * 1_024
    private static let sampleCompressionRatio = 0.94
    private static let finalCompressionRatio = 0.92

    struct Statistics: Sendable {
        let rawBytes: Int64
        let storedBytes: Int64
        let compressedTensorCount: Int
        let rawTensorCount: Int
    }

    private struct Header: Codable, Sendable {
        let formatVersion: Int
        let cacheKey: String
        let payloadOffset: Int64
        let metadata: [String: String]
        let tensors: [TensorDescriptor]
    }

    private struct TensorDescriptor: Codable, Sendable {
        let name: String
        let dtype: String
        let shape: [Int]
        let encoding: String
        let offset: Int64
        let storedBytes: Int64
        let rawBytes: Int64
    }

    enum ContainerError: LocalizedError {
        case createFailed(String)
        case invalidHeader(String)
        case headerTooLarge
        case invalidTensor(String)
        case compressionFailed(String)

        var errorDescription: String? {
            switch self {
            case .createFailed(let name):
                "無法建立 FGGUF 快取：\(name)。"
            case .invalidHeader(let name):
                "FGGUF 快取索引無效：\(name)。"
            case .headerTooLarge:
                "FGGUF 快取索引超出保留空間。"
            case .invalidTensor(let name):
                "FGGUF 快取 tensor 無效：\(name)。"
            case .compressionFailed(let name):
                "FGGUF 快取解壓失敗：\(name)。"
            }
        }
    }

    static func store(
        arrays: [String: MLXArray],
        metadata: [String: String],
        cacheKey: String,
        url: URL,
        progress: ((Int64) -> Void)? = nil
    ) throws -> Statistics {
        let fileManager = FileManager.default
        guard fileManager.createFile(atPath: url.path, contents: nil) else {
            throw ContainerError.createFailed(url.lastPathComponent)
        }
        let handle = try FileHandle(forWritingTo: url)
        defer { try? handle.close() }

        try handle.write(contentsOf: Data(count: headerReserveBytes))
        var descriptors = [TensorDescriptor]()
        descriptors.reserveCapacity(arrays.count)
        var payloadPosition: Int64 = 0
        var completedRawBytes: Int64 = 0
        var compressedTensorCount = 0
        var rawTensorCount = 0

        for name in arrays.keys.sorted() {
            guard let value = arrays[name] else { continue }
            value.eval()
            let arrayData = value.asData(access: .noCopyIfContiguous)
            guard arrayData.data.count == value.nbytes else {
                throw ContainerError.invalidTensor(name)
            }

            let alignedPosition = aligned(payloadPosition, to: tensorAlignment)
            if alignedPosition > payloadPosition {
                try handle.write(
                    contentsOf: Data(count: Int(alignedPosition - payloadPosition))
                )
                payloadPosition = alignedPosition
            }

            let encoded = encodeIfWorthwhile(arrayData.data)
            let encoding: String
            let payload: Data
            if let encoded {
                encoding = "lzfse"
                payload = encoded
                compressedTensorCount += 1
            } else {
                encoding = "raw"
                payload = arrayData.data
                rawTensorCount += 1
            }
            try handle.write(contentsOf: payload)

            descriptors.append(
                TensorDescriptor(
                    name: name,
                    dtype: dtypeName(value.dtype),
                    shape: value.shape,
                    encoding: encoding,
                    offset: payloadPosition,
                    storedBytes: Int64(payload.count),
                    rawBytes: Int64(arrayData.data.count)
                )
            )
            payloadPosition += Int64(payload.count)
            completedRawBytes += Int64(arrayData.data.count)
            progress?(completedRawBytes)
        }

        let header = Header(
            formatVersion: formatVersion,
            cacheKey: cacheKey,
            payloadOffset: Int64(headerReserveBytes),
            metadata: metadata,
            tensors: descriptors
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        let headerData = try encoder.encode(header)
        guard magic.count + MemoryLayout<UInt64>.size + headerData.count <= headerReserveBytes else {
            throw ContainerError.headerTooLarge
        }
        try handle.seek(toOffset: 0)
        try handle.write(contentsOf: magic)
        try handle.write(contentsOf: littleEndianData(UInt64(headerData.count)))
        try handle.write(contentsOf: headerData)
        try handle.synchronize()

        let storedBytes = (try fileManager.attributesOfItem(atPath: url.path)[.size]
            as? NSNumber)?.int64Value ?? Int64(headerReserveBytes) + payloadPosition
        return Statistics(
            rawBytes: completedRawBytes,
            storedBytes: storedBytes,
            compressedTensorCount: compressedTensorCount,
            rawTensorCount: rawTensorCount
        )
    }

    static func load(
        from url: URL,
        expectedCacheKey: String,
        memoryMapped: Bool
    ) throws -> ([String: MLXArray], Statistics) {
        let fileSize = ((try FileManager.default.attributesOfItem(atPath: url.path)[.size]
            as? NSNumber)?.int64Value) ?? 0
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }

        let prefix = try readExact(handle: handle, count: magic.count + MemoryLayout<UInt64>.size)
        guard prefix.prefix(magic.count) == magic else {
            throw ContainerError.invalidHeader(url.lastPathComponent)
        }
        let headerLength = decodeLittleEndianUInt64(prefix.dropFirst(magic.count))
        guard headerLength > 0,
              headerLength <= UInt64(headerReserveBytes - magic.count - MemoryLayout<UInt64>.size),
              headerLength <= UInt64(Int.max) else {
            throw ContainerError.invalidHeader(url.lastPathComponent)
        }
        let headerData = try readExact(handle: handle, count: Int(headerLength))
        let header = try JSONDecoder().decode(Header.self, from: headerData)
        guard header.formatVersion == formatVersion,
              header.cacheKey == expectedCacheKey,
              header.payloadOffset == Int64(headerReserveBytes),
              header.payloadOffset <= fileSize else {
            throw ContainerError.invalidHeader(url.lastPathComponent)
        }
        try validate(header: header, fileSize: fileSize)

        var arrays = [String: MLXArray]()
        arrays.reserveCapacity(header.tensors.count)
        var rawBytes: Int64 = 0
        var compressedTensorCount = 0
        var rawTensorCount = 0
        var mappedTensorCount = 0

        for tensor in header.tensors {
            guard arrays[tensor.name] == nil else {
                throw ContainerError.invalidTensor(tensor.name)
            }
            let dtype = try mlxDType(tensor.dtype)
            let absoluteOffset = header.payloadOffset + tensor.offset
            let value: MLXArray
            switch tensor.encoding {
            case "raw":
                rawTensorCount += 1
                if tensor.rawBytes == 0 {
                    value = MLXArray.zeros(tensor.shape, dtype: dtype)
                } else if memoryMapped {
                    value = try MemoryMappedTensorArray.load(
                        from: url,
                        fileOffset: Int(absoluteOffset),
                        byteCount: Int(tensor.rawBytes),
                        shape: tensor.shape,
                        dtype: dtype
                    )
                    mappedTensorCount += 1
                } else {
                    try handle.seek(toOffset: UInt64(absoluteOffset))
                    let data = try readExact(handle: handle, count: Int(tensor.rawBytes))
                    value = MLXArray(data, tensor.shape, dtype: dtype)
                }
            case "lzfse":
                compressedTensorCount += 1
                try handle.seek(toOffset: UInt64(absoluteOffset))
                let encoded = try readExact(handle: handle, count: Int(tensor.storedBytes))
                let decoded = try decodeLZFSE(
                    encoded,
                    expectedBytes: Int(tensor.rawBytes),
                    name: tensor.name
                )
                value = MLXArray(decoded, tensor.shape, dtype: dtype)
            default:
                throw ContainerError.invalidTensor(tensor.name)
            }
            arrays[tensor.name] = value
            rawBytes += tensor.rawBytes
        }

        let storedMiB = String(format: "%.1f", Double(fileSize) / (1_024 * 1_024))
        fputs(
            "FGGUF file=\(url.lastPathComponent) mmap=\(mappedTensorCount) raw=\(rawTensorCount) compressed=\(compressedTensorCount) stored_mib=\(storedMiB)\n",
            stderr
        )
        return (
            arrays,
            Statistics(
                rawBytes: rawBytes,
                storedBytes: fileSize,
                compressedTensorCount: compressedTensorCount,
                rawTensorCount: rawTensorCount
            )
        )
    }

    private static func validate(header: Header, fileSize: Int64) throws {
        var previousEnd = header.payloadOffset
        for tensor in header.tensors.sorted(by: { $0.offset < $1.offset }) {
            guard !tensor.name.isEmpty,
                  tensor.offset >= 0,
                  tensor.storedBytes >= 0,
                  tensor.rawBytes >= 0,
                  tensor.rawBytes <= Int64(Int.max),
                  tensor.storedBytes <= Int64(Int.max),
                  let dtype = try? mlxDType(tensor.dtype),
                  expectedByteCount(shape: tensor.shape, dtype: dtype) == tensor.rawBytes,
                  tensor.encoding == "raw" || tensor.encoding == "lzfse",
                  tensor.encoding != "raw" || tensor.storedBytes == tensor.rawBytes else {
                throw ContainerError.invalidTensor(tensor.name)
            }
            let start = header.payloadOffset + tensor.offset
            let end = start + tensor.storedBytes
            guard start >= previousEnd, end >= start, end <= fileSize else {
                throw ContainerError.invalidTensor(tensor.name)
            }
            if tensor.encoding == "raw",
               !MemoryMappedTensorArray.canMap(fileOffset: Int(start), dtype: dtype) {
                throw ContainerError.invalidTensor(tensor.name)
            }
            previousEnd = end
        }
    }

    private static func encodeIfWorthwhile(_ data: Data) -> Data? {
        guard data.count >= minimumCompressionBytes,
              data.count <= maximumCompressionBytes else {
            return nil
        }
        let sample = compressionSample(data)
        guard let encodedSample = encodeLZFSE(sample),
              Double(encodedSample.count) / Double(sample.count) <= sampleCompressionRatio,
              let encoded = encodeLZFSE(data),
              Double(encoded.count) / Double(data.count) <= finalCompressionRatio else {
            return nil
        }
        return encoded
    }

    private static func compressionSample(_ data: Data) -> Data {
        guard data.count > sampleSegmentBytes * 3 else { return data }
        let middleStart = max(0, (data.count - sampleSegmentBytes) / 2)
        var sample = Data()
        sample.reserveCapacity(sampleSegmentBytes * 3)
        sample.append(contentsOf: data.prefix(sampleSegmentBytes))
        sample.append(contentsOf: data[middleStart..<(middleStart + sampleSegmentBytes)])
        sample.append(contentsOf: data.suffix(sampleSegmentBytes))
        return sample
    }

    private static func encodeLZFSE(_ source: Data) -> Data? {
        guard !source.isEmpty else { return Data() }
        let destinationCapacity = source.count + 65_536
        var destination = Data(count: destinationCapacity)
        let count = destination.withUnsafeMutableBytes { destinationBuffer in
            source.withUnsafeBytes { sourceBuffer in
                guard let destinationBase = destinationBuffer.bindMemory(to: UInt8.self).baseAddress,
                      let sourceBase = sourceBuffer.bindMemory(to: UInt8.self).baseAddress else {
                    return 0
                }
                return compression_encode_buffer(
                    destinationBase,
                    destinationCapacity,
                    sourceBase,
                    source.count,
                    nil,
                    COMPRESSION_LZFSE
                )
            }
        }
        guard count > 0, count <= destinationCapacity else { return nil }
        destination.removeSubrange(count..<destination.count)
        return destination
    }

    private static func decodeLZFSE(
        _ source: Data,
        expectedBytes: Int,
        name: String
    ) throws -> Data {
        guard expectedBytes > 0 else { return Data() }
        var destination = Data(count: expectedBytes)
        let count = destination.withUnsafeMutableBytes { destinationBuffer in
            source.withUnsafeBytes { sourceBuffer in
                guard let destinationBase = destinationBuffer.bindMemory(to: UInt8.self).baseAddress,
                      let sourceBase = sourceBuffer.bindMemory(to: UInt8.self).baseAddress else {
                    return 0
                }
                return compression_decode_buffer(
                    destinationBase,
                    expectedBytes,
                    sourceBase,
                    source.count,
                    nil,
                    COMPRESSION_LZFSE
                )
            }
        }
        guard count == expectedBytes else {
            throw ContainerError.compressionFailed(name)
        }
        return destination
    }

    private static func readExact(handle: FileHandle, count: Int) throws -> Data {
        guard count >= 0 else { throw ContainerError.invalidHeader("read") }
        var result = Data()
        result.reserveCapacity(count)
        while result.count < count {
            let next = try handle.read(upToCount: count - result.count) ?? Data()
            guard !next.isEmpty else { throw ContainerError.invalidHeader("truncated") }
            result.append(next)
        }
        return result
    }

    private static func aligned(_ value: Int64, to alignment: Int64) -> Int64 {
        ((value + alignment - 1) / alignment) * alignment
    }

    private static func littleEndianData(_ value: UInt64) -> Data {
        var littleEndian = value.littleEndian
        return Data(bytes: &littleEndian, count: MemoryLayout<UInt64>.size)
    }

    private static func decodeLittleEndianUInt64(_ bytes: Data.SubSequence) -> UInt64 {
        bytes.enumerated().reduce(UInt64(0)) { partial, item in
            partial | (UInt64(item.element) << UInt64(item.offset * 8))
        }
    }

    private static func expectedByteCount(shape: [Int], dtype: DType) -> Int64? {
        var count: Int64 = 1
        for dimension in shape {
            guard dimension >= 0 else { return nil }
            let (next, overflow) = count.multipliedReportingOverflow(by: Int64(dimension))
            guard !overflow else { return nil }
            count = next
        }
        let (bytes, overflow) = count.multipliedReportingOverflow(by: Int64(dtype.size))
        return overflow ? nil : bytes
    }

    private static func dtypeName(_ dtype: DType) -> String {
        switch dtype {
        case .bool: "BOOL"
        case .uint8: "U8"
        case .uint16: "U16"
        case .uint32: "U32"
        case .uint64: "U64"
        case .int8: "I8"
        case .int16: "I16"
        case .int32: "I32"
        case .int64: "I64"
        case .float16: "F16"
        case .bfloat16: "BF16"
        case .float32: "F32"
        case .float64: "F64"
        case .complex64: "C64"
        }
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
        default: throw ContainerError.invalidTensor(value)
        }
    }
}
