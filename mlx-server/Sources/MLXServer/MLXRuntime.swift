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
    private var container: ModelContainer?
    private var dflashDrafter: (any DFlashDrafterModel)?

    init(configuration: ServerConfiguration) throws {
        self.configuration = configuration
        modelDirectory = URL(fileURLWithPath: configuration.modelPath, isDirectory: true)
        modelID = modelDirectory.lastPathComponent
        kind = try Self.resolveModelKind(
            requested: configuration.modelKind,
            directory: modelDirectory
        )
    }

    func prepare() async throws {
        guard container == nil else { return }
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

        if let draftPath = configuration.dflashDraftPath {
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
    }

    func generate(
        messages: [InputMessage],
        options: GenerationOptions
    ) async throws -> GenerationResult {
        try await prepare()
        guard let container else {
            throw APIError.invalidRequest("MLX 模型尚未載入。")
        }

        var temporaryFiles: [URL] = []
        defer {
            for file in temporaryFiles {
                try? FileManager.default.removeItem(at: file)
            }
        }
        let chat = try await makeChat(messages, temporaryFiles: &temporaryFiles)
        var additionalContext: [String: any Sendable]?
        if let thinkingEnabled = configuration.thinkingEnabled {
            additionalContext = [
                "enable_thinking": thinkingEnabled,
                "reasoning_effort": "low"
            ]
        }
        // UserInput 內含影像列舉，第三方套件尚未標註 Sendable；此值建立後只會
        // 單向交給 ModelContainer.prepare，不會再被本 actor 存取或修改。
        nonisolated(unsafe) let input = UserInput(
            chat: chat,
            additionalContext: additionalContext
        )
        let prepared = try await container.prepare(input: input)
        let parameters = GenerateParameters(
            maxTokens: max(1, options.maxTokens ?? configuration.maxTokens),
            maxKVSize: configuration.maxKVSize,
            kvBits: configuration.kvBits,
            kvGroupSize: configuration.kvGroupSize,
            kvScheme: configuration.kvScheme,
            temperature: min(max(options.temperature ?? configuration.temperature, 0), 2),
            topP: min(max(options.topP ?? configuration.topP, 0), 1),
            topK: configuration.topK,
            minP: configuration.minP,
            repetitionPenalty: configuration.repetitionPenalty,
            repetitionContextSize: 128,
            prefillStepSize: configuration.prefillStepSize,
            seed: options.seed
        )

        let stream: AsyncStream<Generation>
        if dflashDrafter != nil,
           let fallbackReason = dflashFallbackReason(parameters: parameters) {
            fputs("DFlash fallback to standard generation: \(fallbackReason)\n", stderr)
            stream = try await container.generate(input: prepared, parameters: parameters)
        } else if let drafter = dflashDrafter {
            stream = try await container.generate(
                input: prepared,
                parameters: parameters,
                dflashDrafter: drafter,
                blockSize: configuration.dflashBlockSize
            )
        } else {
            stream = try await container.generate(input: prepared, parameters: parameters)
        }
        var output = ""
        var promptTokens = 0
        var completionTokens = 0
        var finishReason = "stop"
        for await event in stream {
            switch event {
            case .chunk(let text):
                output += text
            case .info(let info):
                promptTokens = info.promptTokenCount
                completionTokens = info.generationTokenCount
                switch info.stopReason {
                case .length: finishReason = "length"
                case .stop, .cancelled: finishReason = "stop"
                }
                fputs(
                    generationLog(info),
                    stderr
                )
            case .toolCall:
                break
            }
        }

        if let stop = firstStop(in: output, candidates: options.stops) {
            output = String(output[..<stop])
            finishReason = "stop"
        }
        return GenerationResult(
            text: output,
            promptTokens: promptTokens,
            completionTokens: completionTokens,
            finishReason: finishReason
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
            fields.append("dflash_proposed=\(proposed)")
            fields.append("dflash_accepted=\(accepted)")
            fields.append("dflash_acceptance=\(String(format: "%.3f", rate))")
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
        for message in messages {
            let parsed = try await parseContent(message.content, temporaryFiles: &temporaryFiles)
            let role = message.role.lowercased()
            switch role {
            case "system":
                result.append(.system(parsed.text, images: parsed.images))
            case "assistant":
                result.append(.assistant(parsed.text, images: parsed.images))
            case "user":
                result.append(.user(parsed.text, images: parsed.images))
            case "tool":
                result.append(.tool(parsed.text))
            default:
                throw APIError.invalidRequest("不支援的訊息角色：\(message.role)")
            }
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
        directory: URL
    ) throws -> ModelKind {
        guard requested == .auto else { return requested }
        let configURL = directory.appendingPathComponent("config.json")
        guard let data = try? Data(contentsOf: configURL),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return .text
        }
        if object["vision_config"] != nil || object["vision_tower"] != nil
            || object["mm_projector_type"] != nil || object["image_token_index"] != nil {
            return .vision
        }
        let modelType = object["model_type"] as? String ?? ""
        let architectures = object["architectures"] as? [String] ?? []
        let signature = ([modelType] + architectures).joined(separator: " ").lowercased()
        let markers = ["vision", "vl", "llava", "paligemma", "pixtral", "idefics", "florence"]
        return markers.contains(where: signature.contains) ? .vision : .text
    }
}
