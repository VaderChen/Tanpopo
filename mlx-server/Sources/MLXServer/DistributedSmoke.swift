import Foundation
import MLX
import MLXNN

/// 小型、固定資料的跨程序 Smoke，不下載模型，也不需要 Python 或外部套件。
enum DistributedSmoke {
    private final class ScaledLinear: Linear {
        override func callAsFunction(_ input: MLXArray) -> MLXArray { super.callAsFunction(input) * 2 }
    }
    private final class Fixture: Module, @unchecked Sendable {
        @ModuleInfo var dense: Linear
        @ModuleInfo var quantized: Linear
        @ModuleInfo var specialized: Linear
        @ModuleInfo var transformed: Linear
        override init() {
            let weight = (MLXArray(0..<512).reshaped(8, 64).asType(.float32) - 256) / 1024
            _dense.wrappedValue = Linear(weight: weight, bias: MLXArray(0..<8).asType(.float32) / 16)
            _quantized.wrappedValue = QuantizedLinear(weight: weight, bias: nil, groupSize: 64, bits: 4)
            _specialized.wrappedValue = ScaledLinear(weight: weight, bias: nil)
            _transformed.wrappedValue = Linear(weight: weight, bias: nil)
        }
    }

    static func run(session: DistributedTensorSession) throws {
        let fixture = Fixture()
        let reference = Fixture()
        eval(fixture, reference)
        // 模擬 sanitize 產生的 lazy 轉換；此層必須留在主節點並正確求值。
        try fixture.update(modules: ModuleChildren.unflattened([
            ("transformed", Linear(weight: reference.transformed.weight * 1, bias: nil) as Module)
        ]), verify: [.all])
        let group = session.group
        let deadline = DistributedDeadline(seconds: session.configuration.startupTimeout, operation: "Smoke")
        defer { withExtendedLifetime(deadline) {} }
        for count in [1, 64, 4096, 262144] {
            let input = MLXArray.ones([count], dtype: .float32) * Float(group.rank + 1)
            let result = try group.sum(input)
            guard all(result .== Float(group.size * (group.size + 1) / 2)).item(Bool.self) else {
                throw DistributedError.invalid("all_sum Smoke 結果不符。")
            }
        }
        try session.install(on: fixture)
        if group.rank != 0 {
            try session.workerLoop()
            print("分散式 worker Smoke 通過。")
            return
        }
        try session.health()
        for shape in [[64], [1, 64], [2, 3, 64]] {
            for dtype in [DType.float32, .float16, .bfloat16] {
                let count = shape.reduce(1, *)
                let input = (MLXArray(0..<count).asType(.float32) / Float(count)).reshaped(shape).asType(dtype)
                for (actual, expected) in [(fixture.dense(input), reference.dense(input)),
                    (fixture.quantized(input), reference.quantized(input)),
                    (fixture.specialized(input), reference.specialized(input)),
                    (fixture.transformed(input), reference.transformed(input))] {
                    guard allClose(actual, expected, rtol: 0.02, atol: 0.02).item(Bool.self) else {
                        throw DistributedError.invalid("分片 Linear 與單機結果不符：\(shape)／\(dtype)。")
                    }
                }
                // 架構可直接讀取 Linear 權重，或依量化型別走融合運算；兩者都不能
                // 被部分列取代而悄悄改變形狀與結果。這些特殊路徑由主節點執行。
                guard let quantized = fixture.quantized as? QuantizedLinear,
                    fixture.specialized is ScaledLinear,
                    type(of: fixture.transformed) == Linear.self else {
                    throw DistributedError.invalid("分散式包裝未保留量化型別或自訂層。")
                }
                let directDense = matmul(input, fixture.dense.weight.T) + fixture.dense.bias!
                let directQuantized = quantizedMM(input, quantized.weight, scales: quantized.scales,
                    biases: quantized.biases, transpose: true, groupSize: quantized.groupSize,
                    bits: quantized.bits, mode: quantized.mode)
                guard allClose(directDense, reference.dense(input), rtol: 0.02, atol: 0.02).item(Bool.self),
                    allClose(directQuantized, reference.quantized(input), rtol: 0.02, atol: 0.02).item(Bool.self) else {
                    throw DistributedError.invalid("直接存取完整權重的結果不符。")
                }
            }
        }
        // 四個呼叫者交錯送出張量；通訊順序仍由 session 統一管理。
        let work = DispatchGroup()
        for index in 0..<4 {
            work.enter()
            DispatchQueue.global().async {
                defer { work.leave() }
                for step in 0..<8 {
                    let input = MLXArray.ones([1, 64]) * Float(index + step)
                    let result = fixture.dense(input)
                    let expected = reference.dense(input)
                    if !allClose(result, expected, rtol: 1e-4, atol: 1e-4).item(Bool.self) {
                        DistributedTensorSession.failGroup(DistributedError.invalid("並行 Smoke 結果不符。"))
                    }
                }
            }
        }
        work.wait()
        try session.health()
        try session.shutdown()
        print("分散式 Smoke 通過：collectives、FP32／FP16／BF16、一般／Q4 線性層、完整權重存取、自訂層與 lazy 轉換保留、四路交錯運算、健康檢查與停止。")
    }
}
