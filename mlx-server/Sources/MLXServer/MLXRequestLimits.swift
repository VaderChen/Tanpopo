import Foundation
import MLX
import MLXLMCommon

/// 依模型 metadata 與裝置資源估算；不以模型名稱建立例外清單。
struct MLXRequestLimits: Sendable {
    private let contextLimit: Int?
    private let attentionHeads: Double
    private let kvElementsPerToken: Double
    private let vocabularySize: Double
    private let intermediateSize: Double
    let memoryLimit: Int
    private let maximumBufferBytes: Int
    private let resourceSampling = MLXRequestResourceSampling()

    init(directory: URL, configuration: ServerConfiguration, memoryMapPlan: MLXMemoryMapPlan?) {
        let root =
            (try? Data(contentsOf: directory.appendingPathComponent("config.json")))
            .flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:]
        let text = root["text_config"] as? [String: Any] ?? root
        func positive(_ key: String) -> Int? {
            guard let value = (text[key] ?? root[key]) as? NSNumber,
                value.doubleValue > 0, value.doubleValue < Double(Int.max)
            else { return nil }
            return value.intValue
        }
        contextLimit = [configuration.maxKVSize, positive("max_position_embeddings")]
            .compactMap { $0 }.filter { $0 > 0 }.min()
        let heads = positive("num_attention_heads") ?? 32
        let kvHeads = positive("num_key_value_heads") ?? heads
        let hidden = positive("hidden_size") ?? 4096
        // 部分混合／滑動視窗模型有獨立的全域 attention head dimension。
        let headDim = max(
            positive("head_dim") ?? max(1, hidden / heads),
            positive("global_head_dim") ?? 0)
        let layers = positive("num_hidden_layers") ?? 32
        let kvLayers: Int
        if let types = text["layer_types"] as? [String], types.count == layers {
            kvLayers = types.filter { !$0.contains("linear") && !$0.contains("mamba") }.count
        } else if let interval = positive("full_attention_interval") {
            kvLayers = max(1, layers / interval)
        } else {
            kvLayers = layers
        }
        attentionHeads = Double(heads)
        // Prefill 期間未必已量化 cache，不能用 Q4 的大小低估所需記憶體。
        kvElementsPerToken = Double(kvHeads) * Double(headDim) * Double(kvLayers) * 2
        vocabularySize = Double(positive("vocab_size") ?? 262144)
        intermediateSize = positive("intermediate_size").map(Double.init) ?? Double(hidden) * 4
        let physical = Int(min(UInt64(Int.max), ProcessInfo.processInfo.physicalMemory))
        let reserve = max(2 * 1024 * 1024 * 1024, physical / 10)
        let device = GPU.deviceInfo()
        memoryLimit = max(
            1,
            min(
                memoryMapPlan?.runtimeLimitBytes ?? max(1, physical - reserve),
                Memory.memoryLimit))
        maximumBufferBytes = device.maxBufferSize > 0 ? device.maxBufferSize : memoryLimit
    }

    /// 只縮小執行分段，不變更上下文、使用者設定或效能校準紀錄。
    func plan(
        promptTokens: Int, parameters: GenerateParameters, supportsChunking: Bool,
        reservedMemoryFloor: Double, maximumConcurrentRequests: Int
    ) throws -> (parameters: GenerateParameters, reservedBytes: Double)
    {
        guard promptTokens > 0 else {
            throw APIError.invalidRequest("模型處理後的輸入不可為空。")
        }
        let outputTokens = parameters.maxTokens ?? 4096
        if let limit = contextLimit,
            promptTokens > limit || outputTokens > limit - promptTokens
        {
            throw MLXRequestError.contextExceeded(
                prompt: promptTokens, output: outputTokens, limit: limit)
        }
        let totalTokens = Double(promptTokens) + Double(outputTokens)
        let kvBytes = totalTokens * kvElementsPerToken * 4
        let active = max(Double(currentMemoryBytes()), reservedMemoryFloor)
        let safetyMargin = Double(512 * 1024 * 1024)
        let available = Double(memoryLimit) - active - kvBytes - safetyMargin
        guard available > 0 else {
            throw MLXRequestError.memoryBudget(prompt: promptTokens)
        }
        // 預留 attention scores、FP32 softmax、mask 及 MLP/logits 的暫存。
        // 即使 Metal 必須走非 fused attention，也不能配置整個 N × N 矩陣。
        let bytesPerRow =
            Double(promptTokens) * attentionHeads * 12
            + vocabularySize * 4 + intermediateSize * 16
        // 限制每個請求的暫存份額，為其他名額的 KV 與後續生成保留空間。
        let sharedScratchBudget = Double(memoryLimit) / Double(max(1, maximumConcurrentRequests)) / 4
        let transientBudget = min(available / 3, Double(maximumBufferBytes) / 2, sharedScratchBudget)
        guard bytesPerRow > 0, bytesPerRow <= transientBudget else {
            throw MLXRequestError.memoryBudget(prompt: promptTokens)
        }
        let safeRows = Int(min(Double(Int.max / 2), (transientBudget / bytesPerRow).rounded(.down)))
        let actualPrefillChunk =
            supportsChunking
            ? min(promptTokens, max(1, parameters.prefillStepSize)) : promptTokens
        var result = parameters
        if actualPrefillChunk > safeRows {
            guard supportsChunking else {
                throw MLXRequestError.memoryBudget(prompt: promptTokens)
            }
            result.prefillStepSize = max(1, min(parameters.prefillStepSize, safeRows))
        }
        let effectiveRows = supportsChunking ? min(promptTokens, result.prefillStepSize) : promptTokens
        let reservedBytes = kvBytes + Double(effectiveRows) * bytesPerRow + safetyMargin
        fputs(
            "generation request prompt_tokens=\(promptTokens) max_tokens=\(outputTokens) "
                + "prefill_step_size=\(result.prefillStepSize) "
                + "estimated_kv_mib=\(Int(kvBytes / 1_048_576)) "
                + "reserved_mib=\(Int(reservedBytes / 1_048_576)) "
                + "memory_limit_mib=\(memoryLimit / 1_048_576)\n",
            stderr)
        return (result, reservedBytes)
    }

    func checkResources() throws {
        // residentBytes 會掃描映射頁面，不可在每個 token 都重做。
        guard resourceSampling.shouldSample() else { return }
        let active = currentMemoryBytes()
        guard active < memoryLimit else {
            throw MLXRequestError.memoryPressure
        }
    }

    func currentMemoryBytes() -> Int {
        max(Memory.activeMemory, MLXMemoryMapPlan.processFootprintBytes()
            + MemoryMappedRegionRegistry.shared.residentBytes())
    }
}

