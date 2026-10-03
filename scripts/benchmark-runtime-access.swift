import Foundation

// 與 AccessControl.swift 一起用 swiftc -O 編譯；不啟動模型或使用 Python。
@main
struct RuntimeAccessBenchmark {
    static func main() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: url) }
        var patterns = (0..<255).map { index -> String in
            switch index % 4 {
            case 0: return "10.0.\(index).1"
            case 1: return "10.\(index).0.0/16"
            case 2: return "2001:db8:\(index)::/48"
            default: return "192.0.\(index).*"
            }
        }
        patterns.append("203.0.113.9/32")
        try JSONSerialization.data(withJSONObject: ["version": 1, "keys": [], "policy": [
            "api_key_enabled": false, "ip_allowlist_enabled": true, "ip_allowlist": patterns
        ]] as [String: Any]).write(to: url)
        let control = RuntimeAccessControl(path: url.path)
        precondition(control.authorize(remoteAddress: "203.0.113.9", apiKey: nil, validateAPIKey: false) == .allowed)
        var samples: [Double] = []
        let iterations = 1000
        for _ in 0..<5 {
            let start = DispatchTime.now().uptimeNanoseconds
            for _ in 0..<iterations {
                precondition(control.authorize(remoteAddress: "203.0.113.9", apiKey: nil, validateAPIKey: false) == .allowed)
            }
            samples.append(Double(DispatchTime.now().uptimeNanoseconds - start) / Double(iterations))
        }
        let result: [String: Any] = ["patterns": patterns.count, "iterations_per_sample": iterations,
            "samples_ns_per_call": samples, "median_ns_per_call": samples.sorted()[2], "decisions_verified": true]
        print(String(data: try JSONSerialization.data(withJSONObject: result, options: [.sortedKeys]), encoding: .utf8)!)
    }
}
