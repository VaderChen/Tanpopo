import Foundation
import XCTest
import Cmlx
import MLX
import MLXDistributedBridge
@testable import MLXServer

final class DistributedWeightLoadingTests: XCTestCase {
    func testBF16ShardsKeepTheUnshardedReductionOrder() throws {
        let weight = (sin(MLXArray(0..<(1024 * 1024)).asType(.float32) * 0.013) / 32).reshaped(1024, 1024).asType(.bfloat16)
        let input = cos(MLXArray(0..<(16 * 1024)).asType(.float32) * 0.007).reshaped(16, 1024).asType(.bfloat16)
        let bias = (MLXArray(0..<1024).asType(.float32) / 1024).asType(.bfloat16)
        eval(weight, input, bias)
        let expected = addMM(bias, input, weight.T)
        var outputs: [MLXArray] = []
        for start in [0, 512] {
            var result = mlx_array_new()
            let localWeight = contiguous(weight[start..<(start + 512)])
            let localBias = contiguous(bias[start..<(start + 512)])
            var biasContext = localBias.ctx
            XCTAssertEqual(tanpopo_distributed_linear(&result, input.ctx, localWeight.ctx, &biasContext, 1024), 0)
            outputs.append(MLXArray(result))
        }
        XCTAssertTrue(all(concatenated(outputs, axis: 1) .== expected).item(Bool.self))
    }

    func testCompressedAndRawWeightsRemainRowReadableAfterTemporaryFileCleanup() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".fgguf")
        defer { try? FileManager.default.removeItem(at: url) }
        let compressed = MLXArray.ones([128, 128])
        let raw = MLXArray(Array(0..<64).map(Float.init), [8, 8])
        let stats = try FastGGUFContainer.store(arrays: ["compressed": compressed, "raw": raw],
            metadata: [:], cacheKey: "rows", url: url)
        XCTAssertEqual(stats.compressedTensorCount, 1)
        XCTAssertEqual(stats.rawTensorCount, 1)
        let arrays = try DistributedWeightLoading.$enabled.withValue(true) {
            try FastGGUFContainer.load(from: url, expectedCacheKey: "rows", memoryMapped: false).0
        }
        // Reader 持有已開啟的 backing；scope 清理與 unlink 不得讓後續 lazy eval 失敗。
        try FileManager.default.removeItem(at: url)
        for name in ["compressed", "raw"] {
            let array = try XCTUnwrap(arrays[name])
            XCTAssertTrue(tanpopo_distributed_can_copy_rows(array.ctx))
            var context = mlx_array_new()
            XCTAssertEqual(tanpopo_distributed_copy_rows(&context, array.ctx, 1, 3), 0)
            let rows = MLXArray(context)
            eval(rows)
            XCTAssertEqual(rows.shape, [2, array.shape[1]])
            let expected = name == "raw" ? Array(8..<24).map(Float.init) : Array(repeating: Float(1), count: 256)
            XCTAssertEqual(rows.asArray(Float.self), expected)
        }
    }
}
