import Foundation
import Cmlx
import MLX
import MLXNN
import MLXLMCommon
import MLXDistributedBridge

/// 與架構名稱無關的檔案權重執行器。權重快取、常駐參數與運算暫存分開計算。
/// 快取限制不包含 KV、輸出、Metal 暫存與作業系統的檔案頁面快取。
final class DiskOffloadSession: @unchecked Sendable {
    static let mib = 1_024 * 1_024
    let cacheBytes: Int
    let tileBytes: Int
    let runtimeBytes: Int
    private let execution = NSRecursiveLock()
    private let statistics = NSLock()
    private var modelStatistics: [String: Int] = [:]
    private let cache: DiskTileCache
    private var sourceCount = 0
    private(set) var offloadedBytes = 0
    private(set) var residentBytes = 0

    init(cacheBytes: Int, tileBytes: Int, runtimeBytes: Int) throws {
        guard tileBytes > 0, tileBytes < Int(Int32.max), cacheBytes >= tileBytes * 2,
            runtimeBytes > cacheBytes + 512 * Self.mib else {
            throw DiskOffloadError.invalid("磁碟快取至少需容納兩個區塊，且需保留 512 MiB 以上的運算空間。")
        }
        self.cacheBytes = cacheBytes
        self.tileBytes = tileBytes
        self.runtimeBytes = runtimeBytes
        self.cache = DiskTileCache(limit: cacheBytes)
    }

    var summary: [String: Any] {
        let model = statistics.withLock { modelStatistics }
        var value = cache.summary
        value["mode"] = "disk-row-streaming"
        value["tile_bytes"] = tileBytes
        for (key, count) in model { value[key] = count }
        return value
    }

    func prepareModel(_ model: BaseLanguageModel) throws -> Bool {
        try install(on: model)
        return false
    }

