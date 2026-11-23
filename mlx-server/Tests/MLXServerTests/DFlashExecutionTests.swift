import XCTest
import MLX
import MLXNN
import MLXLMCommon

final class DFlashExecutionTests: XCTestCase {
    func testResidualMatchesHostBranchAndSeededSampling() {
        for dtype in [DType.float32, .float16, .bfloat16] {
            let target = MLXArray([Float(0.125), 0.25, 0.5, 0.125]).asType(dtype)
            for draft in [target, MLXArray([Float(0.5), 0.25, 0, 0.25]).asType(dtype)] {
                for sparse in [false, true] {
                    let indices = MLXArray([2, 0, 3, 1])
                    let q = sparse ? takeAlong(draft, indices, axis: 0) : draft
                    let actual = dflashResidualDistribution(
                        target: target, draft: q, indices: sparse ? indices : nil)
                    let positive = maximum(target - draft, MLXArray(0))
                    let total = positive.sum()
                    eval(total)
                    let expected = total.item(Float.self) > 0 ? positive / total : target
                    eval(actual, expected)
                    XCTAssertEqual(actual.dtype, expected.dtype)
                    XCTAssertEqual(actual.asArray(Float.self), expected.asArray(Float.self))
                    XCTAssertTrue(actual.asArray(Float.self).allSatisfy { $0.isFinite })
                    let a = MLXRandom.RandomState(seed: 123)
                    let b = MLXRandom.RandomState(seed: 123)
                    for _ in 0..<12 {
                        XCTAssertEqual(
                            dflashSample(probabilities: actual, randomState: a).item(Int.self),
                            dflashSample(probabilities: expected, randomState: b).item(Int.self))
                    }
                }
            }
            // Sparse top-k 以外的 Target 機率必須保留。
            let partial = dflashResidualDistribution(
                target: target, draft: MLXArray([Float(1)]).asType(dtype),
                indices: MLXArray([2]))
            XCTAssertEqual(partial.asArray(Float.self), [0.25, 0.5, 0, 0.25])
        }
    }

    func testGreedyAndSamplingPreserveAcceptedPrefixAndCache() throws {
        for temperature: Float in [0, 0.8] {
            for sparse in [false, true] {
                for rejection in [nil, 0, 1, 2] as [Int?] {
                    let target = CountingTarget()
                    let cache = target.newCache(parameters: nil)
                    let parameters = GenerateParameters(
                        maxTokens: 11, temperature: temperature,
                        repetitionPenalty: 1.1, prefillStepSize: 2, seed: 42)
                    var iterator = try DFlashSpeculativeTokenIterator(
                        input: LMInput(tokens: MLXArray([1, 2, 3])), target: target,
                        drafter: CountingDrafter(sparse: sparse, rejection: rejection),
                        targetCache: cache, parameters: parameters, blockSize: 4)
                    var tokens = [Int]()
                    while let token = iterator.next() { tokens.append(token) }
                    let expected = (4..<15).map { $0 % 8 }
                    XCTAssertEqual(tokens, expected,
                                   "temperature=\(temperature), sparse=\(sparse), rejection=\(String(describing: rejection))")
                    XCTAssertEqual(iterator.tokenCount, 11)
                    XCTAssertNil(iterator.next())
                    XCTAssertEqual(cache[0].offset, 13)
                    eval(cache)
                    XCTAssertEqual(cache[0].state[0][0, 0, ..<13, 0].asArray(Int.self),
                                   [1, 2, 3] + Array(expected.dropLast()))
                    XCTAssertGreaterThan(iterator.proposedCount, 0)
                    if rejection == 0 {
                        XCTAssertEqual(iterator.acceptedCount, 0)
                    } else if rejection == nil {
                        XCTAssertEqual(iterator.acceptedCount, iterator.proposedCount)
                    } else {
                        XCTAssertGreaterThan(iterator.acceptedCount, 0)
                        XCTAssertLessThan(iterator.acceptedCount, iterator.proposedCount)
                    }
                }
            }
        }
    }
}

/// 小型可預測 Target 使用實際 MLX 運算及 KVCache，驗證接受、拒絕與 trim 的串接。
private final class CountingTarget: Module, DFlashTargetModel, KVCacheDimensionProvider {
    let dflashModelType = "smoke"
    let dflashHiddenSize = 1
    let dflashLayerCount = 1
    let dflashVocabularySize = 8
    let dflashSupportsNonTrimmableCacheRollback = false
    var kvHeads: [Int] { [1] }

    func prepare(_ input: LMInput, cache: [KVCache], windowSize: Int?) throws -> PrepareResult {
        .tokens(input.text)
    }

    func dflashEmbed(_ tokens: MLXArray) -> MLXArray {
        tokens.asType(.float32).expandedDimensions(axis: -1)
    }

    func dflashComputeLogits(_ hiddenStates: MLXArray) -> MLXArray {
        let next = (hiddenStates.asType(.int32) + 1) % 8
        return MLX.where(next .== MLXArray(0..<8), MLXArray(Float(0)), MLXArray(-Float.infinity))
    }

    func dflashForward(_ inputs: MLXArray, cache: [KVCache]?, captureLayerIDs: [Int],
                       captureRollback: Bool) -> DFlashTargetOutput {
        let hidden = dflashEmbed(inputs)
        let kv = hidden.expandedDimensions(axis: 1)
        _ = cache?.first?.update(keys: kv, values: kv)
        return DFlashTargetOutput(logits: dflashComputeLogits(hidden), hiddenStates: hidden)
    }
}

private final class CountingDrafter: Module, DFlashDrafterModel, @unchecked Sendable {
    let sparse: Bool
    let rejection: Int?
    var dflashDescriptor: DFlashDrafterDescriptor {
        .init(variant: sparse ? .dflash2 : .dflash1, targetModelType: "smoke",
              targetLayerIDs: [0], targetLayerCount: 1, blockSize: 4, maskTokenID: 0)
    }

    init(sparse: Bool, rejection: Int?) {
        self.sparse = sparse
        self.rejection = rejection
        super.init()
    }

    func validate(target: any LanguageModel) throws {}
    func newCache() -> [KVCache] { [] }

    func propose(target: any DFlashTargetModel, anchorToken: MLXArray,
                 targetHiddenStates: MLXArray, cache: [KVCache], blockSize: Int,
                 sampling: DFlashSamplingParameters,
                 randomState: MLXRandom.RandomState) throws -> DFlashDraftProposal {
        let count = blockSize - 1
        let shifts = MLXArray((0..<count).map { $0 == rejection ? 3 : 1 })
        let tokens = ((anchorToken + MLXArray(0..<count) + shifts) % 8).reshaped(1, count)
        guard !sampling.isGreedy else { return DFlashDraftProposal(tokens: tokens) }
        if sparse {
            return DFlashDraftProposal(tokens: tokens, probabilities: .ones([1, count, 1]),
                                       probabilityIndices: tokens.expandedDimensions(axis: -1))
        }
        let q = (tokens.expandedDimensions(axis: -1) .== MLXArray(0..<8)).asType(.float32)
        return DFlashDraftProposal(tokens: tokens, probabilities: q)
    }
}
