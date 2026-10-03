import Foundation
import CryptoKit
import Cmlx
import MLX
import MLXNN
import MLXLMCommon
import MLXDistributedBridge
import Darwin

final class DistributedTensorSession: @unchecked Sendable {
    let group: DistributedGroup
    let configuration: DistributedConfiguration
    private let lock = NSLock()
    private var layers: [Linear] = []
    private var outputSizes: [Int] = []
    private(set) var originalLinearBytes = 0
    private(set) var localLinearBytes = 0
    private(set) var replicatedBytes = 0
    private let headerSize = 16
    private var heartbeat: DispatchSourceTimer?
    private static let dtypes: [DType] = [.float32, .float16, .bfloat16]

    init(group: DistributedGroup, configuration: DistributedConfiguration) {
        self.group = group
        self.configuration = configuration
    }
    deinit { heartbeat?.cancel() }

    func startHeartbeat() {
        guard group.rank == 0, heartbeat == nil else { return }
        let timer = DispatchSource.makeTimerSource(queue: .global(qos: .utility))
        timer.schedule(deadline: .now() + 10, repeating: 10)
        timer.setEventHandler { [weak self] in
            do { try self?.health() } catch { Self.failGroup(error) }
        }
        timer.resume()
        heartbeat = timer
    }

    var summary: [String: Any] {
        ["backend": configuration.backend, "rank": group.rank, "world_size": group.size,
         "strategy": "linear-output-sharding", "sharded_layers": layers.count,
         "original_linear_bytes": originalLinearBytes, "local_linear_bytes": localLinearBytes,
         "coordinator_replicated_bytes": replicatedBytes, "kv_cache_location": "rank0",
         "specialized_operations": "rank0", "direct_weight_access": "lazy-full-weight-on-rank0",
         "weight_loading": "direct-row-read", "hardware_validation": "experimental"]
    }

    /// 必須在模型首次 eval 前執行，避免先將整份大模型搬入裝置記憶體。
    func prepareModel(_ model: BaseLanguageModel) throws -> Bool {
        try install(on: model)
        // 已只求值本地分片與主節點的常駐參數。完整 Linear 權重維持 lazy，
        // 供架構直接讀取 weight 的特殊運算使用，不能再由載入器 eval(model)。
        return false
    }

    private func shardableLinear(_ module: Module) -> Linear? {
        guard let linear = module as? Linear,
            type(of: linear) == Linear.self || type(of: linear) == QuantizedLinear.self,
            linear.weight.ndim == 2, linear.shape.0 >= group.size,
            // sanitize 可能產生轉置／合併後的 lazy 權重；無法直接按列讀取時
            // 留在主節點，不能為了切分先在每個 worker 實體化完整矩陣。
            linear.parameters().flattened().allSatisfy({ tanpopo_distributed_can_copy_rows($0.1.ctx) })
            else { return nil }
        return linear
    }

