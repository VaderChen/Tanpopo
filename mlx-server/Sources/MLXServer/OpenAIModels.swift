import Foundation
import MLXLMCommon

struct ChatCompletionRequest: Decodable, Sendable {
    var model: String?
    var messages: [InputMessage]
    var maxTokens: Int?
    var maxCompletionTokens: Int?
    var temperature: Float?
    var topP: Float?
    var stream: Bool?
    var stop: StopValue?
    var seed: UInt64?
    var tools: [[String: JSONValue]]?
    var toolChoice: ToolChoice?

    enum CodingKeys: String, CodingKey {
        case model
        case messages
        case maxTokens = "max_tokens"
        case maxCompletionTokens = "max_completion_tokens"
        case temperature
        case topP = "top_p"
        case stream
        case stop
        case seed
        case tools
        case toolChoice = "tool_choice"
    }

    var toolSpecs: [ToolSpec]? {
        guard let tools, !tools.isEmpty else { return nil }
        return tools.map { tool in
            tool.mapValues(\.openAISendableValue)
        }
    }
}

enum ToolChoice: Decodable, Sendable {
    case auto
    case none
    case required
    case function(String)

    private struct FunctionChoice: Decodable {
        var type: String?
        var function: NamedFunction
    }

    private struct NamedFunction: Decodable {
        var name: String
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let value = try? container.decode(String.self) {
            switch value.lowercased() {
            case "none": self = .none
            case "required": self = .required
            default: self = .auto
            }
            return
        }
        let choice = try container.decode(FunctionChoice.self)
        self = .function(choice.function.name)
    }

    var disablesTools: Bool {
        if case .none = self { return true }
        return false
    }

    var requiredInstruction: String? {
        switch self {
        case .required:
            return "You must call at least one of the provided tools. Do not answer directly."
        case .function(let name):
            return "You must call the provided tool named \"\(name)\". Do not answer directly."
        case .auto, .none:
            return nil
        }
    }

    var templateValue: any Sendable {
        switch self {
        case .auto: return "auto"
        case .none: return "none"
        case .required: return "required"
        case .function(let name):
            return [
                "type": "function",
                "function": ["name": name] as [String: any Sendable]
            ] as [String: any Sendable]
        }
    }
}

struct CompletionRequest: Decodable, Sendable {
    var model: String?
    var prompt: PromptValue
    var maxTokens: Int?
    var nPredict: Int?
    var temperature: Float?
    var topP: Float?
    var stream: Bool?
    var stop: StopValue?
    var seed: UInt64?

    enum CodingKeys: String, CodingKey {
        case model
        case prompt
        case maxTokens = "max_tokens"
        case nPredict = "n_predict"
        case temperature
        case topP = "top_p"
        case stream
        case stop
        case seed
    }
}

struct InputMessage: Decodable, Sendable {
    var role: String
    var content: MessageContent?
    var toolCalls: [InputToolCall]?
    var toolCallID: String?

    enum CodingKeys: String, CodingKey {
        case role
        case content
        case toolCalls = "tool_calls"
        case toolCallID = "tool_call_id"
    }
}

struct InputToolCall: Decodable, Sendable {
    struct Function: Decodable, Sendable {
        var name: String
        var arguments: String
    }

    var id: String?
    var type: String?
    var function: Function

    func mlxToolCall() throws -> ToolCall {
        let data = Data(function.arguments.utf8)
        let arguments: [String: JSONValue]
        if function.arguments.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            arguments = [:]
        } else {
            arguments = try JSONDecoder().decode([String: JSONValue].self, from: data)
        }
        return ToolCall(
            function: .init(name: function.name, arguments: arguments),
            id: id
        )
    }
}

enum MessageContent: Decodable, Sendable {
    case text(String)
    case parts([ContentPart])

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let text = try? container.decode(String.self) {
            self = .text(text)
            return
        }
        self = .parts(try container.decode([ContentPart].self))
    }
}

struct ContentPart: Decodable, Sendable {
    var type: String
    var text: String?
    var imageURL: ImageURLValue?

    enum CodingKeys: String, CodingKey {
        case type
        case text
        case imageURL = "image_url"
    }
}

struct ImageURLValue: Decodable, Sendable {
    var url: String

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let value = try? container.decode(String.self) {
            url = value
            return
        }
        let object = try container.decode(Object.self)
        url = object.url
    }

    private struct Object: Decodable {
        var url: String
    }
}

enum PromptValue: Decodable, Sendable {
    case text(String)
    case list([String])

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let text = try? container.decode(String.self) {
            self = .text(text)
            return
        }
        self = .list(try container.decode([String].self))
    }

    var text: String {
        switch self {
        case .text(let value): value
        case .list(let values): values.joined(separator: "\n")
        }
    }
}

enum StopValue: Decodable, Sendable {
    case text(String)
    case list([String])

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let text = try? container.decode(String.self) {
            self = .text(text)
            return
        }
        self = .list(try container.decode([String].self))
    }

    var values: [String] {
        switch self {
        case .text(let value): [value]
        case .list(let values): values
        }
    }
}

struct GenerationOptions: Sendable {
    var maxTokens: Int?
    var temperature: Float?
    var topP: Float?
    var stops: [String]
    var seed: UInt64?
    var tools: [ToolSpec]?
    var toolChoice: ToolChoice?
}

struct GenerationResult: Sendable {
    var text: String
    var promptTokens: Int
    var completionTokens: Int
    var tokensPerSecond: Double
    var finishReason: String
    var toolCalls: [ToolCall]
}

private extension JSONValue {
    var openAISendableValue: any Sendable {
        switch self {
        case .null: NSNull()
        case .bool(let value): value
        case .int(let value): value
        case .double(let value): value
        case .string(let value): value
        case .array(let values): values.map(\.openAISendableValue)
        case .object(let values): values.mapValues(\.openAISendableValue)
        }
    }
}

enum APIError: LocalizedError {
    case invalidRequest(String)
    case unsupportedContent(String)
    case requestTooLarge
    case imageTooLarge
    case invalidImageURL(String)

    var errorDescription: String? {
        switch self {
        case .invalidRequest(let message): message
        case .unsupportedContent(let message): message
        case .requestTooLarge: "HTTP 請求內容超過大小限制。"
        case .imageTooLarge: "輸入圖片超過大小限制。"
        case .invalidImageURL(let value): "無法讀取 image_url：\(value)"
        }
    }
}
