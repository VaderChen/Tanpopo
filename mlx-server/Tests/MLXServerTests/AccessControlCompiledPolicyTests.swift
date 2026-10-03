import CryptoKit
import Foundation
import XCTest
@testable import MLXServer

final class AccessControlCompiledPolicyTests: XCTestCase {
    func testCompiledRulesPreserveAddressFamiliesAndWildcardSemantics() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: url) }
        for (patterns, allowed, denied) in [
            (["10.*", "2001:db8::/32", "::ffff:203.0.113.7"],
             ["10.2.3.4", "2001:db8:ffff::1", "203.0.113.7", "::ffff:203.0.113.7"],
             ["11.2.3.4", "2001:db9::1", "203.0.113.8", "invalid"]),
            (["fe80::1"], ["[fe80::1%en0]", "fe80::1%en0"], ["fe80::2"]),
            (["192.168.*"], ["192.168.1.5"], ["::ffff:192.168.1.5", "192.169.1.5"]),
            (["2001:*:1"], ["2001:db8::1"], ["2001:db8::2"]),
            (["*"], ["127.0.0.1", "::1"], ["invalid", ""])
        ] {
            try JSONSerialization.data(withJSONObject: ["version": 1, "keys": [], "policy": [
                "api_key_enabled": false, "ip_allowlist_enabled": true, "ip_allowlist": patterns
            ]] as [String: Any]).write(to: url)
            let control = RuntimeAccessControl(path: url.path)
            for address in allowed { XCTAssertEqual(control.authorize(remoteAddress: address, apiKey: nil, validateAPIKey: true), .allowed, address) }
            for address in denied { XCTAssertEqual(control.authorize(remoteAddress: address, apiKey: nil, validateAPIKey: true), .ipNotAllowed, address) }
            XCTAssertEqual(control.authorize(remoteAddress: nil, apiKey: nil, validateAPIKey: false), .ipNotAllowed)
        }
    }

    func testInvalidPoliciesFailClosedAndKeyValidationRemainsIndependent() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: url) }
        let hash = SHA256.hash(data: Data("valid-key".utf8)).map { String(format: "%02x", $0) }.joined()
        func policy(_ patterns: [String]) throws -> RuntimeAccessControl {
            try JSONSerialization.data(withJSONObject: ["version": 1, "keys": [["hash": hash]], "policy": [
                "api_key_enabled": true, "ip_allowlist_enabled": true, "ip_allowlist": patterns
            ]] as [String: Any]).write(to: url)
            return RuntimeAccessControl(path: url.path)
        }
        for patterns in [["*", "xyz*"], ["127.0.0.1/33"], ["::/129"], ["::ffff:127.0.0.1/128"], [""], [], Array(repeating: "*", count: 257)] {
            let control = try policy(patterns)
            XCTAssertEqual(control.authorize(remoteAddress: "127.0.0.1", apiKey: "valid-key", validateAPIKey: true), .policyUnavailable)
        }
        let control = try policy(["127.0.0.0/8"])
        XCTAssertEqual(control.authorize(remoteAddress: "127.0.0.1", apiKey: "valid-key", validateAPIKey: true), .allowed)
        XCTAssertEqual(control.authorize(remoteAddress: "127.0.0.1", apiKey: "wrong-key", validateAPIKey: true), .invalidAPIKey)
        XCTAssertEqual(control.authorize(remoteAddress: "127.0.0.1", apiKey: nil, validateAPIKey: false), .allowed)
        XCTAssertEqual(control.authorize(remoteAddress: "192.0.2.1", apiKey: "valid-key", validateAPIKey: true), .ipNotAllowed)
    }
}
