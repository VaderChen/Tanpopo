import Foundation
import MLX

/// 每個請求各自持有錯誤與資源檢查，不使用會互相覆蓋的全域 MLX handler。
public enum GenerationSafety {
    @TaskLocal public static var failure: GenerationFailure?
    @TaskLocal public static var checkResources: (@Sendable () throws -> Void)?
    @TaskLocal public static var didFinish: (@Sendable () async -> Void)?
    @TaskLocal public static var cancellation: GenerationCancellation?

    public static var isCancelled: Bool {
        Task.isCancelled || cancellation?.isCancelled == true
    }

    public static func checkCancellation() throws {
        if isCancelled { throw CancellationError() }
    }
}

/// 每個請求一個訊號，供 HTTP event loop 與所有衍生 Task 共用。
/// Swift 的非結構化 Task 不會自動繼承父 Task 後續的取消狀態。
public final class GenerationCancellation: @unchecked Sendable {
    public let id = UUID()
    private let lock = NSLock()
    private var cancelled = false

    public init() {}

    public var isCancelled: Bool { lock.withLock { cancelled } }

    public func cancel() { lock.withLock { cancelled = true } }
}

/// 串流 worker 與 HTTP 消費者間的單次失敗通知。
public final class GenerationFailure: @unchecked Sendable {
    private let lock = NSLock()
    private var error: Error?

    public init() {}

    public var firstError: Error? {
        get { lock.withLock { error } }
        set {
            lock.withLock {
                if error == nil { error = newValue }
            }
        }
    }

    public func check() throws {
        if let error = firstError { throw error }
    }
}

/// 共用的有界 Prefill；呼叫端負責同步切分 token、embedding、mask 與位置資訊。
/// 每一段都完成求值才繼續，避免 lazy graph 將所有段落重新累積在記憶體中。
public func prefillInChunks(
    tokenCount: Int,
    windowSize: Int?,
    cache: [any KVCache],
    initialState: LMOutput.State? = nil,
    evaluate: (Range<Int>, LMOutput.State?) throws -> LMOutput
) throws -> PrepareResult {
    guard tokenCount > 0 else {
        throw MLXError.caught("Prefill 輸入不可為空。")
    }
    let step = max(1, windowSize ?? 512)
    guard !cache.isEmpty || tokenCount <= step else {
        throw MLXError.caught("此模型沒有可供分段 Prefill 使用的快取。")
    }
    var state = initialState
    var offset = 0
    while offset < tokenCount {
        try GenerationSafety.checkCancellation()
        try GenerationSafety.checkResources?()
        let end = offset + min(step, tokenCount - offset)
        let output = try withError { error in
            let output = try evaluate(offset ..< end, state)
            try error.check()
            if end == tokenCount {
                // 只保留最後位置的 logits；中間段只需求值 cache。
                let positions = output.logits.dim(1)
                let last = output.logits[0..., (positions - 1) ..< positions, 0...]
                eval(last, cache)
                try error.check()
                return LMOutput(logits: last, state: output.state)
            }
            eval(cache)
            try error.check()
            return output
        }
        try GenerationSafety.checkCancellation()
        state = output.state
        if end == tokenCount {
            return .logits(output)
        }
        offset = end
    }
    throw MLXError.caught("Prefill 未產生結果。")
}
