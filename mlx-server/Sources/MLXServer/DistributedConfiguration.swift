import Foundation
import Darwin

enum DistributedError: LocalizedError {
    case invalid(String)
    var errorDescription: String? {
        switch self { case .invalid(let message): "分散式 Runtime：\(message)" }
    }
}

/// TCP Ring 支援 2–8 個節點；JACCL 仍採雙節點裝置拓樸。
struct DistributedConfiguration: Codable, Sendable {
    struct Node: Codable, Sendable {
        enum Launch: String, Codable, Sendable { case manual, ssh, local }
        var launch: Launch?
        var rdmaDevice: String?
        var ringAddress: String?
        var ssh: String?
        var runtimePath: String?
        var modelPath: String?
        var launchMode: Launch { launch ?? (ssh == nil ? .manual : .ssh) }
    }
    var version: Int
    var backend: String
    var coordinator: String?
    var nodes: [Node]
    var operationTimeoutSeconds: Int?
    var startupTimeoutSeconds: Int?

    var operationTimeout: Int { operationTimeoutSeconds ?? 120 }
    var startupTimeout: Int { startupTimeoutSeconds ?? 600 }

    static func decode(_ data: Data) throws -> Self {
        guard data.count <= 65_536 else { throw DistributedError.invalid("設定檔超過 64 KiB。") }
        let result = try JSONDecoder().decode(Self.self, from: data)
        try result.validate()
        return result
    }

    func validate() throws {
        guard version == 1, (2...8).contains(nodes.count), ["jaccl", "ring"].contains(backend),
            backend != "jaccl" || nodes.count == 2 else {
            throw DistributedError.invalid("需要 version=1；Ring 支援 2–8 個節點，JACCL 支援兩個節點。")
        }
        guard (5...3600).contains(operationTimeout), (10...7200).contains(startupTimeout) else {
            throw DistributedError.invalid("通訊逾時需為 5–3600 秒，啟動逾時需為 10–7200 秒。")
        }
        if backend == "jaccl" {
            guard let coordinator, Self.validEndpoint(coordinator),
                nodes.allSatisfy({ node in
                    guard let device = node.rdmaDevice else { return false }
                    return device.range(of: "^rdma_[A-Za-z0-9_]+$", options: .regularExpression) != nil
                }) else {
                throw DistributedError.invalid("JACCL 需要 IPv4:port coordinator 與每個節點的 rdmaDevice。")
            }
        } else {
            guard nodes.allSatisfy({ $0.ringAddress.map(Self.validEndpoint) == true }),
                Set(nodes.compactMap(\.ringAddress)).count == nodes.count else {
                throw DistributedError.invalid("Ring 需要各不相同的 IPv4:port ringAddress。")
            }
        }
        guard nodes[0].launchMode == .manual, nodes[0].ssh == nil else {
            throw DistributedError.invalid("第一個節點由 Tanpopo 或 CLI 啟動，不能指定 worker 啟動方式。")
        }
        for worker in nodes.dropFirst() {
            guard nodes.count == 2 || worker.launchMode == .manual else {
                throw DistributedError.invalid("多節點 Ring 由各台 Tanpopo Server 管理；三個以上節點需使用 manual 啟動。")
            }
            for path in [worker.runtimePath, worker.modelPath].compactMap({ $0 }) {
                guard path.hasPrefix("/"), !path.contains("\0"), !path.contains("\n"), !path.contains("\r") else {
                    throw DistributedError.invalid("Worker runtimePath／modelPath 必須是有效絕對路徑。")
                }
            }
            switch worker.launchMode {
            case .ssh:
                guard let ssh = worker.ssh else { throw DistributedError.invalid("SSH worker 缺少 ssh host。") }
                guard !ssh.hasPrefix("-"),
                    ssh.range(of: "^[A-Za-z0-9_.@-]+$", options: .regularExpression) != nil,
                    worker.runtimePath != nil, worker.modelPath != nil else {
                    throw DistributedError.invalid("SSH 節點需要有效 host，以及絕對 runtimePath／modelPath。")
                }
            case .local:
                guard backend == "ring", worker.ssh == nil,
                    nodes.allSatisfy({ $0.ringAddress?.hasPrefix("127.") == true }) else {
                    throw DistributedError.invalid("本機 worker 僅支援 loopback TCP Ring，不能混用 SSH 或 JACCL。")
                }
            case .manual:
                guard worker.ssh == nil else { throw DistributedError.invalid("手動啟動不能同時指定 ssh。") }
            }
        }
    }

    static func validEndpoint(_ value: String) -> Bool {
        let parts = value.split(separator: ":", omittingEmptySubsequences: false)
        guard parts.count == 2, let port = Int(parts[1]), (1024...65535).contains(port) else { return false }
        var address = in_addr()
        return String(parts[0]).withCString { inet_pton(AF_INET, $0, &address) } == 1
            && parts[0] != "0.0.0.0"
    }

    /// 所有 MLX 初始化之前呼叫；只設定明確選用的後端，不接受外部殘留變數改變拓樸。
    func configureEnvironment(rank: Int) throws -> URL {
        guard (0..<nodes.count).contains(rank) else { throw DistributedError.invalid("rank 超出範圍。") }
        if backend == "jaccl" {
            guard #available(macOS 26.2, *) else { throw DistributedError.invalid("JACCL 需要 macOS 26.2 以上。") }
        }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("tanpopo-rdma-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false,
            attributes: [.posixPermissions: 0o700])
        let file = directory.appendingPathComponent("hosts.json")
        let value: Any
        if backend == "jaccl" {
            value = [[NSNull(), nodes[0].rdmaDevice!] as [Any], [nodes[1].rdmaDevice!, NSNull()] as [Any]]
        } else {
            value = nodes.map { [$0.ringAddress!] }
        }
        do {
            try JSONSerialization.data(withJSONObject: value).write(to: file, options: .atomic)
        } catch {
            try? FileManager.default.removeItem(at: directory)
            throw error
        }
        for key in ["MLX_HOSTFILE", "MLX_IBV_DEVICES", "MLX_JACCL_COORDINATOR", "MLX_JACCL_RING", "MLX_METAL_FAST_SYNCH"] {
            unsetenv(key)
        }
        setenv("MLX_RANK", String(rank), 1)
        if backend == "jaccl" {
            setenv("MLX_IBV_DEVICES", file.path, 1)
            setenv("MLX_JACCL_COORDINATOR", coordinator!, 1)
        } else {
            setenv("MLX_HOSTFILE", file.path, 1)
        }
        return directory
    }
}
