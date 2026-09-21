import Foundation
import XCTest
@testable import MLXServer

final class FastGGUFValidationTests: XCTestCase {
    func testMalformedTensorBoundsReturnErrors() throws {
        for values: [String: Any] in [
            ["offset": Int64.max],
            ["offset": Int64.max - 1048576 - 15, "shape": [32], "storedBytes": 32, "rawBytes": 32],
            ["storedBytes": Int64.max, "rawBytes": Int64.max, "shape": [Int64.max]],
            ["offset": -1],
            ["shape": [0, Int64.max], "storedBytes": 0, "rawBytes": 0],
            ["encoding": "lzfse", "shape": [536870912], "storedBytes": 1, "rawBytes": 536870912]
        ] {
            try rejects(values)
        }
    }

    private func rejects(_ overrides: [String: Any]) throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".fgguf")
        defer { try? FileManager.default.removeItem(at: url) }
        var tensor: [String: Any] = [
            "name": "test", "dtype": "U8", "shape": [1], "encoding": "raw",
            "offset": 0, "storedBytes": 1, "rawBytes": 1
        ]
        tensor.merge(overrides) { _, new in new }
        let header: [String: Any] = [
            "formatVersion": 1, "cacheKey": "review", "payloadOffset": 1048576,
            "metadata": [:],
            "tensors": [tensor]
        ]
        let encoded = try JSONSerialization.data(withJSONObject: header)
        var data = Data("FGGUF001".utf8)
        var length = UInt64(encoded.count).littleEndian
        withUnsafeBytes(of: &length) { data.append(contentsOf: $0) }
        data.append(encoded)
        data.append(Data(count: 1048577 - data.count))
        try data.write(to: url)
        XCTAssertThrowsError(try FastGGUFContainer.load(from: url, expectedCacheKey: "review", memoryMapped: false))
    }
}