    func install(on model: Module) throws {
        guard layers.isEmpty else { throw DistributedError.invalid("同一群組不能重複載入模型。") }
        let parameters = model.parameters().flattened()
        let totalBytes = parameters.reduce(0) { $0 + $1.1.nbytes }
        let candidates = model.leafModules().flattened().sorted { $0.0 < $1.0 }
        let eligibleBytes = candidates.reduce(0) { total, item in
            guard let linear = shardableLinear(item.1) else { return total }
            return total + linear.parameters().flattened().reduce(0) { $0 + $1.1.nbytes }
        }
        let physical = Int(ProcessInfo.processInfo.physicalMemory)
        let budget = min(Memory.memoryLimit, physical - max(2 * 1024 * 1024 * 1024, physical / 10))
        let shardBytes = candidates.reduce(0) { total, item in
            guard let linear = shardableLinear(item.1) else { return total }
            let bytes = linear.parameters().flattened().reduce(0) { $0 + $1.1.nbytes }
            return total + bytes / linear.shape.0 * Self.rowRange(output: linear.shape.0, rank: group.rank, size: group.size).count
        }
        let expected = shardBytes + (group.rank == 0 ? totalBytes - eligibleBytes : 0)
        guard expected + 768 * 1024 * 1024 < budget else {
            throw DistributedError.invalid("Rank \(group.rank) 的分片與通訊暫存超出本機記憶體預算。")
        }
        var updates: [(String, Module)] = []
        var signatures: [String] = []
        var shardedPaths: [String] = []
        for (path, module) in candidates {
            // 自訂子類別可能更改運算語意，保留在主節點，不假定其等同矩陣乘法。
            guard let linear = shardableLinear(module) else { continue }
            let (output, _) = linear.shape
            // 沿輸出列分攤餘數，不切斷列內的量化分組；通訊時才補齊長度。
            guard output >= group.size else { continue }
            let rows = Self.rowRange(output: output, rank: group.rank, size: group.size)
            let local: Linear
            if let quantized = linear as? QuantizedLinear {
                local = QuantizedLinear(
                    weight: try Self.copyRows(quantized.weight, rows: rows),
                    bias: try quantized.bias.map { try Self.copyRows($0, rows: rows) },
                    scales: try Self.copyRows(quantized.scales, rows: rows),
                    biases: try quantized.biases.map { try Self.copyRows($0, rows: rows) },
                    groupSize: quantized.groupSize, bits: quantized.bits, mode: quantized.mode)
            } else {
                local = Linear(weight: try Self.copyRows(linear.weight, rows: rows),
                    bias: try linear.bias.map { try Self.copyRows($0, rows: rows) })
            }
            let identifier = layers.count
            layers.append(local)
            outputSizes.append(output)
            originalLinearBytes += linear.parameters().flattened().reduce(0) { $0 + $1.1.nbytes }
            localLinearBytes += local.parameters().flattened().reduce(0) { $0 + $1.1.nbytes }
            signatures.append("\(path):\(linear.shape):\(linear.weight.dtype):\(String(describing: type(of: linear)))")
            shardedPaths.append(path + ".")
            if group.rank == 0 {
                let replacement: Linear
                if let quantized = linear as? QuantizedLinear {
                    replacement = DistributedQuantizedLinear(original: quantized, identifier: identifier, session: self)
                } else {
                    replacement = DistributedLinear(original: linear, identifier: identifier, session: self)
                }
                updates.append((path, replacement))
            }
        }
        guard !layers.isEmpty else { throw DistributedError.invalid("模型沒有可切分的線性層。") }
        replicatedBytes = totalBytes - originalLinearBytes
        let deadline = DistributedDeadline(seconds: configuration.startupTimeout, operation: "切分計畫核對")
        defer { withExtendedLifetime(deadline) {} }
        try group.verifyDigest(Array(SHA256.hash(data: Data(signatures.joined(separator: "\n").utf8))), label: "模型切分計畫")
        if group.rank == 0 {
            try model.update(modules: ModuleChildren.unflattened(updates), verify: [.all])
            // 保留完整形狀、量化型別與 lazy 參數，讓融合運算直接讀 weight 時仍正確。
            // 一般 forward 僅使用 session 的分片；特殊讀取才在主節點實體化完整權重。
            let resident = parameters.filter { item in !shardedPaths.contains(where: item.0.hasPrefix) }.map(\.1)
            try withError { eval(resident) }
        }
        fputs("TANPOPO_DISTRIBUTED rank=\(group.rank) backend=\(configuration.backend) layers=\(layers.count) local_linear_bytes=\(localLinearBytes)\n", stderr)
    }

    private static func copyRows(_ input: MLXArray, rows: Range<Int>) throws -> MLXArray {
        try withError {
            var context = mlx_array_new()
            let code = tanpopo_distributed_copy_rows(&context, input.ctx, Int32(rows.lowerBound), Int32(rows.upperBound))
            let result = MLXArray(context)
            guard code == 0 else { throw DistributedError.invalid(String(cString: tanpopo_distributed_error())) }
            eval(result)
            return result
        }
    }

