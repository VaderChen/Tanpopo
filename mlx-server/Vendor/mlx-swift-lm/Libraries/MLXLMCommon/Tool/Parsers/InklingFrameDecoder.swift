// Copyright © 2026 Apple Inc.

import Foundation

/// The payload kind a frame header ends with.
private enum InklingContentKind: Equatable {
    case thinking
    case text
    case toolJSON
    case toolText
}

private enum InklingParseStep {
    case consumed
    case opened(InklingContentKind)
    case payload(InklingContentKind, Int)
    case closed(InklingContentKind, name: String, tokens: [Int])
    case rejected(String)
}

/// Token-aware parser of Inkling 2's message frames:
/// `<|message_model|>name<|content_kind|>payload<|end_message|>`, repeated, then
/// `<|content_model_end_sampling|>`.
///
/// Structural markers are compared as token ids, so marker-shaped text in a payload is never
/// a frame boundary.
private struct InklingFrameParser {
    private struct Controls {
        var messageModel: Int
        var endMessage: Int
        var endSampling: Int
        var kinds: [Int: InklingContentKind]
    }

    private enum State {
        case start
        case betweenFrames(reportedUnexpectedToken: Bool)
        case header(tokens: [Int])
        case payload(kind: InklingContentKind, name: String, tokens: [Int])
        case finished
    }

    private let tokenizer: any Tokenizer
    private let controls: Controls
    private var state = State.start
    private let maximumHeaderTokens = 64

    init?(tokenizer: any Tokenizer) {
        guard let messageModel = tokenizer.convertTokenToId("<|message_model|>"),
            let endMessage = tokenizer.convertTokenToId("<|end_message|>"),
            let endSampling = tokenizer.convertTokenToId("<|content_model_end_sampling|>"),
            let thinking = tokenizer.convertTokenToId("<|content_thinking|>"),
            let text = tokenizer.convertTokenToId("<|content_text|>"),
            let toolJSON = tokenizer.convertTokenToId("<|content_invoke_tool_json|>")
        else { return nil }
        var kinds: [Int: InklingContentKind] = [
            thinking: .thinking, text: .text, toolJSON: .toolJSON,
        ]
        if let toolText = tokenizer.convertTokenToId("<|content_invoke_tool_text|>") {
            kinds[toolText] = .toolText
        }
        guard Set([messageModel, endMessage, endSampling] + kinds.keys).count == kinds.count + 3
        else { return nil }
        self.tokenizer = tokenizer
        self.controls = Controls(
            messageModel: messageModel, endMessage: endMessage, endSampling: endSampling,
            kinds: kinds)
    }

    mutating func push(_ token: Int) -> InklingParseStep {
        if token == controls.endSampling {
            defer { state = .finished }
            switch state {
            case .payload(let kind, _, _):
                return .rejected(
                    "Inkling \(description(of: kind)) frame ended without <|end_message|>")
            case .header(let tokens) where !tokens.isEmpty:
                return .rejected(
                    "Incomplete Inkling frame header at <|content_model_end_sampling|>")
            default:
                return .consumed
            }
        }

        switch state {
        case .finished:
            return .consumed

        case .start:
            // A model turn opens with a frame marker, so a first token without one continues
            // the frame the prompt left open: a continued final message ends inside its text.
            let opensFrame = token == controls.messageModel || controls.kinds[token] != nil
            state =
                opensFrame
                ? .betweenFrames(reportedUnexpectedToken: false)
                : .payload(kind: .text, name: "", tokens: [])
            return push(token)

        case .betweenFrames(let reported):
            if token == controls.messageModel {
                state = .header(tokens: [])
                return .consumed
            }
            // Tolerate a frame that opens without its role marker.
            if let kind = controls.kinds[token] {
                state = .payload(kind: kind, name: "", tokens: [])
                return .opened(kind)
            }
            guard !reported else { return .consumed }
            state = .betweenFrames(reportedUnexpectedToken: true)
            let text = tokenizer.decode(tokenIds: [token], skipSpecialTokens: false)
            return .rejected("Unexpected token between Inkling frames: \(text)")

        case .header(var tokens):
            if let kind = controls.kinds[token] {
                let name = tokenizer.decode(tokenIds: tokens, skipSpecialTokens: false)
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                state = .payload(kind: kind, name: name, tokens: [])
                return .opened(kind)
            }
            if token == controls.messageModel || token == controls.endMessage {
                state = .betweenFrames(reportedUnexpectedToken: false)
                return .rejected("Inkling frame header ended before its content marker")
            }
            guard tokens.count < maximumHeaderTokens else {
                state = .betweenFrames(reportedUnexpectedToken: false)
                return .rejected("Inkling frame header exceeded \(maximumHeaderTokens) tokens")
            }
            tokens.append(token)
            state = .header(tokens: tokens)
            return .consumed

        case .payload(let kind, let name, var tokens):
            if token == controls.endMessage {
                state = .betweenFrames(reportedUnexpectedToken: false)
                return .closed(kind, name: name, tokens: tokens)
            }
            if token == controls.messageModel {
                state = .header(tokens: [])
                return .rejected(
                    "Inkling \(description(of: kind)) frame was interrupted by <|message_model|>")
            }
            if kind == .toolJSON || kind == .toolText {
                tokens.append(token)
            }
            state = .payload(kind: kind, name: name, tokens: tokens)
            return .payload(kind, token)
        }
    }

    private func description(of kind: InklingContentKind) -> String {
        switch kind {
        case .thinking: "thinking"
        case .text: "text"
        case .toolJSON, .toolText: "tool"
        }
    }
}

/// 將完整的訊息邊界轉成 Tanpopo 的串流事件。
struct InklingFrameDecoder {
    private var parser: InklingFrameParser
    private var detokenizer: NaiveStreamingDetokenizer
    private let tokenizer: any Tokenizer

    init?(tokenizer: any Tokenizer) {
        guard let parser = InklingFrameParser(tokenizer: tokenizer) else { return nil }
        self.parser = parser
        self.tokenizer = tokenizer
        self.detokenizer = .init(tokenizer: tokenizer)
    }

    mutating func push(_ token: Int) -> Generation? {
        switch parser.push(token) {
        case .opened:
            detokenizer = .init(tokenizer: tokenizer)
        case .payload(let kind, let token) where kind == .text || kind == .thinking:
            detokenizer.append(token: token)
            guard let chunk = detokenizer.next(), !chunk.isEmpty else { return nil }
            return kind == .text ? .chunk(chunk) : .reasoning(chunk)
        case .closed(let kind, let header, let tokens) where kind == .toolJSON || kind == .toolText:
            let data = Data(tokenizer.decode(tokenIds: tokens).utf8)
            struct Invocation: Decodable {
                let name: String?
                let args: [String: JSONValue]
            }
            guard let invocation = try? JSONDecoder().decode(Invocation.self, from: data),
                  let name = invocation.name ?? (header.isEmpty ? nil : header), !name.isEmpty,
                  header.isEmpty || header == name else { return nil }
            return .toolCall(.init(function: .init(name: name, arguments: invocation.args)))
        default: break
        }
        return nil
    }
}
