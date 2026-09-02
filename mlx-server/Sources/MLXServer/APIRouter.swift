import Foundation
import MLXLMCommon

enum HTTPResponseBody: Sendable {
    case data(Data)
    case stream(AsyncThrowingStream<Data, Error>)
}

struct HTTPResponse: Sendable {
    var status: Int
    var headers: [(String, String)]
    var body: HTTPResponseBody

    static func json(status: Int = 200, object: Any) -> Self {
        let body: Data
        do {
            body = try JSONSerialization.data(withJSONObject: object, options: [.withoutEscapingSlashes])
        } catch {
            body = Data("{\"error\":{\"message\":\"無法編碼回應\"}}".utf8)
        }
        return Self(
            status: status,
            headers: [
                ("Content-Type", "application/json; charset=utf-8"),
                ("Access-Control-Allow-Origin", "*")
            ],
            body: .data(body)
        )
    }

    static func sse(_ objects: [[String: Any]]) -> Self {
        var content = ""
        for object in objects {
            if let data = try? JSONSerialization.data(withJSONObject: object, options: [.withoutEscapingSlashes]),
               let json = String(data: data, encoding: .utf8) {
                content += "data: \(json)\n\n"
            }
        }
        content += "data: [DONE]\n\n"
        return Self(
            status: 200,
            headers: [
                ("Content-Type", "text/event-stream; charset=utf-8"),
                ("Cache-Control", "no-cache"),
                ("Access-Control-Allow-Origin", "*")
            ],
            body: .data(Data(content.utf8))
        )
    }

    static func sseStream(_ stream: AsyncThrowingStream<Data, Error>) -> Self {
        Self(
            status: 200,
            headers: [
                ("Content-Type", "text/event-stream; charset=utf-8"),
                ("Cache-Control", "no-cache, no-transform"),
                ("X-Accel-Buffering", "no"),
                ("Access-Control-Allow-Origin", "*")
            ],
            body: .stream(stream)
        )
    }
}