    /// 主節點的多個請求共用此序列化通訊入口；worker 不持有請求／KV／抽樣狀態。
    func execute(layer: Int, input: MLXArray) -> MLXArray {
        // 請求本身的限制在通訊開始前回報給現有 MLX 錯誤處理器，不破壞群組。
        guard (1...8).contains(input.ndim), let dtype = Self.dtypes.firstIndex(of: input.dtype),
            input.shape.allSatisfy({ $0 > 0 && $0 <= Int(Int32.max) }), input.nbytes <= 256 * 1024 * 1024 else {
            tanpopo_distributed_raise("分散式 Linear 輸入型別／形狀不支援或超過 256 MiB。")
            return MLXArray.zeros([1])
        }
        return lock.withLock {
            do {
                let detail = "Rank \(group.rank) layer=\(layer) shape=\(input.shape) dtype=\(input.dtype)"
                let deadline = DistributedDeadline(seconds: configuration.operationTimeout, operation: "\(detail) 運算交握")
                defer { withExtendedLifetime(deadline) {} }
                var header = [Int32](repeating: 0, count: headerSize)
                header[0] = 1
                header[1] = Int32(layer)
                header[2] = Int32(input.ndim)
                header[3] = Int32(dtype)
                for (index, dimension) in input.shape.enumerated() { header[4 + index] = Int32(dimension) }
                _ = try group.sum(MLXArray(header))
                deadline.update("\(detail) 輸入交換")
                let shared = try group.sum(input)
                deadline.update("\(detail) 線性運算／輸出匯集")
                return try gatherOutput(layers[layer](shared), layer: layer)
            } catch {
                // 已進入 collective 後不能安全地讓任一 Rank 單獨跳過運算。
                Self.failGroup(error)
            }
        }
    }

    private static func rowRange(output: Int, rank: Int, size: Int) -> Range<Int> {
        let base = output / size, remainder = output % size
        let start = rank * base + min(rank, remainder)
        return start..<(start + base + (rank < remainder ? 1 : 0))
    }

    private func gatherOutput(_ output: MLXArray, layer: Int) throws -> MLXArray {
        // all_gather 要求各 Rank 形狀一致；補齊各分片後按原順序去掉暫存列。
        let total = outputSizes[layer], stride = (total + group.size - 1) / group.size
        var transposed = output.swappedAxes(0, -1)
        if transposed.shape[0] < stride {
            var paddingShape = transposed.shape
            paddingShape[0] = stride - transposed.shape[0]
            transposed = concatenated([transposed, MLXArray.zeros(paddingShape, dtype: output.dtype)], axis: 0)
        }
        let combined = try group.gather(transposed)
        if total % group.size == 0 { return combined.swappedAxes(0, -1) }
        let chunks = (0..<group.size).map { rank in
            let count = Self.rowRange(output: total, rank: rank, size: group.size).count
            return combined[(rank * stride)..<(rank * stride + count)]
        }
        return concatenated(chunks, axis: 0).swappedAxes(0, -1)
    }

    func workerLoop() throws {
        guard group.rank != 0 else { throw DistributedError.invalid("主節點不能進入 worker loop。") }
        while true {
            let lease = DistributedDeadline(seconds: max(60, configuration.operationTimeout * 2), operation: "主節點心跳")
            let header = try group.sum(MLXArray.zeros([headerSize], dtype: .int32)).asArray(Int32.self)
            lease.cancel()
            let deadline = DistributedDeadline(seconds: configuration.operationTimeout, operation: "Worker 張量運算")
            defer { withExtendedLifetime(deadline) {} }
            switch header[0] {
            case 0: return
            case 2:
                _ = try group.sum(MLXArray(Int32(1)))
            case 1:
                let identifier = Int(header[1]), dimensions = Int(header[2]), dtype = Int(header[3])
                guard layers.indices.contains(identifier), (1...8).contains(dimensions), Self.dtypes.indices.contains(dtype) else {
                    throw DistributedError.invalid("Worker 收到無效的運算描述。")
                }
                let shape = header[4..<(4 + dimensions)].map(Int.init)
                var elements = 1
                for dimension in shape {
                    guard dimension > 0, elements <= (64 * 1024 * 1024) / dimension else {
                        throw DistributedError.invalid("Worker 張量大小超限。")
                    }
                    elements *= dimension
                }
                let detail = "Rank \(group.rank) layer=\(identifier) shape=\(shape) dtype=\(Self.dtypes[dtype])"
                deadline.update("\(detail) 輸入交換")
                let input = try group.sum(MLXArray.zeros(shape, dtype: Self.dtypes[dtype]))
                deadline.update("\(detail) 線性運算／輸出匯集")
                let output = try gatherOutput(layers[identifier](input), layer: identifier)
                eval(output)
            default: throw DistributedError.invalid("Worker 收到未知指令。")
            }
        }
    }

