import CryptoKit
import Darwin
import Foundation

enum RuntimeAccessDecision: Sendable {
    case allowed
    case invalidAPIKey
    case ipNotAllowed
    case policyUnavailable
}

/// Runtime-local access control. Tanpopo writes the snapshot; mlx-server
/// periodically reloads it and authorizes requests without proxying to Go.
final class RuntimeAccessControl: @unchecked Sendable {
    private struct PolicyFile: Decodable {
        struct Policy: Decodable {
            let apiKeyEnabled: Bool
            let ipAllowlistEnabled: Bool
            let ipAllowlist: [String]?

            enum CodingKeys: String, CodingKey {
                case apiKeyEnabled = "api_key_enabled"
                case ipAllowlistEnabled = "ip_allowlist_enabled"
                case ipAllowlist = "ip_allowlist"
            }
        }

        struct StoredKey: Decodable {
            let hash: String
        }

        let version: Int
        let policy: Policy
        let keys: [StoredKey]?
    }

    private struct Snapshot {
        let apiKeyEnabled: Bool
        let ipAllowlistEnabled: Bool
        let ipAllowlist: [String]
        let keyHashes: [[UInt8]]
    }

    private enum ParsedIPAddress {
        case ipv4([UInt8])
        case ipv6([UInt8])

        var bytes: [UInt8] {
            switch self {
            case .ipv4(let bytes), .ipv6(let bytes): bytes
            }
        }

        var bitCount: Int { bytes.count * 8 }

        func hasSameFamily(as other: Self) -> Bool {
            switch (self, other) {
            case (.ipv4, .ipv4), (.ipv6, .ipv6): true
            default: false
            }
        }
    }

    private let path: String?
    private let lock = NSLock()
    private var snapshot: Snapshot?
    private var nextRefresh = Date.distantPast
    private(set) var lastError = ""

    init(path: String?) {
        let normalized = path?.trimmingCharacters(in: .whitespacesAndNewlines)
        self.path = normalized?.isEmpty == false ? normalized : nil
    }

    func authorize(
        remoteAddress: String?,
        apiKey: String?,
        validateAPIKey: Bool
    ) -> RuntimeAccessDecision {
        guard path != nil else { return .allowed }
        lock.lock()
        defer { lock.unlock() }
        refreshIfNeeded()
        guard let snapshot else { return .policyUnavailable }
        if snapshot.ipAllowlistEnabled,
           !Self.isIPAllowed(remoteAddress, patterns: snapshot.ipAllowlist) {
            return .ipNotAllowed
        }
        if validateAPIKey, snapshot.apiKeyEnabled,
           !Self.isKeyAllowed(apiKey, hashes: snapshot.keyHashes) {
            return .invalidAPIKey
        }
        return .allowed
    }

    private func refreshIfNeeded() {
        let now = Date()
        guard now >= nextRefresh, let path else { return }
        nextRefresh = now.addingTimeInterval(10)
        do {
            let data = try Data(contentsOf: URL(fileURLWithPath: path), options: [.mappedIfSafe])
            let decoded = try JSONDecoder().decode(PolicyFile.self, from: data)
            guard decoded.version == 1 else {
                throw AccessControlError.invalidSnapshot("不支援的策略版本")
            }
            let storedKeys = decoded.keys ?? []
            let rawPatterns = decoded.policy.ipAllowlist ?? []
            guard storedKeys.count <= 100, rawPatterns.count <= 256 else {
                throw AccessControlError.invalidSnapshot("策略項目數量超過限制")
            }
            let hashes = try storedKeys.map { key in
                guard let bytes = Self.decodeHex(key.hash), bytes.count == SHA256.byteCount else {
                    throw AccessControlError.invalidSnapshot("金鑰雜湊格式錯誤")
                }
                return bytes
            }
            let patterns = rawPatterns.map {
                $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            }
            guard patterns.allSatisfy(Self.isValidIPPattern) else {
                throw AccessControlError.invalidSnapshot("IP 白名單格式錯誤")
            }
            guard !decoded.policy.apiKeyEnabled || !hashes.isEmpty else {
                throw AccessControlError.invalidSnapshot("已啟用金鑰驗證，但沒有金鑰")
            }
            guard !decoded.policy.ipAllowlistEnabled || !patterns.isEmpty else {
                throw AccessControlError.invalidSnapshot("已啟用 IP 白名單，但沒有項目")
            }
            snapshot = Snapshot(
                apiKeyEnabled: decoded.policy.apiKeyEnabled,
                ipAllowlistEnabled: decoded.policy.ipAllowlistEnabled,
                ipAllowlist: patterns,
                keyHashes: hashes
            )
            lastError = ""
        } catch {
            snapshot = nil
            lastError = error.localizedDescription
        }
    }

    private static func isKeyAllowed(_ key: String?, hashes: [[UInt8]]) -> Bool {
        guard let key, !key.isEmpty else { return false }
        let candidate = Array(SHA256.hash(data: Data(key.utf8)))
        var matched = false
        for expected in hashes {
            var difference: UInt8 = 0
            for index in candidate.indices {
                difference |= candidate[index] ^ expected[index]
            }
            matched = matched || difference == 0
        }
        return matched
    }

