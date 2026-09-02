// Copyright © 2026 Tanpopo contributors.

import Foundation
import MLX

/// DFlash 1／2 block speculative decoding。
///
/// 每輪只有一次 draft forward 與一次 target verify forward；target 驗證後只提交
/// 最長接受 prefix 與一個 correction/bonus token。Greedy 使用精確 token 比對；
/// sampling 使用 rejection sampling，因此保留 Target 的條件分布。
public struct DFlashSpeculativeTokenIterator: TokenIteratorProtocol {
    let target: any DFlashTargetModel
    let drafter: any DFlashDrafterModel

    var anchor: MLXArray
    var targetHiddenStates: MLXArray
    var targetCache: [KVCache]
    var draftCache: [KVCache]

    var processor: LogitProcessor?
    let sampling: DFlashSamplingParameters
    let randomState: MLXRandom.RandomState
    public let maxTokens: Int?
    public let blockSize: Int

    private var pendingTokens = [Int]()
    private var pendingIndex = 0
    private var telemetry = SpeculativeDecodingTelemetry()

    public var tokenCount: Int { telemetry.emittedTokenCount }
    public var promptPrefillTime: TimeInterval = 0
    public var speculativeDecodingTelemetry: SpeculativeDecodingTelemetry? {
        telemetry.roundCount > 0 ? telemetry : nil
    }

    public private(set) var acceptedCount = 0
    public private(set) var proposedCount = 0

    public init(
        input: LMInput,
        target: any LanguageModel,
        drafter: any DFlashDrafterModel,
        targetCache: [KVCache]? = nil,
        draftCache: [KVCache]? = nil,
        parameters: GenerateParameters,
        blockSize: Int
    ) throws {
        guard input.image == nil, input.video == nil, input.audio == nil else {
            throw DFlashError.unsupportedGeneration("DFlash 目前只支援文生文模型。")
        }
        guard input.text.tokens.ndim == 1 else {
            throw DFlashError.unsupportedGeneration("DFlash 目前只支援 batch size 1。")
        }
        guard parameters.maxKVSize == nil else {
            throw DFlashError.unsupportedGeneration(
                "DFlash 目前尚未支援 rotating target KV Cache。")
        }
        guard parameters.kvBits == nil, parameters.kvScheme == nil else {
            throw DFlashError.unsupportedGeneration(
                "DFlash 目前尚未支援量化 target KV Cache。")
        }
        guard let target = target as? any DFlashTargetModel else {
            throw DFlashError.unsupportedTarget(String(describing: type(of: target)))
        }

        try drafter.validate(target: target)
        var effectiveBlockSize = Swift.min(blockSize, drafter.dflashDescriptor.blockSize)
        if parameters.temperature <= 0,
           target.dflashModelType == "qwen3_5",
           drafter.dflashDescriptor.variant == .dflash2,
           effectiveBlockSize > 2 {
            // Hybrid GDN 的多 token verification 與逐 token forward 可能因浮點累積順序
            // 在極接近的 logits 上產生不同 argmax。Greedy 限制為單一 Draft token，
            // 可維持逐 token Target 的確定性；Sampling 仍使用完整訓練 block。
            effectiveBlockSize = 2
            fputs(
                "DFlash 2 hybrid greedy uses block_size=2 for deterministic target parity\n",
                stderr
            )
        }
        guard effectiveBlockSize >= 2 else {
            throw DFlashError.invalidDraftConfiguration("執行 block size 必須至少為 2。")
        }

        self.target = target
        self.drafter = drafter
        self.targetCache = targetCache ?? target.newCache(parameters: parameters)
        self.draftCache = draftCache ?? drafter.newCache()
        guard canTrimPromptCache(self.targetCache)
                || target.dflashSupportsNonTrimmableCacheRollback else {
            throw DFlashError.unsupportedGeneration("target KV Cache 必須可回滾。")
        }

        self.processor = parameters.processor()
        sampling = DFlashSamplingParameters(
            temperature: parameters.temperature,
            topP: parameters.topP,
            topK: parameters.topK,
            minP: parameters.minP
        )
        randomState = parameters.seed.map {
            MLXRandom.RandomState(seed: $0)
        } ?? MLXRandom.RandomState()
        maxTokens = parameters.maxTokens
        self.blockSize = effectiveBlockSize
        anchor = input.text.tokens
        targetHiddenStates = MLXArray.zeros([1, 0, 1])

        let started = Date.timeIntervalSinceReferenceDate
        try prepare(input: input, prefillStepSize: parameters.prefillStepSize)
        promptPrefillTime = Date.timeIntervalSinceReferenceDate - started
    }