    func health() throws {
        try lock.withLock {
            let deadline = DistributedDeadline(seconds: configuration.operationTimeout, operation: "健康檢查")
            defer { withExtendedLifetime(deadline) {} }
            var command = [Int32](repeating: 0, count: headerSize)
            command[0] = 2
            _ = try group.sum(MLXArray(command))
            guard try group.sum(MLXArray(Int32(1))).item(Int32.self) == group.size else {
                throw DistributedError.invalid("Worker 健康檢查失敗。")
            }
        }
    }

    func shutdown() throws {
        heartbeat?.cancel()
        try lock.withLock {
            let deadline = DistributedDeadline(seconds: configuration.operationTimeout, operation: "停止")
            defer { withExtendedLifetime(deadline) {} }
            _ = try group.sum(MLXArray.zeros([headerSize], dtype: .int32))
        }
    }

    static func failGroup(_ error: Error) -> Never {
        fputs("分散式群組失敗：\(error.localizedDescription)\n", stderr)
        _exit(70)
    }

    static func modelDigest(directory: URL) throws -> [UInt8] {
        // FileManager 列舉會展開 /var → /private/var；先正規化根目錄，
        // 才不會把模型資料夾名稱的一部分誤算進相對檔名摘要。
        guard let canonical = realpath(directory.path, nil) else {
            throw DistributedError.invalid("無法解析模型目錄的實際位置。")
        }
        let directory = URL(fileURLWithPath: String(cString: canonical), isDirectory: true)
        free(canonical)
        var hash = SHA256()
        hash.update(data: Data("\(ServerConfiguration.version):linear-output-sharding-v3".utf8))
        func appendFile(_ file: URL) throws {
            let handle = try FileHandle(forReadingFrom: file)
            defer { try? handle.close() }
            // 校驗大型模型不應把整份檔案留在 OS 檔案快取。
            _ = fcntl(handle.fileDescriptor, F_NOCACHE, 1)
            while let chunk = try handle.read(upToCount: 8 * 1024 * 1024), !chunk.isEmpty { hash.update(data: chunk) }
        }
        guard let executable = Bundle.main.executableURL else {
            throw DistributedError.invalid("無法取得 Runtime 執行檔以核對版本。")
        }
        try appendFile(executable)
        guard let iterator = FileManager.default.enumerator(at: directory, includingPropertiesForKeys: [.isRegularFileKey]) else {
            throw DistributedError.invalid("無法讀取模型目錄。")
        }
        let files = iterator.compactMap { $0 as? URL }.filter { file in
            let relative = String(file.path.dropFirst(directory.path.count + 1))
            return !relative.split(separator: "/").contains(where: { $0.hasPrefix(".") })
                && ["safetensors", "json", "model", "jinja", "txt", "tiktoken"].contains(file.pathExtension)
        }.sorted { $0.path < $1.path }
        guard files.contains(where: { $0.pathExtension == "safetensors" }) else {
            throw DistributedError.invalid("模型目錄中沒有 safetensors。")
        }
        for file in files {
            let relative = String(file.path.dropFirst(directory.path.count))
            hash.update(data: Data(relative.utf8)); hash.update(data: Data([0]))
            try appendFile(file)
        }
        return Array(hash.finalize())
    }
}

private final class DistributedLinear: Linear {
    private let identifier: Int
    private let session: DistributedTensorSession
    init(original: Linear, identifier: Int, session: DistributedTensorSession) {
        self.identifier = identifier
        self.session = session
        super.init(weight: original.weight, bias: original.bias)
    }
    override func callAsFunction(_ input: MLXArray) -> MLXArray {
        session.execute(layer: identifier, input: input)
    }
}

private final class DistributedQuantizedLinear: QuantizedLinear {
    private let identifier: Int
    private let session: DistributedTensorSession
    init(original: QuantizedLinear, identifier: Int, session: DistributedTensorSession) {
        self.identifier = identifier
        self.session = session
        super.init(weight: original.weight, bias: original.bias, scales: original.scales,
            biases: original.biases, groupSize: original.groupSize, bits: original.bits, mode: original.mode)
    }
    override func callAsFunction(_ input: MLXArray) -> MLXArray {
        session.execute(layer: identifier, input: input)
    }
}
