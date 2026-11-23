import Foundation
import MLXLMCommon
import XCTest

final class InklingGenerationTests: XCTestCase {
    func testFramesSeparateReasoningTextAndCompleteToolCalls() async throws {
        let tokens = FrameTokenizer()
        let sequence = [
            "<|message_model|>", "<|content_thinking|>", "思考", "<|end_message|>",
            "<|message_model|>", "<|content_text|>", "答案", "<|end_message|>",
            "<|message_model|>", "lookup", "<|content_invoke_tool_json|>",
            #"{"name":"lookup","args":{"city":"Taipei"}}"#, "<|end_message|>",
            "<|message_model|>", "lookup", "<|content_invoke_tool_json|>",
            #"{"name":"lookup","args":{"city":"Taipei"}}"#, "<|content_model_end_sampling|>",
        ]
        let ids = try sequence.map { try XCTUnwrap(tokens.convertTokenToId($0)) }
        let config = ModelConfiguration(
            id: "fixture/Inkling", toolCallFormat: ToolCallFormat.infer(from: "inkling2_loop"))
        let (events, task) = generateTask(
            promptTokenCount: 1, modelConfiguration: config,
            tokenizer: tokens, iterator: FrameIterator(tokens: ids))
        var reasoning = ""
        var text = ""
        var calls: [ToolCall] = []
        for await event in events {
            switch event {
            case .chunk(let chunk): text += chunk
            case .reasoning(let chunk): reasoning += chunk
            case .toolCall(let call): calls.append(call)
            case .info: break
            }
        }
        await task.value
        XCTAssertEqual(reasoning, "思考")
        XCTAssertEqual(text, "答案")
        XCTAssertEqual(calls.count, 1)
        XCTAssertEqual(calls.first?.function.name, "lookup")
        XCTAssertEqual(calls.first?.function.arguments["city"], .string("Taipei"))
    }
}

private struct FrameIterator: TokenIteratorProtocol {
    let tokens: [Int]
    var tokenCount = 0
    var maxTokens: Int? { tokens.count }
    var promptPrefillTime: TimeInterval { 0 }

    mutating func next() -> Int? {
        guard tokenCount < tokens.count else { return nil }
        defer { tokenCount += 1 }
        return tokens[tokenCount]
    }
}

private struct FrameTokenizer: Tokenizer {
    let vocabulary = [
        "<|message_model|>", "<|end_message|>", "<|content_model_end_sampling|>",
        "<|content_thinking|>", "<|content_text|>", "<|content_invoke_tool_json|>",
        "思考", "答案", "lookup", #"{"name":"lookup","args":{"city":"Taipei"}}"#,
    ]
    var bosToken: String? { nil }
    var eosToken: String? { "<|content_model_end_sampling|>" }
    var unknownToken: String? { nil }

    func convertTokenToId(_ token: String) -> Int? { vocabulary.firstIndex(of: token) }
    func convertIdToToken(_ id: Int) -> String? {
        vocabulary.indices.contains(id) ? vocabulary[id] : nil
    }
    func encode(text: String, addSpecialTokens: Bool) -> [Int] {
        convertTokenToId(text).map { [$0] } ?? []
    }
    func decode(tokenIds: [Int], skipSpecialTokens: Bool) -> String {
        tokenIds.compactMap(convertIdToToken).joined()
    }
    func applyChatTemplate(
        messages: [[String: any Sendable]], tools: [[String: any Sendable]]?,
        additionalContext: [String: any Sendable]?
    ) throws -> [Int] { throw TokenizerError.missingChatTemplate }
}
