import Foundation
import XCTest
@testable import MLXServer

final class AccessControlTests: XCTestCase {
    func testEveryIPv4AndIPv6Prefix() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: url) }
        for (network, same, different, bits) in [
            ("0.0.0.0", "0.0.0.0", "128.0.0.0", 32),
            ("::", "::", "8000::", 128)
        ] {
            for prefix in 0...bits {
                let policy: [String: Any] = ["version": 1, "policy": [
                    "api_key_enabled": false, "ip_allowlist_enabled": true,
                    "ip_allowlist": ["\(network)/\(prefix)"]
                ], "keys": []]
                try JSONSerialization.data(withJSONObject: policy).write(to: url)
                let control = RuntimeAccessControl(path: url.path)
                XCTAssertEqual(control.authorize(remoteAddress: same, apiKey: nil, validateAPIKey: false), .allowed)
                XCTAssertEqual(control.authorize(remoteAddress: different, apiKey: nil, validateAPIKey: false), prefix == 0 ? .allowed : .ipNotAllowed)
            }
        }
    }

    func testPartialByteBoundary() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: url) }
        try Data(#"{"version":1,"policy":{"api_key_enabled":false,"ip_allowlist_enabled":true,"ip_allowlist":["127.0.0.0/25","2001:db8::/65"]}}"#.utf8).write(to: url)
        let control = RuntimeAccessControl(path: url.path)
        for ip in ["127.0.0.127", "2001:db8:0:0:7fff::"] {
            XCTAssertEqual(control.authorize(remoteAddress: ip, apiKey: nil, validateAPIKey: false), .allowed)
        }
        for ip in ["127.0.0.128", "2001:db8:0:0:8000::"] {
            XCTAssertEqual(control.authorize(remoteAddress: ip, apiKey: nil, validateAPIKey: false), .ipNotAllowed)
        }
    }
}
