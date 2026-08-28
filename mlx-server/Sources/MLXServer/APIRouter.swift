import Foundation

struct HTTPResponse: Sendable {
    var status: Int
    var headers: [(String, String)]
    var body: Data

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
            body: body
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
            body: Data(content.utf8)
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
                body: Data()
            )
        }
        guard body.count <= configuration.maximumRequestBytes else {
            return errorResponse(APIError.requestTooLarge, status: 413)
        }
        do {
            switch (method, path) {
            case ("GET", "/health"):
                return .json(object: ["status": "ok"])
            case ("GET", "/v1/models"):
                return await modelsResponse()
            case ("GET", "/props"):
                return await propsResponse()
            case ("POST", "/v1/chat/completions"):
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
        let result = try await runtime.generate(
            messages: request.messages,
            options: GenerationOptions(
                maxTokens: request.maxCompletionTokens ?? request.maxTokens,
                temperature: request.temperature,
                topP: request.topP,
                stops: request.stop?.values ?? [],
                seed: request.seed
            )
        )
        let id = "chatcmpl-\(UUID().uuidString.replacingOccurrences(of: "-", with: ""))"
        let created = Int(Date().timeIntervalSince1970)
        let model = runtime.modelID
        let usage: [String: Any] = [
            "prompt_tokens": result.promptTokens,
            "completion_tokens": result.completionTokens,
            "total_tokens": result.promptTokens + result.completionTokens
        ]
        if request.stream == true {
            return .sse([
                [
                    "id": id, "object": "chat.completion.chunk", "created": created, "model": model,
                    "choices": [["index": 0, "delta": ["role": "assistant"], "finish_reason": NSNull()]]
                ],
                [
                    "id": id, "object": "chat.completion.chunk", "created": created, "model": model,
                    "choices": [["index": 0, "delta": ["content": result.text], "finish_reason": NSNull()]]
                ],
                [
                    "id": id, "object": "chat.completion.chunk", "created": created, "model": model,
                    "choices": [["index": 0, "delta": [:], "finish_reason": result.finishReason]],
                    "usage": usage
                ]
            ])
        }
        return .json(object: [
            "id": id,
            "object": "chat.completion",
            "created": created,
            "model": model,
            "choices": [[
                "index": 0,
                "message": ["role": "assistant", "content": result.text],
                "finish_reason": result.finishReason
            ]],
            "usage": usage
        ])
    }

    private func completion(_ body: Data, llamaCompatible: Bool) async throws -> HTTPResponse {
        let request = try decoder.decode(CompletionRequest.self, from: body)
        try await validateModel(request.model)
        let result = try await runtime.generate(
            messages: [InputMessage(role: "user", content: .text(request.prompt.text))],
            options: GenerationOptions(
                maxTokens: request.maxTokens ?? request.nPredict,
                temperature: request.temperature,
                topP: request.topP,
                stops: request.stop?.values ?? [],
                seed: request.seed
            )
        )
        if llamaCompatible {
            let object: [String: Any] = [
                "content": result.text,
                "stop": result.finishReason == "stop",
                "stopped_limit": result.finishReason == "length",
                "tokens_evaluated": result.promptTokens,
                "tokens_predicted": result.completionTokens
            ]
            return request.stream == true ? .sse([object]) : .json(object: object)
        }
        let id = "cmpl-\(UUID().uuidString.replacingOccurrences(of: "-", with: ""))"
        let created = Int(Date().timeIntervalSince1970)
        let model = runtime.modelID
        let usage: [String: Any] = [
            "prompt_tokens": result.promptTokens,
            "completion_tokens": result.completionTokens,
            "total_tokens": result.promptTokens + result.completionTokens
        ]
        let choice: [String: Any] = [
            "index": 0,
            "text": result.text,
            "finish_reason": result.finishReason,
            "logprobs": NSNull()
        ]
        if request.stream == true {
            return .sse([[
                "id": id, "object": "text_completion", "created": created, "model": model,
                "choices": [choice], "usage": usage
            ]])
        }
        return .json(object: [
            "id": id,
            "object": "text_completion",
            "created": created,
            "model": model,
            "choices": [choice],
            "usage": usage
        ])
    }

    private func validateModel(_ requested: String?) async throws {
        guard let requested, !requested.isEmpty else { return }
        let loaded = runtime.modelID
        if requested != loaded && requested != configuration.modelPath {
            throw APIError.modelMismatch(requested)
        }
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