    private static func isIPAllowed(_ remoteAddress: String?, patterns: [String]) -> Bool {
        guard let remoteAddress,
              let remote = parseIPAddress(remoteAddress) else { return false }
        let comparable = addressWithoutZone(remoteAddress).lowercased()
        for pattern in patterns {
            if pattern == "*" { return true }
            if pattern.contains("/") {
                if matchesCIDR(remote, pattern: pattern) { return true }
            } else if pattern.contains("*") {
                if wildcardMatch(comparable, pattern: pattern) { return true }
            } else if let exact = parseIPAddress(pattern),
                      remote.hasSameFamily(as: exact), remote.bytes == exact.bytes {
                return true
            }
        }
        return false
    }

    private static func isValidIPPattern(_ pattern: String) -> Bool {
        guard !pattern.isEmpty else { return false }
        if pattern == "*" { return true }
        if pattern.contains("/") {
            let parts = pattern.split(separator: "/", omittingEmptySubsequences: false)
            guard parts.count == 2,
                  let address = parseIPAddress(String(parts[0])),
                  let prefix = Int(parts[1]),
                  (0...address.bitCount).contains(prefix) else { return false }
            return true
        }
        if pattern.contains("*") {
            guard pattern.count <= 128 else { return false }
            return pattern.unicodeScalars.allSatisfy {
                CharacterSet(charactersIn: "0123456789abcdefABCDEF:.*").contains($0)
            }
        }
        return parseIPAddress(pattern) != nil
    }

    private static func matchesCIDR(_ remote: ParsedIPAddress, pattern: String) -> Bool {
        let parts = pattern.split(separator: "/", omittingEmptySubsequences: false)
        guard parts.count == 2,
              let network = parseIPAddress(String(parts[0])),
              remote.hasSameFamily(as: network),
              let prefix = Int(parts[1]),
              (0...remote.bitCount).contains(prefix) else { return false }
        let fullBytes = prefix / 8
        let remainingBits = prefix % 8
        guard Array(remote.bytes.prefix(fullBytes)) == Array(network.bytes.prefix(fullBytes)) else {
            return false
        }
        guard remainingBits > 0 else { return true }
        let mask = UInt8(0xff << (8 - remainingBits))
        return remote.bytes[fullBytes] & mask == network.bytes[fullBytes] & mask
    }

    private static func wildcardMatch(_ value: String, pattern: String) -> Bool {
        let value = Array(value)
        let pattern = Array(pattern)
        var valueIndex = 0
        var patternIndex = 0
        var wildcardIndex: Int?
        var wildcardValueIndex = 0
        while valueIndex < value.count {
            if patternIndex < pattern.count, pattern[patternIndex] == value[valueIndex] {
                valueIndex += 1
                patternIndex += 1
            } else if patternIndex < pattern.count, pattern[patternIndex] == "*" {
                wildcardIndex = patternIndex
                patternIndex += 1
                wildcardValueIndex = valueIndex
            } else if let wildcardIndex {
                patternIndex = wildcardIndex + 1
                wildcardValueIndex += 1
                valueIndex = wildcardValueIndex
            } else {
                return false
            }
        }
        while patternIndex < pattern.count, pattern[patternIndex] == "*" {
            patternIndex += 1
        }
        return patternIndex == pattern.count
    }

    private static func parseIPAddress(_ raw: String) -> ParsedIPAddress? {
        let value = addressWithoutZone(raw)
        var ipv4 = in_addr()
        if value.withCString({ inet_pton(AF_INET, $0, &ipv4) }) == 1 {
            return .ipv4(withUnsafeBytes(of: &ipv4) { Array($0) })
        }
        var ipv6 = in6_addr()
        guard value.withCString({ inet_pton(AF_INET6, $0, &ipv6) }) == 1 else {
            return nil
        }
        let bytes = withUnsafeBytes(of: &ipv6) { Array($0) }
        if bytes.prefix(10).allSatisfy({ $0 == 0 }), bytes[10] == 0xff, bytes[11] == 0xff {
            return .ipv4(Array(bytes.suffix(4)))
        }
        return .ipv6(bytes)
    }

    private static func addressWithoutZone(_ raw: String) -> String {
        var value = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if value.hasPrefix("["), value.hasSuffix("]") {
            value.removeFirst()
            value.removeLast()
        }
        if let zone = value.lastIndex(of: "%") {
            value = String(value[..<zone])
        }
        return value
    }

    private static func decodeHex(_ value: String) -> [UInt8]? {
        let characters = Array(value.lowercased())
        guard characters.count.isMultiple(of: 2) else { return nil }
        var result: [UInt8] = []
        result.reserveCapacity(characters.count / 2)
        var index = 0
        while index < characters.count {
            guard let byte = UInt8(String(characters[index...index + 1]), radix: 16) else { return nil }
            result.append(byte)
            index += 2
        }
        return result
    }
}

private enum AccessControlError: LocalizedError {
    case invalidSnapshot(String)

    var errorDescription: String? {
        switch self {
        case .invalidSnapshot(let message): "Tanpopo 安全策略無效：\(message)"
        }
    }
}
