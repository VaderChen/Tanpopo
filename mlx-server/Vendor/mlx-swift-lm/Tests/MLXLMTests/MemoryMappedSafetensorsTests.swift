// Copyright © 2026 Tanpopo contributors.

import Foundation
import MLX
import Testing

@testable import MLXLMCommon

@Suite("Memory-mapped safetensors")
struct MemoryMappedSafetensorsTests {
    @Test("Mapping requires a Metal-safe file offset")
    func mappingAlignment() {
        #expect(MemoryMappedTensorArray.canMap(fileOffset: 16, dtype: .float32))
        #expect(!MemoryMappedTensorArray.canMap(fileOffset: 4, dtype: .float32))
        #expect(!MemoryMappedTensorArray.canMap(fileOffset: -16, dtype: .float32))
    }

    @Test("An aligned tensor can be read without changing its values")
    func alignedTensorRoundTrip() throws {
        let url = temporaryURL(name: "aligned.bin")
        defer { try? FileManager.default.removeItem(at: url) }

        let expected: [Float] = [1.25, -2.5, 3.75, 4.5]
        let payload = expected.withUnsafeBytes { Data($0) }
        var file = Data(repeating: 0, count: 16 * 1024)
        file.replaceSubrange(16 ..< 16 + payload.count, with: payload)
        try file.write(to: url, options: .atomic)

        let array = try MemoryMappedTensorArray.load(
            from: url,
            fileOffset: 16,
            byteCount: expected.count * MemoryLayout<Float>.size,
            shape: [expected.count],
            dtype: .float32
        )
        eval(array)

        #expect(array.asArray(Float.self) == expected)
    }

    @Test("Safetensors metadata and aligned payload round-trip")
    func safetensorsRoundTrip() throws {
        let url = temporaryURL(name: "weights.safetensors")
        defer { try? FileManager.default.removeItem(at: url) }

        let expected: [Float] = [0.5, -1.5]
        let file = try makeSafetensors(
            dtype: "F32",
            shape: [expected.count],
            payload: expected.withUnsafeBytes { Data($0) },
            metadata: ["format": "test"]
        )
        try file.write(to: url, options: .atomic)

        let (arrays, metadata) = try MemoryMappedSafetensors.loadArraysAndMetadata(from: url)
        let array = try #require(arrays["weight"])
        eval(array)

        #expect(array.asArray(Float.self) == expected)
        #expect(metadata == ["format": "test"])
    }

    @Test("Unsupported safetensors dtype fails closed")
    func unsupportedDTypeFailsClosed() throws {
        let url = temporaryURL(name: "unsupported.safetensors")
        defer { try? FileManager.default.removeItem(at: url) }

        let file = try makeSafetensors(
            dtype: "U128",
            shape: [1],
            payload: Data(repeating: 0, count: 16),
            metadata: [:]
        )
        try file.write(to: url, options: .atomic)

        #expect(throws: MemoryMappedTensorError.self) {
            _ = try MemoryMappedSafetensors.loadArraysAndMetadata(from: url)
        }
    }

    @Test("Tensor byte count must match shape and dtype")
    func invalidByteCountFailsClosed() throws {
        let url = temporaryURL(name: "invalid-range.bin")
        defer { try? FileManager.default.removeItem(at: url) }
        try Data(repeating: 0, count: 32).write(to: url, options: .atomic)

        #expect(throws: MemoryMappedTensorError.self) {
            _ = try MemoryMappedTensorArray.load(
                from: url,
                fileOffset: 16,
                byteCount: 8,
                shape: [4],
                dtype: .float32
            )
        }
    }

    private func temporaryURL(name: String) -> URL {
        FileManager.default.temporaryDirectory
            .appending(component: "tanpopo-\(UUID().uuidString)-\(name)")
    }

    private func makeSafetensors(
        dtype: String,
        shape: [Int],
        payload: Data,
        metadata: [String: String]
    ) throws -> Data {
        let header: [String: Any] = [
            "__metadata__": metadata,
            "weight": [
                "dtype": dtype,
                "shape": shape,
                "data_offsets": [0, payload.count],
            ],
        ]
        var headerData = try JSONSerialization.data(withJSONObject: header, options: [.sortedKeys])
        while (8 + headerData.count) % MemoryMappedTensorArray.requiredAlignment != 0 {
            headerData.append(0x20)
        }

        var headerLength = UInt64(headerData.count).littleEndian
        var result = withUnsafeBytes(of: &headerLength) { Data($0) }
        result.append(headerData)
        result.append(payload)
        return result
    }
}
