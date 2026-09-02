import Foundation
import MLXHuggingFace
import MLXLLM
import MLXLMCommon
import MLXVLM
import Tokenizers

actor MLXRuntime {
    let modelDirectory: URL
    let modelID: String
    let kind: ModelKind

    private let configuration: ServerConfiguration
    private let ggufWeightURL: URL?
    private let fastGGUFManifestURL: URL?
    private let ggufMMProjURL: URL?
    private let memoryMapPlan: MLXMemoryMapPlan?
    private let modelGenerationDefaults: ModelGenerationDefaults
    private var container: ModelContainer?
    private var memoryGuard: Task<Void, Never>?
    private var dflashDrafter: (any DFlashDrafterModel)?
    private var mtpDrafterContainer: MTPDrafterContainer?

    init(configuration: ServerConfiguration) throws {
        self.configuration = configuration
        let modelURL = URL(fileURLWithPath: configuration.modelPath)
        let isFastGGUF = modelURL.lastPathComponent.lowercased().hasSuffix(".fgguf.json")
        let isGGUF = modelURL.pathExtension.lowercased() == "gguf" || isFastGGUF
        ggufWeightURL = isGGUF && !isFastGGUF ? modelURL : nil
        fastGGUFManifestURL = isFastGGUF ? modelURL : nil
        ggufMMProjURL = configuration.mmprojPath.map(URL.init(fileURLWithPath:))
        memoryMapPlan = configuration.memoryMappingEnabled
            ? try MLXMemoryMapPlan(reserveGB: configuration.mmapReserveGB)
            : nil
        let resolvedModelDirectory = isGGUF ? modelURL.deletingLastPathComponent() : modelURL
        modelDirectory = resolvedModelDirectory
        modelGenerationDefaults = ModelGenerationDefaults.load(from: resolvedModelDirectory)
        modelID = isGGUF
            ? modelURL.deletingPathExtension().lastPathComponent
            : resolvedModelDirectory.lastPathComponent
        kind = try Self.resolveModelKind(
            requested: configuration.modelKind,
            directory: resolvedModelDirectory,
            isGGUF: isGGUF,
            hasMMProj: ggufMMProjURL != nil
        )
    }

    func prepare() async throws {
        guard container == nil else { return }
        if let memoryMapPlan {
            memoryMapPlan.applyBeforeLoading()
            try await ModelWeightLoadingContext.$mode.withValue(.memoryMapped) {
                try await prepareModels()
            }
            memoryMapPlan.finishLoading()
            memoryGuard?.cancel()
            memoryGuard = memoryMapPlan.startMemoryGuard()
        } else {
            try await prepareModels()
        }
    }

    private func prepareModels() async throws {
        if !modelGenerationDefaults.isEmpty {
            fputs(modelGenerationDefaults.logDescription + "\n", stderr)
        }
        if let fastGGUFManifestURL {
            container = try await MLXGGUFModelLoader.loadFastGGUFContainer(
                from: modelDirectory,
                manifestURL: fastGGUFManifestURL,
                memoryMapped: configuration.memoryMappingEnabled
            )
        } else if let ggufWeightURL {
            switch kind {
            case .text, .auto:
                container = try await MLXGGUFModelLoader.loadContainer(
                    from: modelDirectory,
                    weightURL: ggufWeightURL,
                    quantizationGroupSize: configuration.ggufGroupSize,
                    quantizationProfile: configuration.ggufProfile,
                    recurrentPromotion: configuration.ggufRecurrentPromotion,
                    conversionCacheDirectory: configuration.ggufCacheDirectory,
                    conversionCacheEnabled: configuration.ggufCacheEnabled,
                    memoryMapped: configuration.memoryMappingEnabled
                )
            case .vision:
                guard let ggufMMProjURL else {
                    throw MLXGGUFLoaderError.missingMultimodalProjector(modelDirectory)
                }
                container = try await MLXGGUFModelLoader.loadVLMContainer(
                    from: modelDirectory,
                    weightURL: ggufWeightURL,
                    mmprojURL: ggufMMProjURL,
                    quantizationGroupSize: configuration.ggufGroupSize,
                    quantizationProfile: configuration.ggufProfile,
                    recurrentPromotion: configuration.ggufRecurrentPromotion,
                    conversionCacheDirectory: configuration.ggufCacheDirectory,
                    conversionCacheEnabled: configuration.ggufCacheEnabled,
                    memoryMapped: configuration.memoryMappingEnabled
                )
            }
            let requestedGroupSize = configuration.ggufGroupSize.map(String.init) ?? "auto"
            fputs(
                "GGUF loaded model=\(ggufWeightURL.lastPathComponent) "
                    + "profile=\(configuration.ggufProfile.rawValue) "
                    + "recurrent=\(configuration.ggufRecurrentPromotion.rawValue) "
                    + "requested_group=\(requestedGroupSize)\n",
                stderr
            )
        } else {
            let progressHandler: @Sendable (Int64, Int64) -> Void = { completed, total in
                fputs(
                    "TANPOPO_MODEL_PROGRESS phase=loading "
                        + "completed=\(completed) total=\(total) unit=steps\n",
                    stderr
                )
            }
            try await ModelWeightLoadingContext.$progressHandler.withValue(progressHandler) {
                switch kind {
                case .text, .auto:
                    container = try await LLMModelFactory.shared.loadContainer(
                        from: modelDirectory,
                        using: #huggingFaceTokenizerLoader()
                    )
                case .vision:
                    container = try await VLMModelFactory.shared.loadContainer(
                        from: modelDirectory,
                        using: #huggingFaceTokenizerLoader()
                    )
                }
            }
        }

        if let draftPath = configuration.dflashDraftPath {
            guard ggufWeightURL == nil else {
                throw DFlashError.unsupportedGeneration(
                    "GGUF Target 目前使用一般 MLX 生成；DFlash 請搭配 MLX safetensors Target。")
            }
            guard kind != .vision else {
                throw DFlashError.unsupportedGeneration(
                    "DFlash 目前只支援 language target model。")
            }
            let draftDirectory = URL(fileURLWithPath: draftPath, isDirectory: true)
            let drafter = try DFlashModelFactory.load(from: draftDirectory)
            guard let container else {
                throw APIError.invalidRequest("MLX target model 尚未載入。")
            }
            _ = try await container.perform(nonSendable: drafter) { context, drafter in
                try drafter.validate(target: context.model)
                return true
            }
            dflashDrafter = drafter
            fputs(
                "DFlash loaded variant=\(drafter.dflashDescriptor.variant.rawValue) draft=\(draftDirectory.lastPathComponent) block_size=\(min(configuration.dflashBlockSize, drafter.dflashDescriptor.blockSize))\n",
                stderr
            )
        }

        if configuration.mtpEnabled {
            await Qwen35TextMTPRegistration.register()
            await Qwen35VLMMTPRegistration.register()
            await Gemma4AssistantRegistration.register()

            let draftContainer: MTPDrafterContainer
            let draftDescription: String
            if let draftPath = configuration.mtpDraftPath {
                let draftDirectory = URL(fileURLWithPath: draftPath, isDirectory: true)
                draftContainer = try await MTPDrafterModelFactory.shared.loadContainer(
                    from: draftDirectory,
                    using: #huggingFaceTokenizerLoader())
                draftDescription = draftDirectory.lastPathComponent
            } else if let ggufWeightURL {
                draftContainer = try await MLXGGUFModelLoader.loadEmbeddedMTPDrafterContainer(
                    from: modelDirectory,
                    weightURL: ggufWeightURL,
                    mmprojURL: ggufMMProjURL,
                    quantizationGroupSize: configuration.ggufGroupSize,
                    quantizationProfile: configuration.ggufProfile,
                    recurrentPromotion: configuration.ggufRecurrentPromotion,
                    memoryMapped: configuration.memoryMappingEnabled)
                draftDescription = "embedded:\(ggufWeightURL.lastPathComponent)"
            } else {
                throw MTPCompatibilityError.incompatibleConfiguration(
                    "原生 MLX Target 必須指定 --mtp-draft；內嵌 MTP 僅適用於含預測層的 GGUF")
            }
            guard let container else {
                throw APIError.invalidRequest("MLX target model 尚未載入。")
            }
            _ = try await draftContainer.perform { draftContext in
                try await container.perform(nonSendable: draftContext.model) {
                    targetContext, drafter in
                    try drafter.validate(target: targetContext.model)
                    return true
                }
            }
            mtpDrafterContainer = draftContainer
            fputs(
                "MTP loaded draft=\(draftDescription) requested_block_size=\(configuration.mtpBlockSize)\n",
                stderr)
        }
    }

    func generate(
        messages: [InputMessage],
        options: GenerationOptions
    ) async throws -> GenerationResult {
        let stream = try await generationStream(messages: messages, options: options)
        var output = ""
        var promptTokens = 0
        var completionTokens = 0
        var tokensPerSecond = 0.0
        var finishReason = "stop"
        var toolCalls: [ToolCall] = []
        for await event in stream {
            switch event {
            case .chunk(let text):
                output += text
            case .info(let info):
                promptTokens = info.promptTokenCount
                completionTokens = info.generationTokenCount
                tokensPerSecond = info.tokensPerSecond
                switch info.stopReason {
                case .length: finishReason = "length"
                case .stop, .cancelled: finishReason = "stop"
                }
                fputs(
                    generationLog(info),
                    stderr
                )
            case .toolCall(let toolCall):
                toolCalls.append(toolCall)
                finishReason = "tool_calls"
            }
        }

        if !toolCalls.isEmpty {
            finishReason = "tool_calls"
        }

        if let stop = firstStop(in: output, candidates: options.stops) {
            output = String(output[..<stop])
            finishReason = "stop"
        }
        return GenerationResult(
            text: output,
            promptTokens: promptTokens,
            completionTokens: completionTokens,
            tokensPerSecond: tokensPerSecond,
            finishReason: finishReason,
            toolCalls: toolCalls
        )
    }

    /// 建立可直接輸出的生成事件串流。HTTP 層可在每個 chunk 到達時
    /// 立即寫入 SSE，不需等待整段生成完成。
    func stream(
        messages: [InputMessage],
        options: GenerationOptions
    ) async throws -> AsyncStream<Generation> {
        try await generationStream(messages: messages, options: options)
    }

    func logCompletion(_ info: GenerateCompletionInfo) {
        fputs(generationLog(info), stderr)
    }

    private func generationStream(
        messages: [InputMessage],
        options: GenerationOptions
    ) async throws -> AsyncStream<Generation> {
        try await prepare()
        guard let container else {
            throw APIError.invalidRequest("MLX 模型尚未載入。")
        }

        var temporaryFiles: [URL] = []
        var chat = try await makeChat(messages, temporaryFiles: &temporaryFiles)
        defer {
            for file in temporaryFiles {
                try? FileManager.default.removeItem(at: file)
            }
        }
        let tools = options.toolChoice?.disablesTools == true ? nil : options.tools
        if let instruction = options.toolChoice?.requiredInstruction {
            if let systemIndex = chat.lastIndex(where: { $0.role == .system }) {
                chat[systemIndex].content += "\n\n\(instruction)"
            } else {
                chat.insert(.system(instruction), at: 0)
            }
        }
        var additionalContextValues: [String: any Sendable] = [:]
        if let thinkingEnabled = configuration.thinkingEnabled {
            additionalContextValues["enable_thinking"] = thinkingEnabled
            additionalContextValues["reasoning_effort"] = "low"
        }
        if let toolChoice = options.toolChoice {
            additionalContextValues["tool_choice"] = toolChoice.templateValue
        }
        let additionalContext = additionalContextValues.isEmpty ? nil : additionalContextValues
        // UserInput 內含影像列舉，第三方套件尚未標註 Sendable；此值建立後只會
        // 單向交給 ModelContainer.prepare，不會再被本 actor 存取或修改。
        nonisolated(unsafe) let input = UserInput(
            chat: chat,
            tools: tools,
            additionalContext: additionalContext
        )
        let prepared = try await container.prepare(input: input)
        let resolvedTemperature = options.temperature
            ?? configuration.temperatureOverride
            ?? modelGenerationDefaults.temperature
            ?? configuration.temperature
        let resolvedTopP = options.topP
            ?? configuration.topPOverride
            ?? modelGenerationDefaults.topP
            ?? configuration.topP
        let resolvedTopK = configuration.topKOverride
            ?? modelGenerationDefaults.topK
            ?? configuration.topK
        let resolvedMinP = configuration.minPOverride
            ?? modelGenerationDefaults.minP
            ?? configuration.minP
        let resolvedRepetitionPenalty = configuration.repetitionPenalty
            ?? modelGenerationDefaults.repetitionPenalty
        let parameters = GenerateParameters(
            maxTokens: max(1, options.maxTokens ?? configuration.maxTokens),
            maxKVSize: configuration.maxKVSize,
            kvBits: configuration.kvBits,
            kvGroupSize: configuration.kvGroupSize,
            quantizedKVStart: configuration.quantizedKVStart,
            kvScheme: configuration.kvScheme,
            temperature: min(max(resolvedTemperature, 0), 2),
            topP: min(max(resolvedTopP, 0), 1),
            topK: resolvedTopK,
            minP: resolvedMinP,
            repetitionPenalty: resolvedRepetitionPenalty,
            repetitionContextSize: 128,
            prefillStepSize: configuration.prefillStepSize,
            seed: options.seed
        )
        let wiredMemoryTicket = memoryMapPlan?.inferenceTicket()

        if mtpDrafterContainer != nil,
            let fallbackReason = mtpFallbackReason(parameters: parameters)
        {
            fputs("MTP fallback to standard generation: \(fallbackReason)\n", stderr)
            return try await container.generate(
                input: prepared,
                parameters: parameters,
                wiredMemoryTicket: wiredMemoryTicket)
        }
        if let mtpDrafterContainer {
            return try await container.generate(
                input: prepared,
                parameters: parameters,
                mtpDrafterContainer: mtpDrafterContainer,
                blockSize: configuration.mtpBlockSize,
                wiredMemoryTicket: wiredMemoryTicket)
        }
        if dflashDrafter != nil,
           let fallbackReason = dflashFallbackReason(parameters: parameters) {
            fputs("DFlash fallback to standard generation: \(fallbackReason)\n", stderr)
            return try await container.generate(
                input: prepared,
                parameters: parameters,
                wiredMemoryTicket: wiredMemoryTicket
            )
        }
        if let drafter = dflashDrafter {
            return try await container.generate(
                input: prepared,
                parameters: parameters,
                dflashDrafter: drafter,
                blockSize: configuration.dflashBlockSize,
                wiredMemoryTicket: wiredMemoryTicket
            )
        }
        return try await container.generate(
            input: prepared,
            parameters: parameters,
            wiredMemoryTicket: wiredMemoryTicket
        )
    }

    private func dflashFallbackReason(parameters: GenerateParameters) -> String? {
        if parameters.maxKVSize != nil {
            return "DFlash 目前不支援 rotating target KV Cache"
        }
        if parameters.kvBits != nil || parameters.kvScheme != nil {
            return "DFlash 目前不支援量化 target KV Cache"
        }
        return nil
    }

    private func mtpFallbackReason(parameters: GenerateParameters) -> String? {
        if parameters.maxKVSize != nil {
            return "MTP 目前不支援 rotating target KV Cache"
        }
        if parameters.kvBits != nil || parameters.kvScheme != nil {
            return "MTP 目前不支援量化 target KV Cache"
        }
        return nil
    }

    private func generationLog(_ info: GenerateCompletionInfo) -> String {
        var fields = [
            "generation",
            "prompt_tokens=\(info.promptTokenCount)",
            "completion_tokens=\(info.generationTokenCount)",
            "tokens_per_second=\(String(format: "%.2f", info.tokensPerSecond))"
        ]
        if let proposed = info.proposedDraftTokens,
           let accepted = info.acceptedDraftTokens {
            let rate = proposed > 0 ? Double(accepted) / Double(proposed) : 0
            let prefix = mtpDrafterContainer == nil ? "dflash" : "mtp"
            fields.append("\(prefix)_proposed=\(proposed)")
            fields.append("\(prefix)_accepted=\(accepted)")
            fields.append("\(prefix)_acceptance=\(String(format: "%.3f", rate))")
        }
        return fields.joined(separator: " ") + "\n"
    }

    private func makeChat(
        _ messages: [InputMessage],
        temporaryFiles: inout [URL]
    ) async throws -> [Chat.Message] {
        guard !messages.isEmpty else {
            throw APIError.invalidRequest("messages 不可為空。")
        }
        var result: [Chat.Message] = []
        var systemParts: [String] = []
        for message in messages {
            let parsed = try await parseContent(
                message.content ?? .text(""),
                temporaryFiles: &temporaryFiles
            )
            let role = message.role.lowercased()
            switch role {
            case "system", "developer":
                guard parsed.images.isEmpty else {
                    throw APIError.unsupportedContent("系統指令不支援圖片。")
                }
                if !parsed.text.isEmpty {
                    systemParts.append(parsed.text)
                }
            case "assistant":
                let toolCalls = try message.toolCalls?.map { try $0.mlxToolCall() }
                result.append(
                    .assistant(parsed.text, images: parsed.images, toolCalls: toolCalls)
                )
            case "user":
                result.append(.user(parsed.text, images: parsed.images))
            case "tool":
                result.append(.tool(parsed.text, id: message.toolCallID))
            default:
                throw APIError.invalidRequest("不支援的訊息角色：\(message.role)")
            }
        }
        if !systemParts.isEmpty {
            // OpenAI-compatible clients may emit more than one system/developer
            // message. Many Hugging Face templates only accept one leading
            // system message, so preserve their order and normalize them here.
            result.insert(.system(systemParts.joined(separator: "\n\n")), at: 0)
        }
        return result
    }

    private func parseContent(
        _ content: MessageContent,
        temporaryFiles: inout [URL]
    ) async throws -> (text: String, images: [UserInput.Image]) {
        switch content {
        case .text(let text):
            return (text, [])
        case .parts(let parts):
            var textParts: [String] = []
            var images: [UserInput.Image] = []
            for part in parts {
                switch part.type.lowercased() {
                case "text", "input_text":
                    if let text = part.text { textParts.append(text) }
                case "image_url", "input_image":
                    guard kind == .vision else {
                        throw APIError.unsupportedContent("目前載入的是文生文模型，不能處理圖片。")
                    }
                    guard let value = part.imageURL?.url else {
                        throw APIError.invalidRequest("image_url 缺少 url。")
                    }
                    let image = try await resolveImage(value, temporaryFiles: &temporaryFiles)
                    images.append(.url(image))
                default:
                    throw APIError.unsupportedContent("不支援的 content part：\(part.type)")
                }
            }
            return (textParts.joined(separator: "\n"), images)
        }
    }

    private func resolveImage(
        _ value: String,
        temporaryFiles: inout [URL]
    ) async throws -> URL {
        if value.hasPrefix("data:") {
            guard let comma = value.firstIndex(of: ","),
                  value[..<comma].contains(";base64"),
                  let data = Data(base64Encoded: String(value[value.index(after: comma)...])) else {
                throw APIError.invalidImageURL("無效的 data URL")
            }
            return try persistTemporaryImage(data, temporaryFiles: &temporaryFiles)
        }
        guard let url = URL(string: value) else {
            throw APIError.invalidImageURL(value)
        }
        if url.isFileURL {
            guard FileManager.default.fileExists(atPath: url.path) else {
                throw APIError.invalidImageURL(value)
            }
            return url
        }
        if url.scheme == nil, FileManager.default.fileExists(atPath: value) {
            return URL(fileURLWithPath: value)
        }
        guard url.scheme == "https" || url.scheme == "http" else {
            throw APIError.invalidImageURL(value)
        }
        let (data, response) = try await URLSession.shared.data(from: url)
        if let response = response as? HTTPURLResponse,
           !(200...299).contains(response.statusCode) {
            throw APIError.invalidImageURL("\(value)（HTTP \(response.statusCode)）")
        }
        return try persistTemporaryImage(data, temporaryFiles: &temporaryFiles)
    }

    private func persistTemporaryImage(
        _ data: Data,
        temporaryFiles: inout [URL]
    ) throws -> URL {
        guard data.count <= configuration.maximumImageBytes else {
            throw APIError.imageTooLarge
        }
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("llamaloader-mlx-\(UUID().uuidString).image")
        try data.write(to: url, options: .atomic)
        temporaryFiles.append(url)
        return url
    }

    private func firstStop(in text: String, candidates: [String]) -> String.Index? {
        candidates.compactMap { candidate in
            candidate.isEmpty ? nil : text.range(of: candidate)?.lowerBound
        }.min()
    }

    private static func resolveModelKind(
        requested: ModelKind,
        directory: URL,
        isGGUF: Bool,
        hasMMProj: Bool
    ) throws -> ModelKind {
        guard requested == .auto else { return requested }
        if isGGUF {
            return hasMMProj ? .vision : .text
        }
        let configURL = directory.appendingPathComponent("config.json")
        guard let data = try? Data(contentsOf: configURL),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return .text
        }
        let hasVisionConfiguration = object["vision_config"] != nil
            || object["vision_tower"] != nil
            || object["mm_projector_type"] != nil
            || object["image_token_index"] != nil
        let modelType = object["model_type"] as? String ?? ""
        let architectures = object["architectures"] as? [String] ?? []
        let signature = ([modelType] + architectures).joined(separator: " ").lowercased()
        let markers = ["vision", "vl", "llava", "paligemma", "pixtral", "idefics", "florence"]
        let declaresVisionModel = hasVisionConfiguration
            || markers.contains(where: signature.contains)
        guard declaresVisionModel else { return .text }

        // 部分文字用途的衍生 checkpoint 會沿用原始 VLM 的 config.json，卻只保留
        // Tokenizer 與語言權重，沒有任何 Vision Processor 設定。這類目錄若直接走
        // VLMModelFactory，只會在載入 processor_config.json 時失敗；改由 LLM registry
        // 實際判斷其 model_type 是否可作為文字模型載入。
        let processorFiles = ["preprocessor_config.json", "processor_config.json"]
        let hasVisionProcessor = processorFiles.contains { filename in
            FileManager.default.fileExists(
                atPath: directory.appendingPathComponent(filename).path)
        }
        return hasVisionProcessor ? .vision : .text
    }
}