    /// 必須在首次 eval 前安裝。無法按列讀取的參數只允許在常駐預算內載入。
    func install(on model: Module) throws {
        try execution.withLock {
            let parameters = model.parameters().flattened()
            var managed = Set<String>()
            var updates: [(String, Module)] = []
            var switches: [(SwitchLinear, DiskWeightSource)] = []
            for (path, module) in model.leafModules().flattened().sorted(by: { $0.0 < $1.0 }) {
                let supported = type(of: module) == Linear.self || type(of: module) == QuantizedLinear.self
                    || type(of: module) == Embedding.self || type(of: module) == QuantizedEmbedding.self
                    || type(of: module) == SwitchLinear.self || type(of: module) == QuantizedSwitchLinear.self
                guard supported else { continue }
                let values = Dictionary(uniqueKeysWithValues: module.parameters().flattened())
                let tensors = values.filter { ["weight", "scales", "biases"].contains($0.key) }
                guard let weight = tensors["weight"], weight.ndim == (module is SwitchLinear ? 3 : 2),
                    tensors.values.allSatisfy({ $0.shape.allSatisfy({ $0 > 0 }) }),
                    tensors.values.allSatisfy({ tanpopo_disk_is_file_tensor($0.ctx) }) else { continue }
                let rows = weight.size / weight.dim(-1)
                guard tensors.values.allSatisfy({ $0.size / $0.dim(-1) == rows }),
                    tensors.values.reduce(0, { $0 + $1.dim(-1) * $1.dtype.size }) <= tileBytes else {
                    throw DiskOffloadError.invalid("\(path) 的單列權重超過區塊預算或量化形狀不相容。")
                }
                let source = DiskWeightSource(id: sourceCount, tensors: tensors, quantized: module as? Quantized)
                sourceCount += 1
                for name in tensors.keys { managed.insert(path + "." + name) }
                offloadedBytes += tensors.values.reduce(0) { $0 + $1.nbytes }
                switch module {
                case let layer as QuantizedLinear:
                    updates.append((path, try DiskQuantizedLinear(original: layer, source: source, session: self)))
                case let layer as Linear:
                    updates.append((path, try DiskLinear(original: layer, source: source, session: self)))
                case let layer as QuantizedEmbedding:
                    updates.append((path, try DiskQuantizedEmbedding(original: layer, source: source, session: self)))
                case let layer as Embedding:
                    updates.append((path, try DiskEmbedding(original: layer, source: source, session: self)))
                case let layer as SwitchLinear:
                    switches.append((layer, source))
                default: break
                }
            }
            let resident = parameters.filter { !managed.contains($0.0) }
            let additional = resident.reduce(0) { $0 + $1.1.nbytes }
            var loadingBytes = 0
            for (_, value) in resident {
                var graphBytes = 0
                guard tanpopo_disk_resident_graph_bytes(value.ctx, &graphBytes) == 0, graphBytes >= 0,
                    graphBytes <= runtimeBytes - min(loadingBytes, runtimeBytes) else {
                    throw DiskOffloadError.invalid("常駐參數的來源運算圖超出磁碟模式預算；請使用已正規化的 Fast GGUF 權重。")
                }
                loadingBytes += graphBytes
            }
            guard residentBytes + loadingBytes + cacheBytes + 512 * Self.mib < runtimeBytes else {
                let largest = resident.sorted { $0.1.nbytes > $1.1.nbytes }.prefix(3).map(\.0).joined(separator: ", ")
                throw DiskOffloadError.invalid("未能分塊的常駐參數超出預算：\(largest)。請使用可直接按列讀取的 safetensors／Fast GGUF；不會回退成全量載入。")
            }
            try model.update(modules: ModuleChildren.unflattened(updates), verify: [.all])
            for (layer, source) in switches {
                let bias = layer.parameters().flattened().first { $0.0 == "bias" }?.1
                try layer.update(parameters: ModuleParameters.unflattened(try source.guarded()), verify: [.noUnusedKeys])
                layer.inferenceOverride = { [self] input, indices, _ in
                    execute { try self.experts(input, indices: indices, source: source, bias: bias) }
                }
            }
            try withError { eval(resident.map(\.1)) }
            residentBytes += additional
            statistics.withLock {
                modelStatistics = ["offloaded_bytes": offloadedBytes, "resident_parameter_bytes": residentBytes, "modules": sourceCount]
            }
            Memory.clearCache()
            fputs("TANPOPO_DISK_OFFLOAD modules=\(sourceCount) offloaded_bytes=\(offloadedBytes) resident_bytes=\(residentBytes) cache_bytes=\(cacheBytes) tile_bytes=\(tileBytes)\n", stderr)
        }
    }

    fileprivate func execute(_ operation: () throws -> MLXArray) -> MLXArray {
        execution.withLock {
            do { return try withError(operation) }
            catch {
                tanpopo_distributed_raise("磁碟輔助推論失敗：\(error.localizedDescription)")
                return MLXArray.zeros([1])
            }
        }
    }

    fileprivate func linear(_ input: MLXArray, source: DiskWeightSource, expert: Int = 0, bias: MLXArray? = nil) throws -> MLXArray {
        let outputRows = source.outputRows
        let step = max(1, tileBytes / source.rowBytes)
        var outputs: [MLXArray] = []
        for start in stride(from: 0, to: outputRows, by: step) {
            let count = min(step, outputRows - start)
            let offset = expert * outputRows + start
            let tile = try cache.acquire(source, start: offset, count: count)
            defer { cache.release(tile.key) }
            if start + count < outputRows {
                cache.prefetch(source, start: offset + count, count: min(step, outputRows - start - count))
            }
            var output: MLXArray
            if let q = source.quantization {
                output = quantizedMM(input, tile.values["weight"]!, scales: tile.values["scales"]!,
                    biases: tile.values["biases"], transpose: true, groupSize: q.groupSize, bits: q.bits, mode: q.mode)
            } else {
                output = matmul(input, tile.values["weight"]!.T)
            }
            if let bias { output = output + bias[start..<(start + count)] }
            // 在釋放／逐出權重前完成運算；不能把整個模型的 lazy Load 留在運算圖。
            eval(output)
            outputs.append(output)
        }
        let result = concatenated(outputs, axis: -1)
        eval(result)
        return result
    }

