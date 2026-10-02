import Foundation
import Darwin

/// 本機與 SSH worker 共用 stdin 生命週期管道；主程序被 SIGKILL 時仍由 EOF 回收。
final class DistributedWorkerProcess: @unchecked Sendable {
    private let process = Process()
    private let input = Pipe()
    private let lock = NSLock()
    private var stopping = false
    private var signals: [DispatchSourceSignal] = []

    init(configuration: DistributedConfiguration, modelPath: String, smoke: Bool) throws {
        let node = configuration.nodes[1]
        guard let executable = node.runtimePath ?? Bundle.main.executableURL?.path else {
            throw DistributedError.invalid("無法取得 worker Runtime 執行檔。")
        }
        let model = node.modelPath ?? modelPath
        let data = try JSONEncoder().encode(configuration)
        var command = [executable, "--model", model, "--distributed-config-base64", data.base64EncodedString(),
            "--distributed-rank", "1", "--distributed-parent-stdin"]
        if smoke { command.append("--distributed-smoke") }
        switch node.launchMode {
        case .local:
            process.executableURL = URL(fileURLWithPath: executable)
            process.arguments = Array(command.dropFirst())
        case .ssh:
            guard let host = node.ssh else { throw DistributedError.invalid("SSH worker 缺少 host。") }
            process.executableURL = URL(fileURLWithPath: "/usr/bin/ssh")
            process.arguments = ["-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
                "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
                host, "exec " + command.map(Self.quote).joined(separator: " ")]
        case .manual:
            throw DistributedError.invalid("手動 worker 不應由 Runtime 自動啟動。")
        }
        process.standardInput = input
        process.standardOutput = FileHandle.standardError
        process.standardError = FileHandle.standardError
        process.terminationHandler = { [weak self] process in
            guard let self, !self.lock.withLock({ self.stopping }) else { return }
            if smoke && process.terminationStatus == 0 { return }
            fputs("分散式 worker 已離線（\(node.launchMode.rawValue) exit=\(process.terminationStatus)）。\n", stderr)
            _exit(70)
        }
        try process.run()
        fputs("TANPOPO_DISTRIBUTED_WORKER launch=\(node.launchMode.rawValue) pid=\(process.processIdentifier)\n", stderr)
        // Foundation 的 Pipe 在父端也保留讀端；及早關閉，讓 EOF 行為可預期。
        try? input.fileHandleForReading.close()
        for number in [SIGTERM, SIGINT, SIGHUP] {
            signal(number, SIG_IGN)
            let source = DispatchSource.makeSignalSource(signal: number, queue: .global(qos: .utility))
            source.setEventHandler { [weak self] in self?.stop(); _exit(128 + number) }
            source.resume()
            signals.append(source)
        }
    }

    func stop() {
        let shouldStop = lock.withLock {
            guard !stopping else { return false }
            stopping = true
            return true
        }
        guard shouldStop else { return }
        try? input.fileHandleForWriting.close()
        // 回收本機子程序或 SSH；stdin EOF 與通訊 lease 處理遠端關閉及斷線。
        if process.isRunning { process.terminate() }
    }

    static func quote(_ value: String) -> String { "'" + value.replacingOccurrences(of: "'", with: "'\"'\"'") + "'" }

    static func monitorParentInput() {
        Thread.detachNewThread {
            var byte: UInt8 = 0
            while true {
                let count = read(STDIN_FILENO, &byte, 1)
                if count < 0 && errno == EINTR { continue }
                if count <= 0 { _exit(0) }
            }
        }
    }
}
