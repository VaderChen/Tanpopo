import Foundation
import XCTest
import MLX
import MLXNN
import MLXLMCommon

final class SwiGLUFusionTests: XCTestCase {
    func testFusedProductPreservesValuesAndLayouts() {
        for dtype in [DType.float32, .float16, .bfloat16] {
            for shape in [[1, 1, 1024], [1, 7, 3072], [2, 17, 256]] {
                let count = shape.reduce(1, *)
                let gate = MLXArray((0..<count).map { Float($0 % 199 - 99) / 9 })
                    .reshaped(shape).asType(dtype)
                let up = MLXArray((0..<count).map { Float($0 % 103 - 51) / 13 })
                    .reshaped(shape).asType(dtype)
                for (g, u) in [(gate, up), (gate.swappedAxes(1, 2), up.swappedAxes(1, 2)),
                               (gate, up[0..., 0..<1, 0...])] {
                    let expected = silu(g) * u
                    let actual = compiledSiluProduct(g, u)
                    eval(expected, actual)
                    XCTAssertEqual(actual.shape, expected.shape)
                    XCTAssertEqual(actual.dtype, expected.dtype)
                    XCTAssertEqual(actual.asArray(Float.self), expected.asArray(Float.self),
                                   "dtype=\(dtype), shape=\(g.shape)")
                }
            }
        }
    }

    func testBenchmarkWhenRequested() throws {
        guard let output = ProcessInfo.processInfo.environment["TANPOPO_SILU_BENCHMARK_OUTPUT"] else {
            throw XCTSkip("設定 TANPOPO_SILU_BENCHMARK_OUTPUT 才執行運算基準。")
        }
        var rows = [[String: Any]]()
        for dtype in [DType.float32, .float16, .bfloat16] {
            for tokens in [1, 8, 128] {
                let shape = [1, tokens, 8192]
                let input = MLXArray.ones(shape).asType(dtype) * 0.5
                let up = MLXArray.ones(shape).asType(dtype) * 0.75
                eval(input, up)
                func run(fused: Bool) -> Double {
                    let start = ContinuousClock.now
                    var result = input
                    for _ in 0..<32 {
                        let gate = result + 0.125
                        result = fused ? compiledSiluProduct(gate, up) : silu(gate) * up
                    }
                    eval(result)
                    let duration = start.duration(to: .now).components
                    return Double(duration.seconds) * 1000 + Double(duration.attoseconds) / 1e15
                }
                for _ in 0..<3 { _ = run(fused: false); _ = run(fused: true) }
                var baseline = [Double](), fused = [Double]()
                for index in 0..<10 {
                    // 交錯順序，降低編譯暖機及背景負載的單向偏差。
                    if index.isMultiple(of: 2) {
                        baseline.append(run(fused: false)); fused.append(run(fused: true))
                    } else {
                        fused.append(run(fused: true)); baseline.append(run(fused: false))
                    }
                }
                rows.append(["dtype": String(describing: dtype), "shape": shape,
                             "dependent_products": 32, "baseline_ms": baseline, "fused_ms": fused])
            }
        }
        let data = try JSONSerialization.data(withJSONObject: rows, options: [.prettyPrinted, .sortedKeys])
        try data.write(to: URL(fileURLWithPath: output), options: .withoutOverwriting)
    }
}