    fileprivate func embedding(_ indices: MLXArray, source: DiskWeightSource) throws -> MLXArray {
        guard indices.dtype.isInteger else { throw DiskOffloadError.invalid("Embedding 索引必須為整數。") }
        let ids = indices.asType(.int64).asArray(Int64.self)
        guard !ids.isEmpty else {
            return MLXArray.zeros(indices.shape + [source.inputColumns], dtype: source.outputDType)
        }
        guard ids.allSatisfy({ $0 >= 0 && $0 < Int64(source.outputRows) }) else {
            throw DiskOffloadError.invalid("Embedding 索引超出詞彙表範圍。")
        }
        let unique = Array(Set(ids)).sorted()
        let positions = Dictionary(uniqueKeysWithValues: unique.enumerated().map { ($0.element, Int32($0.offset)) })
        let step = max(1, tileBytes / source.rowBytes)
        var outputs: [MLXArray] = []
        var index = 0
        while index < unique.count {
            var end = index + 1
            while end < unique.count, end - index < step, unique[end] == unique[end - 1] + 1 { end += 1 }
            let tile = try cache.acquire(source, start: Int(unique[index]), count: end - index)
            defer { cache.release(tile.key) }
            let output: MLXArray
            if let q = source.quantization {
                output = dequantized(tile.values["weight"]!, scales: tile.values["scales"]!, biases: tile.values["biases"],
                    groupSize: q.groupSize, bits: q.bits, mode: q.mode)
            } else {
                // 複製，避免回傳的 embedding 以 view 長期保留已逐出的權重區塊。
                var context = mlx_array_new()
                mlx_copy(&context, tile.values["weight"]!.ctx, StreamOrDevice.gpu.ctx)
                output = MLXArray(context)
            }
            eval(output)
            outputs.append(output)
            index = end
        }
        let result = concatenated(outputs, axis: 0)[MLXArray(ids.map { positions[$0]! })]
            .reshaped(indices.shape + [source.inputColumns])
        eval(result)
        return result
    }

    private func experts(_ input: MLXArray, indices: MLXArray, source: DiskWeightSource, bias: MLXArray?) throws -> MLXArray {
        guard indices.dtype.isInteger else { throw DiskOffloadError.invalid("MoE expert 索引必須為整數。") }
        let ids = indices.asType(.int64).asArray(Int64.self)
        guard !ids.isEmpty, ids.allSatisfy({ $0 >= 0 && $0 < Int64(source.experts) }) else {
            throw DiskOffloadError.invalid("MoE expert 索引無效。")
        }
        let matrixRows = input.dim(-2)
        let expanded = broadcast(input, to: indices.shape + [matrixRows, input.dim(-1)])
            .reshaped([ids.count, matrixRows, input.dim(-1)])
        eval(expanded)
        let groups = Dictionary(grouping: ids.indices, by: { ids[$0] })
        var outputGroups: [MLXArray] = []
        var order: [Int32] = []
        for expert in groups.keys.sorted() {
            let positions = groups[expert]!
            let selected = expanded[MLXArray(positions.map(Int32.init))]
            let output = try linear(selected, source: source, expert: Int(expert), bias: bias.map { $0[Int(expert)] })
            outputGroups.append(output)
            order.append(contentsOf: positions.map(Int32.init))
        }
        let result = concatenated(outputGroups, axis: 0)[argSort(MLXArray(order))]
            .reshaped(indices.shape + [matrixRows, source.outputRows])
        eval(result)
        return result
    }
}

enum DiskOffloadError: LocalizedError {
    case invalid(String)
    var errorDescription: String? { if case .invalid(let message) = self { return message }; return nil }
}

