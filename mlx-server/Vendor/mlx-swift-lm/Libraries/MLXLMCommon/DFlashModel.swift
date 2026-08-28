// Copyright © 2026 Tanpopo contributors.

import Foundation
import MLX
import MLXNN

/// Target 模型一次 forward 的 DFlash 輸出。
///
/// `hiddenStates` 依照 drafter 宣告的 layer ID 順序串接在最後一維，
/// 讓 drafter 不必知道 target 內部的具體型別與 layer 容器。
public struct DFlashTargetOutput {
    public let logits: MLXArray
    public let hiddenStates: MLXArray
    public let rollback: (any DFlashTargetRollback)?

    public init(
        logits: MLXArray,
        hiddenStates: MLXArray,
        rollback: (any DFlashTargetRollback)? = nil
    ) {
        self.logits = logits
        self.hiddenStates = hiddenStates
        self.rollback = rollback
    }
}

/// Hybrid target 在 block verify 後提交已接受 prefix 的回滾介面。
///
/// 一般 KV Cache 由 iterator 直接 trim；Gated Delta Net 等 recurrent cache
/// 則由 target 保存該輪輸入，僅重播必要的 recurrent layer。
public protocol DFlashTargetRollback {
    func commit(acceptedInputCount: Int, rejectedInputCount: Int) throws
}

public enum DFlashVariant: String, Sendable, Equatable {
    case dflash1
    case dflash2
}

/// DFlash proposal 與 Target verification 共用的取樣設定。
public struct DFlashSamplingParameters: Sendable, Equatable {
    public let temperature: Float
    public let topP: Float
    public let topK: Int
    public let minP: Float

    public init(temperature: Float, topP: Float, topK: Int, minP: Float) {
        self.temperature = temperature
        self.topP = topP
        self.topK = topK
        self.minP = minP
    }

    public var isGreedy: Bool { temperature <= 0 }
}

/// 一輪 Draft proposal。
///
/// DFlash 1 在 sampling 模式回傳完整 vocabulary probability；DFlash 2
/// 只回傳 selector top-k 的 sparse probability，並以 `probabilityIndices`
/// 指出其 token ID。Greedy 模式不需要 probability。
public struct DFlashDraftProposal {
    public let tokens: MLXArray
    public let probabilities: MLXArray?
    public let probabilityIndices: MLXArray?

    public init(
        tokens: MLXArray,
        probabilities: MLXArray? = nil,
        probabilityIndices: MLXArray? = nil
    ) {
        self.tokens = tokens
        self.probabilities = probabilities
        self.probabilityIndices = probabilityIndices
    }
}

/// 產生與 mlx-swift-lm 一般生成相同的 Target 取樣分布。
///
/// filter 順序與 `TopPSampler` 一致：top-p、min-p、top-k，最後才套用
/// temperature。Draft 可使用任何可計算的 q 分布；Target p 必須與服務
/// 對外的一般生成語意完全一致，exact rejection sampling 才能維持 lossless。
public func dflashSamplingProbabilities(
    logits: MLXArray,
    parameters: DFlashSamplingParameters
) -> MLXArray {
    precondition(parameters.temperature > 0)
    let logits = logits.dtype == .bfloat16 ? logits.asType(.float32) : logits
    var logProbabilities = logSoftmax(logits)
    let negativeInfinity = MLXArray(-Float.infinity)

    if parameters.topP > 0, parameters.topP < 1 {
        let sortedIndices = argSort(logProbabilities, axis: -1)
        let sortedLogProbabilities = takeAlong(
            logProbabilities, sortedIndices, axis: -1)
        let cumulative = cumsum(exp(sortedLogProbabilities), axis: -1)
        let filtered = MLX.where(
            cumulative .> (1 - parameters.topP),
            sortedLogProbabilities,
            negativeInfinity
        )
        logProbabilities = putAlong(
            logProbabilities,
            sortedIndices,
            values: filtered,
            axis: -1
        )
    }

    if parameters.minP > 0 {
        let threshold = logProbabilities.max(axis: -1, keepDims: true)
            + log(MLXArray(parameters.minP))
        logProbabilities = MLX.where(
            logProbabilities .>= threshold,
            logProbabilities,
            negativeInfinity
        )
    }

    if parameters.topK > 0, parameters.topK < logProbabilities.dim(-1) {
        let maskedIndices = argPartition(
            -logProbabilities,
            kth: parameters.topK - 1,
            axis: -1
        )[0..., parameters.topK...]
        logProbabilities = putAlong(
            logProbabilities,
            maskedIndices,
            values: negativeInfinity,
            axis: -1
        )
    }

    return softmax(
        logProbabilities * (1 / MLXArray(parameters.temperature)),
        axis: -1
    )
}