    mutating func prepare(input: LMInput, prefillStepSize: Int) throws {
        try GenerationSafety.checkCancellation()
        let prompt = input.text.tokens
        guard prompt.size > 0 else {
            throw DFlashError.unsupportedGeneration("prompt 不可為空。")
        }
        processor?.prompt(prompt)

        var hiddenChunks = [MLXArray]()
        var finalLogits: MLXArray?
        let captureLayerIDs = drafter.dflashDescriptor.targetLayerIDs

        try withPreparedCache(targetCache, lengths: input.text.sequenceLengths) {
            var start = 0
            while start < prompt.size {
                try GenerationSafety.checkCancellation()
                try GenerationSafety.checkResources?()
                let end = Swift.min(prompt.size, start + Swift.max(1, prefillStepSize))
                let chunk = prompt[start ..< end][.newAxis]
                let output = target.dflashForward(
                    chunk,
                    cache: targetCache.isEmpty ? nil : targetCache,
                    captureLayerIDs: captureLayerIDs,
                    captureRollback: false
                )
                hiddenChunks.append(output.hiddenStates)
                finalLogits = output.logits
                try checkedEval(targetCache, output.hiddenStates)
                start = end
            }
            eval(targetCache)
        }
        try GenerationSafety.checkCancellation()

        guard let finalLogits else {
            throw DFlashError.unsupportedGeneration("target prefill 沒有產生 logits。")
        }
        targetHiddenStates = hiddenChunks.count == 1
            ? hiddenChunks[0]
            : concatenated(hiddenChunks, axis: 1)

        var logits = finalLogits[0..., -1, 0...]
        logits = processor?.process(logits: logits) ?? logits
        let firstToken = sampleTarget(logits: logits)
        processor?.didSample(token: firstToken)
        eval(firstToken, targetHiddenStates)

        anchor = firstToken.flattened()
        pendingTokens.append(anchor.item(Int.self))
    }

    private func sampleTarget(logits: MLXArray) -> MLXArray {
        if sampling.isGreedy {
            return argMax(logits, axis: -1)
        }
        return dflashSample(
            probabilities: dflashSamplingProbabilities(
                logits: logits,
                parameters: sampling
            ),
            randomState: randomState
        )
    }

    private func greedyTargetTokens(
        logits: MLXArray,
        proposed: MLXArray,
        draftCount: Int
    ) -> MLXArray {
        guard var processorCopy = processor else {
            return argMax(logits.squeezed(axis: 0), axis: -1)
        }
        var tokens = [MLXArray]()
        tokens.reserveCapacity(draftCount + 1)
        for index in 0 ... draftCount {
            let processed = processorCopy.process(
                logits: logits[0..., index, 0...])
            tokens.append(argMax(processed, axis: -1))
            if index < draftCount {
                processorCopy.didSample(
                    token: proposed[index ..< (index + 1)])
            }
        }
        return concatenated(tokens)
    }

    private func samplingTargetProbabilities(
        logits: MLXArray,
        proposed: MLXArray,
        draftCount: Int
    ) -> MLXArray {
        var processorCopy = processor
        var rows = [MLXArray]()
        rows.reserveCapacity(draftCount + 1)
        for index in 0 ... draftCount {
            var row = logits[0..., index, 0...]
            row = processorCopy?.process(logits: row) ?? row
            rows.append(dflashSamplingProbabilities(logits: row, parameters: sampling))
            if index < draftCount {
                processorCopy?.didSample(
                    token: proposed[index ..< (index + 1)])
            }
        }
        return stacked(rows, axis: 1)
    }