private final class MLXRequestResourceSampling: @unchecked Sendable {
    private let lock = NSLock()
    private var lastSample: UInt64 = 0

    func shouldSample() -> Bool {
        lock.withLock {
            let now = DispatchTime.now().uptimeNanoseconds
            guard lastSample == 0 || now - lastSample >= 250_000_000 else { return false }
            lastSample = now
            return true
        }
    }
}

enum MLXRequestError: LocalizedError, Sendable {
    case busy(limit: Int)
    case contextExceeded(prompt: Int, output: Int, limit: Int)
    case memoryBudget(prompt: Int)
    case memoryPressure

    var status: Int {
        switch self {
        case .busy: 429
        case .memoryPressure: 503
        case .contextExceeded, .memoryBudget: 413
        }
    }

    var errorDescription: String? {
        switch self {
        case .busy(let limit):
            "模型目前已有 \(limit) 個生成請求處理中，請稍後重試。"
        case .contextExceeded(let prompt, let output, let limit):
            "輸入包含 \(prompt) tokens，加上最多 \(output) tokens 的回答，超過 \(limit) tokens 上限。請縮短上下文或降低 max_tokens；服務仍可使用。"
        case .memoryBudget(let prompt):
            "輸入包含 \(prompt) tokens，預估超過目前可安全使用的記憶體。請縮短上下文、降低 max_tokens 或改用較小的模型；未截斷輸入，服務仍可使用。"
        case .memoryPressure:
            "推論已停止：記憶體用量超過安全預算。請縮短上下文或釋放記憶體後重試；服務仍可使用。"
        }
    }
}
