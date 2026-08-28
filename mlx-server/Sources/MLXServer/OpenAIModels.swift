import Foundation

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
    var content: MessageContent
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
}

struct GenerationResult: Sendable {
    var text: String
    var promptTokens: Int
    var completionTokens: Int
    var finishReason: String
}

enum APIError: LocalizedError {
    case invalidRequest(String)
    case unsupportedContent(String)
    case modelMismatch(String)
    case requestTooLarge
    case imageTooLarge
    case invalidImageURL(String)

    var errorDescription: String? {
        switch self {
        case .invalidRequest(let message): message
        case .unsupportedContent(let message): message
        case .modelMismatch(let model): "目前服務載入的模型與請求不符：\(model)"
        case .requestTooLarge: "HTTP 請求內容超過大小限制。"
        case .imageTooLarge: "輸入圖片超過大小限制。"
        case .invalidImageURL(let value): "無法讀取 image_url：\(value)"
        }
    }
}
