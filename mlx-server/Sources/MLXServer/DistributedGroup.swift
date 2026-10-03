import Foundation
import Cmlx
import MLX
import MLXDistributedBridge

/// 只跨不可恢復的通訊操作設定期限；空閒 worker 不套用推論逾時。
final class DistributedDeadline: @unchecked Sendable {
    private final class Context: @unchecked Sendable {
        let lock = NSLock()
        var operation: String
        init(_ operation: String) { self.operation = operation }
        func describe() -> String { lock.withLock { operation } }
    }
    private let timer: DispatchSourceTimer
    private let context: Context
    init(seconds: Int, operation: String) {
        let context = Context(operation)
        self.context = context
        timer = DispatchSource.makeTimerSource(queue: .global(qos: .utility))
        timer.schedule(deadline: .now() + .seconds(seconds))
        timer.setEventHandler {
            fputs("mlx-server error: 分散式 \(context.describe()) 逾時；終止此 Rank，避免保留損壞的通訊群組。\n", stderr)
            _exit(70)
        }
        timer.resume()
    }
    deinit { timer.cancel() }
    func cancel() { timer.cancel() }
    func update(_ operation: String) { context.lock.withLock { context.operation = operation } }
}

final class DistributedGroup: @unchecked Sendable {
    let rank: Int
    let size: Int
    private let handle: UnsafeMutableRawPointer

    static func available(_ backend: String) -> Bool { tanpopo_distributed_available(backend) }

    init(configuration: DistributedConfiguration, rank: Int) throws {
        guard Self.available(configuration.backend) else {
            throw DistributedError.invalid("\(configuration.backend) 後端不可用；請檢查建置版本、macOS 與 RDMA 設定。")
        }
        let deadline = DistributedDeadline(seconds: configuration.startupTimeout, operation: "初始化")
        defer { withExtendedLifetime(deadline) {} }
        guard let handle = tanpopo_distributed_init(configuration.backend) else {
            throw DistributedError.invalid(String(cString: tanpopo_distributed_error()))
        }
        let actualRank = Int(tanpopo_distributed_rank(handle))
        let actualSize = Int(tanpopo_distributed_size(handle))
        guard actualRank == rank, actualSize == configuration.nodes.count else {
            tanpopo_distributed_free(handle)
            throw DistributedError.invalid("實際 Rank／群組大小與設定不符；禁止退回單機。")
        }
        self.handle = handle
        self.rank = actualRank
        self.size = actualSize
    }

    deinit { tanpopo_distributed_free(handle) }

    func sum(_ input: MLXArray) throws -> MLXArray { try collective(input, gather: false) }
    func gather(_ input: MLXArray) throws -> MLXArray { try collective(input, gather: true) }

    private func collective(_ input: MLXArray, gather: Bool) throws -> MLXArray {
        // 先完成 Metal 運算，再進入 CPU 通訊 stream，避免 lazy graph 反向取得其他鎖。
        try withError {
            eval(input)
            var result = mlx_array_new()
            let code = gather ? tanpopo_distributed_gather(handle, &result, input.ctx)
                : tanpopo_distributed_sum(handle, &result, input.ctx)
            let array = MLXArray(result)
            guard code == 0 else { throw DistributedError.invalid(String(cString: tanpopo_distributed_error())) }
            eval(array)
            return array
        }
    }

    func verifyDigest(_ digest: [UInt8], label: String) throws {
        let combined = try gather(MLXArray(digest)).asArray(UInt8.self)
        guard combined.count == digest.count * size,
            (0..<size).allSatisfy({ Array(combined[($0 * digest.count)..<(($0 + 1) * digest.count)]) == digest }) else {
            throw DistributedError.invalid("節點的\(label)不一致。")
        }
    }
}