actor APIRouter {
    private let runtime: MLXRuntime
    private let configuration: ServerConfiguration
    private let decoder = JSONDecoder()

    init(runtime: MLXRuntime, configuration: ServerConfiguration) {
        self.runtime = runtime
        self.configuration = configuration
    }

    func handle(method: String, path: String, body: Data) async -> HTTPResponse {
        if method == "OPTIONS" {
            return HTTPResponse(
                status: 204,
                headers: [
                    ("Access-Control-Allow-Origin", "*"),
                    ("Access-Control-Allow-Headers", "Content-Type, Authorization, X-OpenLoader-Key, X-Api-Key"),
                    ("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
                ],
                body: .data(Data())
            )
        }
        guard body.count <= configuration.maximumRequestBytes else {
            return errorResponse(APIError.requestTooLarge, status: 413)
        }
        var routePath = path
        while routePath.count > 1, routePath.hasSuffix("/") {
            routePath.removeLast()
        }
        do {
            switch (method, routePath) {
            case ("GET", "/health"), ("GET", "/v1/health"):
                return .json(object: ["status": "ok"])
            case ("GET", "/models"), ("GET", "/v1/models"):
                return await modelsResponse()
            case ("GET", "/props"):
                return await propsResponse()
            case ("POST", "/chat/completions"), ("POST", "/v1/chat/completions"):
                return try await chatCompletion(body)
            case ("POST", "/v1/completions"):
                return try await completion(body, llamaCompatible: false)
            case ("POST", "/completion"):
                return try await completion(body, llamaCompatible: true)
            default:
                return errorResponse(APIError.invalidRequest("Not Found"), status: 404)
            }
        } catch let error as DecodingError {
            return errorResponse(APIError.invalidRequest("JSON 格式錯誤：\(error.localizedDescription)"), status: 400)
        } catch let error as APIError {
            return errorResponse(error, status: 400)
        } catch let error as MLXRequestError {
            return errorResponse(error, status: error.status)
        } catch {
            fputs("request error: \(error.localizedDescription)\n", stderr)
            return errorResponse(error, status: 500)
        }
    }

    private func modelsResponse() async -> HTTPResponse {
        let id = runtime.modelID
        return .json(object: [
            "object": "list",
            "data": [[
                "id": id,
                "object": "model",
                "created": Int(Date().timeIntervalSince1970),
                "owned_by": "local"
            ]]
        ])
    }

    private func propsResponse() async -> HTTPResponse {
        let id = runtime.modelID
        let kind = runtime.kind.rawValue
        return .json(object: [
            "model_path": configuration.modelPath,
            "model_alias": id,
            "chat_template": "mlx-swift-lm",
            "modalities": kind == ModelKind.vision.rawValue ? ["text", "image"] : ["text"]
        ])
    }

    private func chatCompletion(_ body: Data) async throws -> HTTPResponse {
        let request = try decoder.decode(ChatCompletionRequest.self, from: body)
        try await validateModel(request.model)
        let options = GenerationOptions(
            maxTokens: request.maxCompletionTokens ?? request.maxTokens,
            temperature: request.temperature,
            topP: request.topP,
            stops: request.stop?.values ?? [],
            seed: request.seed,
            tools: request.toolSpecs,
            toolChoice: request.toolChoice
        )
        if request.stream == true {
            let generation = try await runtime.stream(
                messages: request.messages,
                options: options
            )
            return streamingChatCompletion(generation: generation, options: options)
        }
        let result = try await runtime.generate(
            messages: request.messages,
            options: options
        )
        let id = "chatcmpl-\(UUID().uuidString.replacingOccurrences(of: "-", with: ""))"
        let created = Int(Date().timeIntervalSince1970)
        let model = runtime.modelID
        let usage: [String: Any] = [
            "prompt_tokens": result.promptTokens,
            "completion_tokens": result.completionTokens,
            "total_tokens": result.promptTokens + result.completionTokens,
            "tokens_per_second": result.tokensPerSecond
        ]
        let toolCalls = openAIToolCalls(result.toolCalls)
        let finishReason = toolCalls.isEmpty ? result.finishReason : "tool_calls"
        var message: [String: Any] = [
            "role": "assistant",
            "content": result.text
        ]
        if !toolCalls.isEmpty {
            message["content"] = result.text.isEmpty ? NSNull() : result.text
            message["tool_calls"] = toolCalls
        }
        return .json(object: [
            "id": id,
            "object": "chat.completion",
            "created": created,
            "model": model,
            "choices": [[
                "index": 0,
                "message": message,
                "finish_reason": finishReason
            ]],
            "usage": usage,
        ])
    }

    private func streamingChatCompletion(
        generation: AsyncThrowingStream<Generation, Error>,
        options: GenerationOptions
    ) -> HTTPResponse {
        let id = "chatcmpl-\(UUID().uuidString.replacingOccurrences(of: "-", with: ""))"
        let created = Int(Date().timeIntervalSince1970)
        let model = runtime.modelID
        let cancellation = GenerationSafety.cancellation
        let stream = AsyncThrowingStream<Data, Error> { continuation in
            let producer = Task {
                // 命中 stop、消費者取消或回應被丟棄時，都停止對應模型工作。
                defer { cancellation?.cancel() }
                continuation.yield(
                    sseData([
                        "id": id,
                        "object": "chat.completion.chunk",
                        "created": created,
                        "model": model,
                        "choices": [
                            [
                                "index": 0,
                                "delta": ["role": "assistant"],
                                "finish_reason": NSNull(),
                            ]
                        ],
                    ]))

                var textFilter = StreamingTextFilter(stops: options.stops)
                var finishReason = "stop"
                var usage: [String: Any]?
                var toolCallIndex = 0
                var stoppedByRequest = false

                do {
                    for try await event in generation {
                        if Task.isCancelled {
                            continuation.finish()
                            return
                        }
                        switch event {
                        case .chunk(let text):
                            let filtered = textFilter.append(text)
                            if !filtered.text.isEmpty {
                                continuation.yield(
                                    chatContentChunk(
                                        id: id,
                                        created: created,
                                        model: model,
                                        text: filtered.text
                                    ))
                            }
                            if filtered.stopped {
                                stoppedByRequest = true
                                finishReason = "stop"
                                break
                            }
                        case .toolCall(let call):
                            let pendingText = textFilter.flush()
                            if !pendingText.isEmpty {
                                continuation.yield(
                                    chatContentChunk(
                                        id: id,
                                        created: created,
                                        model: model,
                                        text: pendingText
                                    ))
                            }
                            let calls = openAIToolCalls([call])
                            if var callValue = calls.first {
                                callValue["index"] = toolCallIndex
                                toolCallIndex += 1
                                continuation.yield(
                                    sseData([
                                        "id": id,
                                        "object": "chat.completion.chunk",
                                        "created": created,
                                        "model": model,
                                        "choices": [
                                            [
                                                "index": 0,
                                                "delta": ["tool_calls": [callValue]],
                                                "finish_reason": NSNull(),
                                            ]
                                        ],
                                    ]))
                            }
                            finishReason = "tool_calls"
                        case .info(let info):
                            await runtime.logCompletion(info)
                            if finishReason != "tool_calls" {
                                switch info.stopReason {
                                case .length: finishReason = "length"
                                case .stop, .cancelled: finishReason = "stop"
                                }
                            }
                            usage = usageObject(info)
                        }
                        if stoppedByRequest { break }
                    }
                } catch {
                    finishStreamError(error, continuation: continuation)
                    return
                }

                if Task.isCancelled {
                    continuation.finish()
                    return
                }
                let pendingText = textFilter.flush()
                if !pendingText.isEmpty {
                    continuation.yield(
                        chatContentChunk(
                            id: id,
                            created: created,
                            model: model,
                            text: pendingText
                        ))
                }
                var finalChunk: [String: Any] = [
                    "id": id,
                    "object": "chat.completion.chunk",
                    "created": created,
                    "model": model,
                    "choices": [
                        [
                            "index": 0,
                            "delta": [:],
                            "finish_reason": finishReason,
                        ]
                    ],
                ]
                if let usage { finalChunk["usage"] = usage }
                continuation.yield(sseData(finalChunk))
                continuation.yield(Data("data: [DONE]\n\n".utf8))
                continuation.finish()
            }
            continuation.onTermination = { @Sendable _ in
                cancellation?.cancel()
                producer.cancel()
            }
        }
        return .sseStream(stream)
    }

    private func completion(_ body: Data, llamaCompatible: Bool) async throws -> HTTPResponse {
        let request = try decoder.decode(CompletionRequest.self, from: body)
        try await validateModel(request.model)
        let options = GenerationOptions(
            maxTokens: request.maxTokens ?? request.nPredict,
            temperature: request.temperature,
            topP: request.topP,
            stops: request.stop?.values ?? [],
            seed: request.seed,
            tools: nil,
            toolChoice: nil
        )
        let messages = [InputMessage(role: "user", content: .text(request.prompt.text))]
        if request.stream == true {
            let generation = try await runtime.stream(messages: messages, options: options)
            return streamingCompletion(
                generation: generation,
                options: options,
                llamaCompatible: llamaCompatible
            )
        }
        let result = try await runtime.generate(
            messages: messages,
            options: options
        )
        if llamaCompatible {
            let object: [String: Any] = [
                "content": result.text,
                "stop": result.finishReason == "stop",
                "stopped_limit": result.finishReason == "length",
                "tokens_evaluated": result.promptTokens,
                "tokens_predicted": result.completionTokens,
                "tokens_per_second": result.tokensPerSecond
            ]
            return .json(object: object)
        }
        let id = "cmpl-\(UUID().uuidString.replacingOccurrences(of: "-", with: ""))"
        let created = Int(Date().timeIntervalSince1970)
        let model = runtime.modelID
        let usage: [String: Any] = [
            "prompt_tokens": result.promptTokens,
            "completion_tokens": result.completionTokens,
            "total_tokens": result.promptTokens + result.completionTokens,
            "tokens_per_second": result.tokensPerSecond
        ]
        let choice: [String: Any] = [
            "index": 0,
            "text": result.text,
            "finish_reason": result.finishReason,
            "logprobs": NSNull()
        ]
        return .json(object: [
            "id": id,
            "object": "text_completion",
            "created": created,
            "model": model,
            "choices": [choice],
            "usage": usage
        ])
    }

    private func streamingCompletion(
        generation: AsyncThrowingStream<Generation, Error>,
        options: GenerationOptions,
        llamaCompatible: Bool
    ) -> HTTPResponse {
        let id = "cmpl-\(UUID().uuidString.replacingOccurrences(of: "-", with: ""))"
        let created = Int(Date().timeIntervalSince1970)
        let model = runtime.modelID
        let cancellation = GenerationSafety.cancellation
        let stream = AsyncThrowingStream<Data, Error> { continuation in
            let producer = Task {
                defer { cancellation?.cancel() }
                var textFilter = StreamingTextFilter(stops: options.stops)
                var finishReason = "stop"
                var finalInfo: GenerateCompletionInfo?
                var stoppedByRequest = false

                do {
                    for try await event in generation {
                        if Task.isCancelled {
                            continuation.finish()
                            return
                        }
                        switch event {
                        case .chunk(let text):
                            let filtered = textFilter.append(text)
                            if !filtered.text.isEmpty {
                                continuation.yield(
                                    completionChunk(
                                        id: id,
                                        created: created,
                                        model: model,
                                        text: filtered.text,
                                        finishReason: nil,
                                        llamaCompatible: llamaCompatible,
                                        info: nil
                                    ))
                            }
                            if filtered.stopped {
                                stoppedByRequest = true
                                finishReason = "stop"
                            }
                        case .toolCall:
                            break
                        case .info(let info):
                            await runtime.logCompletion(info)
                            finalInfo = info
                            switch info.stopReason {
                            case .length: finishReason = "length"
                            case .stop, .cancelled: finishReason = "stop"
                            }
                        }
                        if stoppedByRequest { break }
                    }
                } catch {
                    finishStreamError(error, continuation: continuation)
                    return
                }

                if Task.isCancelled {
                    continuation.finish()
                    return
                }
                let pendingText = textFilter.flush()
                if !pendingText.isEmpty {
                    continuation.yield(
                        completionChunk(
                            id: id,
                            created: created,
                            model: model,
                            text: pendingText,
                            finishReason: nil,
                            llamaCompatible: llamaCompatible,
                            info: nil
                        ))
                }
                continuation.yield(
                    completionChunk(
                        id: id,
                        created: created,
                        model: model,
                        text: "",
                        finishReason: finishReason,
                        llamaCompatible: llamaCompatible,
                        info: finalInfo
                    ))
                continuation.yield(Data("data: [DONE]\n\n".utf8))
                continuation.finish()
            }
            continuation.onTermination = { @Sendable _ in
                cancellation?.cancel()
                producer.cancel()
            }
        }
        return .sseStream(stream)
    }

    private func completionChunk(
        id: String,
        created: Int,
        model: String,
        text: String,
        finishReason: String?,
        llamaCompatible: Bool,
        info: GenerateCompletionInfo?
    ) -> Data {
        if llamaCompatible {
            var object: [String: Any] = [
                "content": text,
                "stop": finishReason != nil,
                "stopped_limit": finishReason == "length",
            ]
            if let info {
                object["tokens_evaluated"] = info.promptTokenCount
                object["tokens_predicted"] = info.generationTokenCount
                object["tokens_per_second"] = info.tokensPerSecond
            }
            return sseData(object)
        }
        var object: [String: Any] = [
            "id": id,
            "object": "text_completion",
            "created": created,
            "model": model,
            "choices": [[
                "index": 0,
                "text": text,
                "finish_reason": finishReason.map { $0 as Any } ?? NSNull(),
                "logprobs": NSNull()
            ]]
        ]
        if let info { object["usage"] = usageObject(info) }
        return sseData(object)
    }

    private func chatContentChunk(
        id: String,
        created: Int,
        model: String,
        text: String
    ) -> Data {
        sseData([
            "id": id,
            "object": "chat.completion.chunk",
            "created": created,
            "model": model,
            "choices": [[
                "index": 0,
                "delta": ["content": text],
                "finish_reason": NSNull()
            ]]
        ])
    }

    private func usageObject(_ info: GenerateCompletionInfo) -> [String: Any] {
        [
            "prompt_tokens": info.promptTokenCount,
            "completion_tokens": info.generationTokenCount,
            "total_tokens": info.promptTokenCount + info.generationTokenCount,
            "tokens_per_second": info.tokensPerSecond
        ]
    }

    private func sseData(_ object: [String: Any]) -> Data {
        guard let data = try? JSONSerialization.data(
            withJSONObject: object,
            options: [.withoutEscapingSlashes]
        ) else {
            return Data()
        }
        var event = Data("data: ".utf8)
        event.append(data)
        event.append(Data("\n\n".utf8))
        return event
    }

    private func validateModel(_ requested: String?) async throws {
        guard let requested, !requested.isEmpty else { return }
        let loaded = runtime.modelID
        if requested != loaded && requested != configuration.modelPath {
            throw APIError.modelMismatch(requested)
        }
    }

    private func openAIToolCalls(_ calls: [ToolCall]) -> [[String: Any]] {
        calls.map { call in
            let arguments = call.function.arguments.mapValues(\.anyValue)
            let argumentsData = try? JSONSerialization.data(
                withJSONObject: arguments,
                options: [.sortedKeys, .withoutEscapingSlashes]
            )
            let argumentsText = argumentsData.flatMap { String(data: $0, encoding: .utf8) } ?? "{}"
            return [
                "id": call.id ?? "call_\(UUID().uuidString.replacingOccurrences(of: "-", with: ""))",
                "type": "function",
                "function": [
                    "name": call.function.name,
                    "arguments": argumentsText
                ]
            ]
        }
    }

    private func finishStreamError(
        _ error: Error, continuation: AsyncThrowingStream<Data, Error>.Continuation
    ) {
        if !Task.isCancelled && !(error is CancellationError) {
            fputs("stream error: \(error.localizedDescription)\n", stderr)
            continuation.yield(sseData([
                "error": [
                    "message": error.localizedDescription,
                    "type": "server_error",
                    "code": "generation_failed"
                ]
            ]))
            continuation.yield(Data("data: [DONE]\n\n".utf8))
        }
        continuation.finish()
    }

    private func errorResponse(_ error: Error, status: Int) -> HTTPResponse {
        .json(status: status, object: [
            "error": [
                "message": error.localizedDescription,
                "type": status >= 500 ? "server_error" : "invalid_request_error",
                "code": NSNull()
            ]
        ])
    }
}

private struct StreamingTextFilter {
    private let stops: [String]
    private let holdCount: Int
    private var buffer = ""
    private(set) var didStop = false

    init(stops: [String]) {
        self.stops = stops.filter { !$0.isEmpty }
        holdCount = max(0, (self.stops.map(\.count).max() ?? 1) - 1)
    }

    mutating func append(_ text: String) -> (text: String, stopped: Bool) {
        guard !didStop else { return ("", true) }
        buffer += text
        if let stopRange = earliestStopRange() {
            let output = String(buffer[..<stopRange.lowerBound])
            buffer = ""
            didStop = true
            return (output, true)
        }
        guard holdCount > 0, buffer.count > holdCount else {
            if holdCount == 0 {
                let output = buffer
                buffer = ""
                return (output, false)
            }
            return ("", false)
        }
        let emitCount = buffer.count - holdCount
        let split = buffer.index(buffer.startIndex, offsetBy: emitCount)
        let output = String(buffer[..<split])
        buffer = String(buffer[split...])
        return (output, false)
    }

    mutating func flush() -> String {
        guard !didStop else { return "" }
        let output = buffer
        buffer = ""
        return output
    }

    private func earliestStopRange() -> Range<String.Index>? {
        stops.compactMap { buffer.range(of: $0) }.min { lhs, rhs in
            lhs.lowerBound < rhs.lowerBound
        }
    }
}