    private func rejectionSample(
        proposed: MLXArray,
        targetProbabilities: MLXArray,
        draftProbabilities: MLXArray,
        draftIndices: MLXArray?
    ) throws -> (accepted: Int, correction: MLXArray) {
        let draftCount = proposed.size
        guard targetProbabilities.ndim == 3,
              targetProbabilities.dim(0) == 1,
              targetProbabilities.dim(1) == draftCount + 1,
              draftProbabilities.ndim == 3,
              draftProbabilities.dim(0) == 1,
              draftProbabilities.dim(1) == draftCount else {
            throw DFlashError.invalidDraftConfiguration(
                "Target／Draft sampling probability 形狀不相容。")
        }

        let proposedIndices = proposed.reshaped(1, draftCount, 1)
        let targetTokenProbabilities = takeAlong(
            targetProbabilities[0..., ..<draftCount, 0...],
            proposedIndices,
            axis: -1
        ).squeezed(axis: -1)
        let draftTokenProbabilities: MLXArray
        if let draftIndices {
            guard draftIndices.shape == draftProbabilities.shape else {
                throw DFlashError.invalidDraftConfiguration(
                    "DFlash 2 sparse probability indices 形狀不一致。")
            }
            draftTokenProbabilities = (
                draftProbabilities * (draftIndices .== proposedIndices)
            ).sum(axis: -1)
        } else {
            guard draftProbabilities.dim(2) == targetProbabilities.dim(2) else {
                throw DFlashError.invalidDraftConfiguration(
                    "DFlash 1 probability vocabulary size 不一致。")
            }
            draftTokenProbabilities = takeAlong(
                draftProbabilities,
                proposedIndices,
                axis: -1
            ).squeezed(axis: -1)
        }

        let uniforms = MLXRandom.uniform(
            0 ..< 1,
            [1, draftCount],
            key: randomState
        )
        let decisions = (
            uniforms * draftTokenProbabilities .< targetTokenProbabilities
        ).flattened()
        eval(decisions)
        var accepted = 0
        for decision in decisions.asArray(Bool.self) {
            guard decision else { break }
            accepted += 1
        }

        if accepted == draftCount {
            return (
                accepted,
                dflashSample(
                    probabilities: targetProbabilities[0..., draftCount, 0...],
                    randomState: randomState
                ).flattened()
            )
        }

        var residual = targetProbabilities[0, accepted, 0...]
        if let draftIndices {
            let indices = draftIndices[0, accepted, 0...]
            let existing = takeAlong(
                residual[.newAxis],
                indices[.newAxis],
                axis: -1
            )
            let adjusted = existing
                - draftProbabilities[0, accepted, 0...][.newAxis]
            residual = putAlong(
                residual[.newAxis],
                indices[.newAxis],
                values: adjusted,
                axis: -1
            )[0]
        } else {
            residual = residual - draftProbabilities[0, accepted, 0...]
        }
        residual = maximum(residual, MLXArray(0))
        let total = residual.sum()
        eval(total)
        if total.item(Float.self) > 0 {
            residual = residual / total
        } else {
            residual = targetProbabilities[0, accepted, 0...]
        }
        return (
            accepted,
            dflashSample(
                probabilities: residual[.newAxis],
                randomState: randomState
            ).flattened()
        )
    }

    mutating func speculateRound() throws {
        let remaining = maxTokens.map { $0 - tokenCount } ?? blockSize
        guard remaining > 0 else { return }

        let draftCount = Swift.min(blockSize - 1, remaining - 1)
        guard draftCount > 0 else {
            passthroughOneToken()
            return
        }

        let proposal = try drafter.propose(
            target: target,
            anchorToken: anchor,
            targetHiddenStates: targetHiddenStates,
            cache: draftCache,
            blockSize: draftCount + 1,
            sampling: sampling,
            randomState: randomState
        )
        let proposed = proposal.tokens.flattened()
        guard proposed.size == draftCount else {
            throw DFlashError.invalidDraftConfiguration(
                "Draft proposal 數量預期 \(draftCount)，實際 \(proposed.size)。")
        }
        asyncEval(proposed)

        let verifyTokens = concatenated([anchor, proposed])
        let verified = target.dflashForward(
            verifyTokens[.newAxis],
            cache: targetCache,
            captureLayerIDs: drafter.dflashDescriptor.targetLayerIDs,
            captureRollback: true
        )

        let proposedList = proposed.asArray(Int.self)
        let accepted: Int
        let correction: MLXArray
        if sampling.isGreedy {
            let targetTokens = greedyTargetTokens(
                logits: verified.logits,
                proposed: proposed,
                draftCount: draftCount
            )
            eval(targetTokens, proposed, verified.hiddenStates)
            let targetList = targetTokens.asArray(Int.self)
            var matched = 0
            while matched < draftCount && targetList[matched] == proposedList[matched] {
                matched += 1
            }
            accepted = matched
            correction = targetTokens[accepted ..< (accepted + 1)]
        } else {
            guard let draftProbabilities = proposal.probabilities else {
                throw DFlashError.invalidDraftConfiguration(
                    "Sampling proposal 缺少 Draft probability。")
            }
            let targetProbabilities = samplingTargetProbabilities(
                logits: verified.logits,
                proposed: proposed,
                draftCount: draftCount
            )
            (accepted, correction) = try rejectionSample(
                proposed: proposed,
                targetProbabilities: targetProbabilities,
                draftProbabilities: draftProbabilities,
                draftIndices: proposal.probabilityIndices
            )
            eval(correction, proposed, verified.hiddenStates)
        }

        for index in 0 ..< accepted {
            let token = proposed[index ..< (index + 1)]
            processor?.didSample(token: token)
            pendingTokens.append(proposedList[index])
        }

        processor?.didSample(token: correction)
        pendingTokens.append(correction.item(Int.self))

        let rejected = draftCount - accepted
        if rejected > 0, let rollback = verified.rollback {
            try rollback.commit(
                acceptedInputCount: accepted + 1,
                rejectedInputCount: rejected
            )
        } else if rejected > 0 {
            let trimmed = trimPromptCache(targetCache, numTokens: rejected)
            guard trimmed == rejected else {
                throw DFlashError.unsupportedGeneration(
                    "target KV Cache 回滾失敗：預期 \(rejected)，實際 \(trimmed)。")
            }
        }

        targetHiddenStates = verified.hiddenStates[0..., ..<(accepted + 1), 0...]
        anchor = correction.flattened()
        proposedCount += draftCount
        acceptedCount += accepted
        telemetry.recordRound(
            drafted: draftCount,
            accepted: accepted,
            targetVerified: draftCount + 1,
            draftModelCalls: 1
        )
        asyncEval(anchor, targetHiddenStates, draftCache)
    }

