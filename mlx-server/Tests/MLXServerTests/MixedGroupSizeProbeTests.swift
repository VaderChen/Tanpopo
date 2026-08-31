import MLX
import MLXNN
import XCTest

@testable import MLXServer

/// 最小可重現案例：在同一個模型內混用不同的量化 group size。
///
/// 實機上 Q4_K 走 group 32 無損沿用、Q6_K 走 group 64 INT8 時會輸出亂碼；
/// 這裡用 4 層合成模型隔離「混合 group」本身是否就是原因，避免在 9B 模型上臆測。
final class MixedGroupSizeProbeTests: XCTestCase {
    private final class ProbeStack: Module {
        @ModuleInfo(key: "l0") var l0: Linear
        @ModuleInfo(key: "l1") var l1: Linear
        @ModuleInfo(key: "l2") var l2: Linear
        @ModuleInfo(key: "l3") var l3: Linear

        init(dimension: Int) {
            self._l0.wrappedValue = Linear(dimension, dimension, bias: false)
            self._l1.wrappedValue = Linear(dimension, dimension, bias: false)
            self._l2.wrappedValue = Linear(dimension, dimension, bias: false)
            self._l3.wrappedValue = Linear(dimension, dimension, bias: false)
            super.init()
        }

        func callAsFunction(_ input: MLXArray) -> MLXArray {
            l3(l2(l1(l0(input))))
        }
    }

    private func makeStack(dimension: Int, weights: [String: MLXArray]) -> ProbeStack {
        let stack = ProbeStack(dimension: dimension)
        try! stack.update(parameters: ModuleParameters.unflattened(weights), verify: [.all])
        return stack
    }

    /// 以與浮點參考的相對誤差判斷；量化雜訊約在個位數 %，
    /// 若配置錯誤會完全失去相關性（誤差接近或超過 100%）。
    private func relativeError(_ actual: MLXArray, _ reference: MLXArray) -> Float {
        let difference = (actual - reference).square().sum().sqrt()
        let scale = reference.square().sum().sqrt()
        let value = (difference / scale)
        eval(value)
        return value.item(Float.self)
    }

    /// 更貼近 GGUF 流程：權重在外部先量化好，再 update 進 QuantizedLinear。
    /// 與 testMixedGroupSizesAcrossLayers 的差別是 MLX 沒有自己做量化，
    /// 用來分辨問題出在「混合 group」還是「外部餵入預量化權重」。
    func testExternallyQuantizedWeightsWithMixedGroupSizes() throws {
        MLXRandom.seed(7)
        let dimension = 256
        let names = ["l0", "l1", "l2", "l3"]
        var weights: [String: MLXArray] = [:]
        for name in names {
            weights["\(name).weight"] = MLXRandom.normal([dimension, dimension]) * 0.05
        }
        let input = MLXRandom.normal([1, dimension])
        let reference = makeStack(dimension: dimension, weights: weights)(input)
        eval(reference)

        let layouts: [(name: String, resolve: (String) -> (Int, Int))] = [
            ("外部量化 全 group64/8bit", { _ in (64, 8) }),
            ("外部量化 全 group32/4bit", { _ in (32, 4) }),
            ("外部量化 混合 32/4 與 64/8", { path in
                (path == "l0" || path == "l2") ? (32, 4) : (64, 8)
            })
        ]

        for layout in layouts {
            var quantizedWeights: [String: MLXArray] = [:]
            for name in names {
                let (groupSize, bits) = layout.resolve(name)
                let source = weights["\(name).weight"]!
                let packed = MLX.quantized(source, groupSize: groupSize, bits: bits)
                quantizedWeights["\(name).weight"] = packed.wq
                // 與 GGUF 轉換路徑一致：scales／biases 以 bfloat16 保存。
                quantizedWeights["\(name).scales"] = packed.scales.asType(.bfloat16)
                quantizedWeights["\(name).biases"] = packed.biases!.asType(.bfloat16)
            }
            let stack = ProbeStack(dimension: dimension)
            quantize(model: stack) { path, _ in
                let (groupSize, bits) = layout.resolve(path)
                return (groupSize, bits, .affine)
            }
            try stack.update(
                parameters: ModuleParameters.unflattened(quantizedWeights),
                verify: [.all]
            )
            let output = stack(input)
            eval(output)
            let error = relativeError(output, reference)
            print("PROBE \(layout.name) 相對誤差 = \(String(format: "%.4f", error * 100))%")
            XCTAssertLessThan(error, 0.5, layout.name)
        }
    }