/// 不是 Module，避免反射把完整檔案權重加入 eval(model)。
private final class DiskWeightSource: @unchecked Sendable {
    struct Quantization { let groupSize: Int; let bits: Int; let mode: QuantizationMode }
    let id: Int
    let tensors: [String: MLXArray]
    let quantization: Quantization?
    let rowBytes: Int
    let outputRows: Int
    let inputColumns: Int
    let experts: Int
    let outputDType: DType

    init(id: Int, tensors: [String: MLXArray], quantized: Quantized?) {
        self.id = id
        // Module.update 會原地改寫 MLXArray；獨立 context 才能保留原始 Load。
        self.tensors = tensors.mapValues { source in
            var context = mlx_array_new()
            mlx_array_set(&context, source.ctx)
            return MLXArray(context)
        }
        quantization = quantized.map { Quantization(groupSize: $0.groupSize, bits: $0.bits, mode: $0.mode) }
        let weight = tensors["weight"]!
        rowBytes = tensors.values.reduce(0) { $0 + $1.dim(-1) * $1.dtype.size }
        outputRows = weight.dim(-2)
        inputColumns = quantized.map { weight.dim(-1) * 32 / $0.bits } ?? weight.dim(-1)
        experts = weight.ndim == 3 ? weight.dim(0) : 1
        outputDType = tensors["scales"]?.dtype ?? weight.dtype
    }
    func guarded() throws -> [(String, MLXArray)] { try tensors.map { ($0.key, try Self.guardArray($0.value)) } }
    static func guardArray(_ source: MLXArray) throws -> MLXArray {
        var context = mlx_array_new()
        let code = tanpopo_disk_guard(&context, source.ctx)
        let result = MLXArray(context)
        guard code == 0 else { throw DiskOffloadError.invalid(String(cString: tanpopo_distributed_error())) }
        return result
    }
    func read(start: Int, count: Int) throws -> [String: MLXArray] {
        try withError {
            let values = try tensors.mapValues { source in
                var context = mlx_array_new()
                let code = tanpopo_disk_read_rows(&context, source.ctx, start, Int32(count))
                let result = MLXArray(context)
                guard code == 0 else { throw DiskOffloadError.invalid(String(cString: tanpopo_distributed_error())) }
                return result
            }
            eval(Array(values.values))
            return values
        }
    }
}

/// 唯一的序列化 I/O 佇列；預取與一般讀取共用預算，正在計算的區塊不可逐出。
private final class DiskTileCache: @unchecked Sendable {
    struct Tile { let key: String; let values: [String: MLXArray] }
    private struct Entry { let tile: Tile; let bytes: Int; var used: UInt64; var pinned: Bool }
    private let queue = DispatchQueue(label: "tanpopo.disk-weight-cache", qos: .userInitiated)
    private let limit: Int
    private var entries: [String: Entry] = [:]
    private var bytes = 0
    private var peak = 0
    private var clock: UInt64 = 0
    private var hits = 0
    private var misses = 0
    private var readBytes = 0
    init(limit: Int) { self.limit = limit }
    var summary: [String: Any] {
        queue.sync { ["cache_limit_bytes": limit, "cache_used_bytes": bytes, "cache_peak_bytes": peak,
                      "cache_hits": hits, "cache_misses": misses, "read_bytes": readBytes] }
    }
    private func key(_ source: DiskWeightSource, _ start: Int, _ count: Int) -> String { "\(source.id):\(start):\(count)" }
    func acquire(_ source: DiskWeightSource, start: Int, count: Int) throws -> Tile {
        try queue.sync {
            let key = key(source, start, count)
            if entries[key] != nil { hits += 1 } else {
                misses += 1
                try load(source, start: start, count: count)
            }
            clock &+= 1
            entries[key]!.used = clock
            entries[key]!.pinned = true
            return entries[key]!.tile
        }
    }
    func release(_ key: String) { queue.sync { entries[key]?.pinned = false } }
    func prefetch(_ source: DiskWeightSource, start: Int, count: Int) {
        queue.async { [self] in
            // 預取失敗由下一次正式 acquire 重試並回報，不將錯誤當成有效快取。
            if entries[key(source, start, count)] == nil { try? load(source, start: start, count: count) }
        }
    }
    private func load(_ source: DiskWeightSource, start: Int, count: Int) throws {
        let required = count * source.rowBytes
        guard required <= limit else { throw DiskOffloadError.invalid("單一權重區塊超過快取上限。") }
        // 小型 embedding 列也限制筆數，避免索引／LRU metadata 無限制成長。
        while bytes + required > limit || entries.count >= 4096 {
            guard let oldest = entries.filter({ !$0.value.pinned }).min(by: { $0.value.used < $1.value.used }) else {
                throw DiskOffloadError.invalid("權重區塊仍在使用，無法滿足快取預算。")
            }
            bytes -= oldest.value.bytes
            entries.removeValue(forKey: oldest.key)
        }
        // I/O 序列執行，此處預留的 required 不會被其他載入超額使用。
        let values = try source.read(start: start, count: count)
        let key = key(source, start, count)
        clock &+= 1
        entries[key] = Entry(tile: Tile(key: key, values: values), bytes: required, used: clock, pinned: false)
        bytes += required
        peak = max(peak, bytes)
        readBytes += required
    }
}

