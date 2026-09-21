import Foundation
import XCTest
@testable import MLXServer

final class MLXRequestLimitsTests: XCTestCase {
    func testMetadataDimensionsRejectBooleansAndNonPositiveIntegers() throws {
        for value in ["true", "false", "0", "-1", "0.5", "32.5", "1e100", #""32""#, "null"] {
            let data = Data("{\"dimension\":\(value)}".utf8)
            let json = try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any])
            XCTAssertNil(MLXRequestLimits.positiveMetadataInteger(json["dimension"]), value)
        }
    }

    func testMetadataDimensionsAcceptExactPositiveIntegers() throws {
        for value in ["1", "32", "128.0", "4096"] {
            let data = Data("{\"dimension\":\(value)}".utf8)
            let json = try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any])
            XCTAssertEqual(MLXRequestLimits.positiveMetadataInteger(json["dimension"]), Int(Double(value)!))
        }
    }
}