    /// 輸出額度只剩一個 token 時，直接讓 target 處理目前 anchor。
    mutating func passthroughOneToken() {
        let output = target.dflashForward(
            anchor[.newAxis],
            cache: targetCache,
            captureLayerIDs: drafter.dflashDescriptor.targetLayerIDs,
            captureRollback: false
        )
        var logits = output.logits[0..., -1, 0...]
        logits = processor?.process(logits: logits) ?? logits
        let token = sampleTarget(logits: logits).flattened()
        processor?.didSample(token: token)
        eval(token)
        anchor = token
        targetHiddenStates = output.hiddenStates
        pendingTokens.append(token.item(Int.self))
    }

    public mutating func next() -> Int? {
        if let maxTokens, tokenCount >= maxTokens {
            return nil
        }
        if pendingIndex >= pendingTokens.count {
            pendingTokens.removeAll(keepingCapacity: true)
            pendingIndex = 0
            do {
                try speculateRound()
            } catch {
                // IteratorProtocol 無法拋出錯誤；初始化後的 cache/model 錯誤屬於
                // 不可恢復狀態，保留清楚訊息並結束 stream。
                fputs("[DFlash] \(error.localizedDescription)\n", stderr)
                return nil
            }
        }
        guard pendingIndex < pendingTokens.count else { return nil }
        let token = pendingTokens[pendingIndex]
        pendingIndex += 1
        telemetry.recordGeneratedToken()
        return token
    }

    public mutating func discardGeneratedToken() {
        telemetry.discardGeneratedToken()
    }
}

extension DFlashSpeculativeTokenIterator: MTPStatsCollecting {
    public var proposedDraftTokens: Int { proposedCount }
    public var acceptedDraftTokens: Int { acceptedCount }
    public var passthroughReason: String? { nil }
}

/// 使用既有的 tokenizer、停止詞與 streaming loop 輸出 DFlash 結果。
public func generate(
    input: LMInput,
    cache: [KVCache]? = nil,
    parameters: GenerateParameters,
    context: ModelContext,
    dflashDrafter: any DFlashDrafterModel,
    draftCache: [KVCache]? = nil,
    blockSize: Int = 5,
    wiredMemoryTicket: WiredMemoryTicket? = nil
) throws -> AsyncStream<Generation> {
    let iterator = try DFlashSpeculativeTokenIterator(
        input: input,
        target: context.model,
        drafter: dflashDrafter,
        targetCache: cache,
        draftCache: draftCache,
        parameters: parameters,
        blockSize: blockSize
    )
    let (stream, _) = generateTask(
        promptTokenCount: input.text.tokens.size,
        modelConfiguration: context.configuration,
        tokenizer: context.tokenizer,
        iterator: iterator,
        wiredMemoryTicket: wiredMemoryTicket
    )
    return stream
}
