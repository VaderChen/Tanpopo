import Foundation
import XCTest
import Cmlx
import MLX
import MLXNN
import MLXLMCommon
import MLXDistributedBridge
@testable import MLXServer

final class DiskOffloadTests: XCTestCase {
    private class Fixture: Module {
        @ModuleInfo var linear: Linear
        @ModuleInfo var embedding: Embedding
        @ModuleInfo var experts: SwitchLinear
        override init() {
            _linear.wrappedValue = Linear(64, 48, bias: true)
            _embedding.wrappedValue = Embedding(embeddingCount: 19, dimensions: 64)
            _experts.wrappedValue = SwitchLinear(inputDims: 64, outputDims: 48, numExperts: 3, bias: true)
            super.init()
        }
    }
    private func close(_ actual: MLXArray, _ expected: MLXArray, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertEqual(actual.shape, expected.shape, file: file, line: line)
        XCTAssertLessThan(abs(actual - expected).max().item(Float.self), 0.002, file: file, line: line)
    }
    private func exercise(quantized: Bool) throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let url = directory.appendingPathComponent("model.safetensors")
        let reference = Fixture()
        if quantized { MLXNN.quantize(model: reference, groupSize: 32, bits: 4) }
        eval(reference)
        try save(arrays: Dictionary(uniqueKeysWithValues: reference.parameters().flattened()), url: url)
        let model = Fixture()
        if quantized { MLXNN.quantize(model: model, groupSize: 32, bits: 4) }
        try model.update(parameters: ModuleParameters.unflattened(try loadArrays(url: url)), verify: [.all])
        let session = try DiskOffloadSession(cacheBytes: 1024, tileBytes: 512, runtimeBytes: 1024 * 1024 * 1024)
        try session.install(on: model)
        XCTAssertEqual(session.summary["modules"] as? Int, 3)
        let x = sin(MLXArray(0..<(6 * 64)).asType(.float32)).reshaped(2, 3, 64)
        close(model.linear(x), reference.linear(x))
        let ids = MLXArray([Int32(18), 2, 2, 0, 3, 4], [2, 3])
        close(model.embedding(ids), reference.embedding(ids))
        close(model.embedding.asLinear(x), reference.embedding.asLinear(x))
        // 非排序路由、重複專家、升維廣播與下投影均保持順序。
        let indices = MLXArray([Int32(2), 0, 1, 2, 1, 1, 0, 2, 2, 1, 0, 0], [2, 3, 2])
        let expanded = MLX.expandedDimensions(x, axes: [-2, -3])
        close(model.experts(expanded, indices), reference.experts(expanded, indices))
        let down = broadcast(expanded, to: [2, 3, 2, 1, 64]) * 0.5
        close(model.experts(down, indices), reference.experts(down, indices))
        let (sorted, order, _) = gatherSort(x: expanded, indices: indices)
        close(model.experts(sorted, order, sortedIndices: true), reference.experts(sorted, order, sortedIndices: true))
        close(model.linear(x), reference.linear(x)) // 逐出後再次讀取
        XCTAssertLessThanOrEqual(session.summary["cache_peak_bytes"] as? Int ?? Int.max, 1024)
        XCTAssertGreaterThan(session.summary["read_bytes"] as? Int ?? 0, 1024)
        XCTAssertThrowsError(try withError { eval(model.linear.weight) })
        XCTAssertThrowsError(try withError { eval(model.embedding(MLXArray([Float(0.5)]))) })
    }
    func testDenseEmbeddingAndExpertsFloat() throws { try exercise(quantized: false) }
    func testDenseEmbeddingAndExpertsQuantized() throws { try exercise(quantized: true) }

    func testReadsRowsBeyondTwoGiBWithoutMaterializingTensor() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        FileManager.default.createFile(atPath: url.path, contents: nil)
        defer { try? FileManager.default.removeItem(at: url) }
        let file = try FileHandle(forWritingTo: url)
        defer { try? file.close() }
        let rows = 600_000_000
        try file.truncate(atOffset: UInt64(rows * 4))
        try file.seek(toOffset: UInt64((rows - 2) * 4))
        try file.write(contentsOf: MLXArray([Float(3), 7]).asData())
        let source = try DistributedWeightFile(url: url).array(offset: 0, shape: [rows, 1], dtype: .float32)
        XCTAssertTrue(tanpopo_disk_is_file_tensor(source.ctx))
        var context = mlx_array_new()
        XCTAssertEqual(tanpopo_disk_read_rows(&context, source.ctx, rows - 2, 2), 0)
        let value = MLXArray(context)
        XCTAssertEqual(value.asArray(Float.self), [3, 7])
        XCTAssertTrue(tanpopo_disk_is_file_tensor(source.ctx))
        class Lookup: Module {
            @ModuleInfo var table: Embedding
            init(_ weight: MLXArray) { _table.wrappedValue = Embedding(weight: weight); super.init() }
        }
        let model = Lookup(source)
        let session = try DiskOffloadSession(cacheBytes: 1024, tileBytes: 512, runtimeBytes: 1024 * 1024 * 1024)
        try session.install(on: model)
        XCTAssertEqual(model.table(MLXArray([Int32(rows - 1), Int32(rows - 2)])).asArray(Float.self), [7, 3])
        // 小型 view 的 lazy 來源仍可能超過可用記憶體，預檢必須看完整運算圖。
        let view = source[0..<2]
        var graphBytes = 0
        XCTAssertEqual(tanpopo_disk_resident_graph_bytes(view.ctx, &graphBytes), 0)
        XCTAssertGreaterThanOrEqual(graphBytes, rows * 4)
        var invalid = mlx_array_new()
        defer { mlx_array_free(invalid) }
        XCTAssertNotEqual(tanpopo_disk_read_rows(&invalid, source.ctx, rows - 1, 2), 0)
    }

    func testCompressedFastGGUFIsStreamedToBackingFile() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".fgguf")
        defer { try? FileManager.default.removeItem(at: url) }
        let expected = MLXArray.ones([512, 256])
        let stats = try FastGGUFContainer.store(arrays: ["weight": expected], metadata: [:], cacheKey: "disk", url: url)
        XCTAssertEqual(stats.compressedTensorCount, 1)
        let arrays = try DiskWeightLoading.$enabled.withValue(true) {
            try FastGGUFContainer.load(from: url, expectedCacheKey: "disk", memoryMapped: false).0
        }
        let source = try XCTUnwrap(arrays["weight"])
        XCTAssertTrue(tanpopo_disk_is_file_tensor(source.ctx))
        var context = mlx_array_new()
        XCTAssertEqual(tanpopo_disk_read_rows(&context, source.ctx, 500, 2), 0)
        let value = MLXArray(context)
        close(value, expected[500..<502])
    }

    func testCompressedStreamSpansMultipleInputBuffers() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".fgguf")
        defer { try? FileManager.default.removeItem(at: url) }
        let data = (0..<131072).map { index -> Float in
            let bits = UInt32(truncatingIfNeeded: index &* 1664525 &+ 1013904223) & 0x00ff_ffff
            return Float(bits) / Float(0x0100_0000)
        }
        let expected = MLXArray(data, [512, 256])
        let stats = try FastGGUFContainer.store(arrays: ["weight": expected], metadata: [:], cacheKey: "multi", url: url)
        XCTAssertEqual(stats.compressedTensorCount, 1)
        XCTAssertGreaterThan(stats.storedBytes - 1_048_576, 65536)
        let arrays = try DiskWeightLoading.$enabled.withValue(true) {
            try FastGGUFContainer.load(from: url, expectedCacheKey: "multi", memoryMapped: false).0
        }
        let source = try XCTUnwrap(arrays["weight"])
        var context = mlx_array_new()
        XCTAssertEqual(tanpopo_disk_read_rows(&context, source.ctx, 255, 3), 0)
        close(MLXArray(context), expected[255..<258])
    }

    func testRejectsInsufficientBudgets() throws {
        XCTAssertThrowsError(try DiskOffloadSession(cacheBytes: 512, tileBytes: 512, runtimeBytes: 1024 * 1024 * 1024))
        let model = Fixture()
        // 權重已常駐／非檔案來源不能冒充磁碟卸載；預算不足則於 eval 前拒絕。
        let session = try DiskOffloadSession(cacheBytes: 1024, tileBytes: 512, runtimeBytes: 512 * 1024 * 1024 + 2048)
        XCTAssertThrowsError(try session.install(on: model))
    }
}