private final class DiskLinear: Linear {
    private let source: DiskWeightSource
    private let session: DiskOffloadSession
    init(original: Linear, source: DiskWeightSource, session: DiskOffloadSession) throws {
        self.source = source; self.session = session
        super.init(weight: try DiskWeightSource.guardArray(original.weight), bias: original.bias)
    }
    override func callAsFunction(_ x: MLXArray) -> MLXArray {
        session.execute { try session.linear(x, source: source, bias: bias) }
    }
}
private final class DiskQuantizedLinear: QuantizedLinear {
    private let source: DiskWeightSource
    private let session: DiskOffloadSession
    init(original: QuantizedLinear, source: DiskWeightSource, session: DiskOffloadSession) throws {
        self.source = source; self.session = session
        super.init(weight: try DiskWeightSource.guardArray(original.weight), bias: original.bias,
            scales: try DiskWeightSource.guardArray(original.scales), biases: try original.biases.map(DiskWeightSource.guardArray),
            groupSize: original.groupSize, bits: original.bits, mode: original.mode)
    }
    override func callAsFunction(_ x: MLXArray) -> MLXArray {
        session.execute { try session.linear(x, source: source, bias: bias) }
    }
}
private final class DiskEmbedding: Embedding {
    private let source: DiskWeightSource
    private let session: DiskOffloadSession
    init(original: Embedding, source: DiskWeightSource, session: DiskOffloadSession) throws {
        self.source = source; self.session = session
        super.init(weight: try DiskWeightSource.guardArray(original.weight))
    }
    override func callAsFunction(_ x: MLXArray) -> MLXArray { session.execute { try session.embedding(x, source: source) } }
    override func asLinear(_ x: MLXArray) -> MLXArray { session.execute { try session.linear(x, source: source) } }
}
private final class DiskQuantizedEmbedding: QuantizedEmbedding {
    private let source: DiskWeightSource
    private let session: DiskOffloadSession
    init(original: QuantizedEmbedding, source: DiskWeightSource, session: DiskOffloadSession) throws {
        self.source = source; self.session = session
        // 上游沒有 raw quantized initializer；只建立一列占位，隨後替換 metadata。
        super.init(weight: MLXArray.zeros([1, original.groupSize]), groupSize: original.groupSize, bits: original.bits, mode: original.mode)
        try update(parameters: ModuleParameters.unflattened(try source.guarded()), verify: [.noUnusedKeys, .allModelKeysSet])
    }
    override func callAsFunction(_ x: MLXArray) -> MLXArray { session.execute { try session.embedding(x, source: source) } }
    override func asLinear(_ x: MLXArray) -> MLXArray { session.execute { try session.linear(x, source: source) } }
}