    /// scales／biases 以 fp16 保存是否被 MLX 的量化路徑接受，且數值是否更準。
    /// fp16 與 bfloat16 同為 2 bytes，頻寬相同，但 fp16 多 3 位尾數。
    func testFP16ScalesAreAcceptedAndMoreAccurateThanBFloat16() throws {
        MLXRandom.seed(11)
        let dimension = 256
        let names = ["l0", "l1", "l2", "l3"]
        var weights: [String: MLXArray] = [:]
        for name in names {
            weights["\(name).weight"] = MLXRandom.normal([dimension, dimension]) * 0.05
        }
        let input = MLXRandom.normal([1, dimension])
        let reference = makeStack(dimension: dimension, weights: weights)(input)
        eval(reference)

        var errors: [DType: Float] = [:]
        for dtype in [DType.bfloat16, DType.float16] {
            var packedWeights: [String: MLXArray] = [:]
            for name in names {
                let packed = MLX.quantized(weights["\(name).weight"]!, groupSize: 32, bits: 4)
                packedWeights["\(name).weight"] = packed.wq
                packedWeights["\(name).scales"] = packed.scales.asType(dtype)
                packedWeights["\(name).biases"] = packed.biases!.asType(dtype)
            }
            let stack = ProbeStack(dimension: dimension)
            quantize(model: stack) { _, _ in (32, 4, .affine) }
            try stack.update(
                parameters: ModuleParameters.unflattened(packedWeights),
                verify: [.all]
            )
            let output = stack(input)
            eval(output)
            errors[dtype] = relativeError(output, reference)
            print("PROBE scales dtype=\(dtype) 相對誤差 = "
                + "\(String(format: "%.4f", errors[dtype]! * 100))%")
        }
        // fp16 被接受且不應比 bfloat16 差。
        XCTAssertLessThanOrEqual(errors[.float16]!, errors[.bfloat16]! * 1.05)
    }

    func testMixedGroupSizesAcrossLayers() throws {
        MLXRandom.seed(42)
        let dimension = 256
        var weights: [String: MLXArray] = [:]
        for name in ["l0", "l1", "l2", "l3"] {
            weights["\(name).weight"] = MLXRandom.normal([dimension, dimension]) * 0.05
        }
        let input = MLXRandom.normal([1, dimension])

        let reference = makeStack(dimension: dimension, weights: weights)(input)
        eval(reference)

        // 三種配置：全 group64／全 group32／逐層混合。
        let configurations: [(name: String, resolve: (String) -> (Int, Int))] = [
            ("全 group64/8bit", { _ in (64, 8) }),
            ("全 group32/4bit", { _ in (32, 4) }),
            ("混合 l0,l2=group32/4bit  l1,l3=group64/8bit", { path in
                (path == "l0" || path == "l2") ? (32, 4) : (64, 8)
            })
        ]

        for configuration in configurations {
            let stack = makeStack(dimension: dimension, weights: weights)
            quantize(model: stack) { path, _ in
                let (groupSize, bits) = configuration.resolve(path)
                return (groupSize, bits, .affine)
            }
            let output = stack(input)
            eval(output)
            let error = relativeError(output, reference)
            print("PROBE \(configuration.name) 相對誤差 = \(String(format: "%.4f", error * 100))%")
            XCTAssertLessThan(error, 0.5, configuration.name)
        }
    }
}
