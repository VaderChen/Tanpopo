import Foundation
import XCTest
@testable import MLXServer

final class ModelContextLimitTests: XCTestCase {
    private func resolve(_ json: String?, configured: Int? = nil) throws -> Int? {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("tanpopo-context-test-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        if let json {
            try Data(json.utf8).write(to: directory.appendingPathComponent("config.json"))
        }
        return ModelContextLimit.resolve(
            directory: directory, ggufWeightURL: nil, fastGGUFManifestURL: nil,
            configuredLimit: configured)
    }

    func testEffectiveLimitUsesSmallerModelOrConfiguredLimit() throws {
        let json = #"{"max_position_embeddings":32768}"#
        XCTAssertEqual(try resolve(json), 32768)
        XCTAssertEqual(try resolve(json, configured: 8192), 8192)
        XCTAssertEqual(try resolve(json, configured: 65536), 32768)
        XCTAssertEqual(try resolve(json, configured: 0), 32768)
    }

    func testMultimodalModelUsesTextConfiguration() throws {
        XCTAssertEqual(
            try resolve(#"{"max_position_embeddings":1024,"text_config":{"max_position_embeddings":8192}}"#),
            8192)
        XCTAssertEqual(
            try resolve(#"{"max_position_embeddings":4096,"text_config":{"max_position_embeddings":false}}"#),
            4096)
    }

    func testInvalidOrUnknownMetadataDoesNotInventContextLimit() throws {
        for value in ["true", "false", "0", "-1", "0.5", "4096.5", "1e100", #""8192""#, "null"] {
            XCTAssertNil(try resolve("{\"max_position_embeddings\":\(value)}"), value)
        }
        for json: String? in [nil, "invalid", #"{"sliding_window":4096,"max_tokens":8192}"#] {
            XCTAssertNil(try resolve(json))
            XCTAssertEqual(try resolve(json, configured: 16384), 16384)
        }
    }
}