public func dflashSample(
    probabilities: MLXArray,
    randomState: MLXRandom.RandomState
) -> MLXArray {
    categorical(log(probabilities), key: randomState)
}

/// 可提供 DFlash 中間層特徵的 target model。
public protocol DFlashTargetModel: LanguageModel {
    var dflashModelType: String { get }
    var dflashHiddenSize: Int { get }
    var dflashLayerCount: Int { get }
    var dflashVocabularySize: Int { get }
    var dflashSupportsNonTrimmableCacheRollback: Bool { get }

    func dflashEmbed(_ tokens: MLXArray) -> MLXArray
    func dflashComputeLogits(_ hiddenStates: MLXArray) -> MLXArray

    func dflashForward(
        _ inputs: MLXArray,
        cache: [KVCache]?,
        captureLayerIDs: [Int],
        captureRollback: Bool
    ) -> DFlashTargetOutput
}

/// 與 target 配對及建立 iterator 所需的穩定 metadata。
public struct DFlashDrafterDescriptor: Sendable, Equatable {
    public let variant: DFlashVariant
    public let targetModelType: String
    public let targetLayerIDs: [Int]
    public let targetLayerCount: Int
    public let blockSize: Int
    public let maskTokenID: Int

    public init(
        variant: DFlashVariant,
        targetModelType: String,
        targetLayerIDs: [Int],
        targetLayerCount: Int,
        blockSize: Int,
        maskTokenID: Int
    ) {
        self.variant = variant
        self.targetModelType = targetModelType
        self.targetLayerIDs = targetLayerIDs
        self.targetLayerCount = targetLayerCount
        self.blockSize = blockSize
        self.maskTokenID = maskTokenID
    }
}

/// DFlash block-diffusion drafter 的通用介面。
public protocol DFlashDrafterModel: BaseLanguageModel, Sendable {
    var dflashDescriptor: DFlashDrafterDescriptor { get }

    func validate(target: any LanguageModel) throws

    func newCache() -> [KVCache]

    /// 由最後一個 target token 與新取得的 target hidden states，一次提出
    /// `blockSize - 1` 個 token。Token 形狀為 `[B, blockSize - 1]`。
    func propose(
        target: any DFlashTargetModel,
        anchorToken: MLXArray,
        targetHiddenStates: MLXArray,
        cache: [KVCache],
        blockSize: Int,
        sampling: DFlashSamplingParameters,
        randomState: MLXRandom.RandomState
    ) throws -> DFlashDraftProposal
}

public enum DFlashError: LocalizedError {
    case unsupportedTarget(String)
    case incompatibleTarget(String)
    case invalidDraftConfiguration(String)
    case unsupportedGeneration(String)

    public var errorDescription: String? {
        switch self {
        case .unsupportedTarget(let detail):
            "DFlash 不支援目前的 target model：\(detail)"
        case .incompatibleTarget(let detail):
            "DFlash draft 與 target model 不相容：\(detail)"
        case .invalidDraftConfiguration(let detail):
            "DFlash draft 設定無效：\(detail)"
        case .unsupportedGeneration(let detail):
            "目前的 DFlash 生成模式不支援：\(detail)"
        }
    }
}
