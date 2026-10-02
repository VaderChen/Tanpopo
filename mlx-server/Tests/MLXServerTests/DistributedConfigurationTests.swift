import Foundation
import XCTest
@testable import MLXServer

final class DistributedConfigurationTests: XCTestCase {
    private func decode(_ value: String) throws -> DistributedConfiguration {
        try DistributedConfiguration.decode(Data(value.utf8))
    }

    func testRejectsIncompleteOrAmbiguousTopology() {
        for value in [
            #"{"version":1,"backend":"jaccl","nodes":[{},{}]}"#,
            #"{"version":1,"backend":"ring","nodes":[{"ringAddress":"127.0.0.1:5500"},{"ringAddress":"127.0.0.1:5500"}]}"#,
            #"{"version":1,"backend":"any","nodes":[{},{}]}"#,
            #"{"version":2,"backend":"ring","nodes":[]}"#
        ] { XCTAssertThrowsError(try decode(value)) }
    }

    func testRejectsSSHOptionsAndRelativeExecutables() throws {
        let base = #"{"version":1,"backend":"ring","nodes":[{"ringAddress":"127.0.0.1:5500"},{"ringAddress":"127.0.0.1:5501","ssh":"HOST","runtimePath":"BINARY","modelPath":"/models/model"}]}"#
        for host in ["-oProxyCommand=bad", "host;command", "host\ncommand"] {
            XCTAssertThrowsError(try decode(base.replacingOccurrences(of: "HOST", with: host).replacingOccurrences(of: "BINARY", with: "/bin/mlx-server")))
        }
        XCTAssertThrowsError(try decode(base.replacingOccurrences(of: "HOST", with: "mac-b").replacingOccurrences(of: "BINARY", with: "./mlx-server")))
        XCTAssertNoThrow(try decode(base.replacingOccurrences(of: "HOST", with: "user@mac-b").replacingOccurrences(of: "BINARY", with: "/opt/Tanpopo Runtime/mlx-server")))
    }

    func testWorkerOptionsCannotSilentlyRunSingleNode() {
        XCTAssertThrowsError(try ServerConfiguration.parse(["--distributed-rank", "1"]))
        XCTAssertThrowsError(try ServerConfiguration.parse(["--distributed-smoke"]))
        XCTAssertThrowsError(try ServerConfiguration.parse(["--distributed-parent-stdin"]))
    }

    func testLocalWorkerRequiresExplicitLoopbackTopology() throws {
        let value = #"{"version":1,"backend":"ring","nodes":[{"ringAddress":"127.0.0.1:5500"},{"ringAddress":"127.0.0.1:5501","launch":"local"}]}"#
        XCTAssertEqual(try decode(value).nodes[1].launchMode, .local)
        XCTAssertThrowsError(try decode(value.replacingOccurrences(of: "127.0.0.1", with: "192.168.1.20")))
        XCTAssertThrowsError(try decode(value.replacingOccurrences(of: #""launch":"local""#, with: #""launch":"local","ssh":"mac-b""#)))
        XCTAssertThrowsError(try decode(value.replacingOccurrences(of: #""launch":"local""#, with: #""launch":"unknown""#)))
        XCTAssertThrowsError(try decode(#"{"version":1,"backend":"jaccl","coordinator":"127.0.0.1:5500","nodes":[{"rdmaDevice":"rdma_en2"},{"rdmaDevice":"rdma_en3","launch":"local"}]}"#))
    }

    func testEndpointValidation() {
        XCTAssertTrue(DistributedConfiguration.validEndpoint("192.168.20.1:5500"))
        for value in ["0.0.0.0:5500", "127.0.0.1:0", "127.0.0.1:65536", "999.0.0.1:5500", "localhost:5500", "127.0.0.1:5500:1"] {
            XCTAssertFalse(DistributedConfiguration.validEndpoint(value), value)
        }
    }

    func testManagedServerCanMonitorParentForEitherRank() throws {
        let value = #"{"version":1,"backend":"ring","nodes":[{"ringAddress":"127.0.0.1:5500"},{"ringAddress":"127.0.0.1:5501"}]}"#
        for rank in [0, 1] {
            let configuration = try ServerConfiguration.parse([
                "--distributed-config-base64", Data(value.utf8).base64EncodedString(),
                "--distributed-rank", String(rank), "--distributed-parent-stdin", "--distributed-smoke"
            ])
            XCTAssertTrue(configuration.distributedParentStdin)
            XCTAssertEqual(configuration.distributedRank, rank)
        }
    }
    func testManualRingSupportsMultipleNodesAndRejectsDuplicateEndpoints() throws {
        for count in [2, 3, 8] {
            let nodes = (0..<count).map { ["ringAddress": "127.0.0.1:\(5500 + $0)", "launch": "manual"] }
            let data = try JSONSerialization.data(withJSONObject: ["version": 1, "backend": "ring", "nodes": nodes])
            XCTAssertEqual(try DistributedConfiguration.decode(data).nodes.count, count)
            let configuration = try ServerConfiguration.parse(["--distributed-config-base64", data.base64EncodedString(),
                "--distributed-rank", String(count - 1), "--distributed-parent-stdin", "--distributed-smoke"])
            XCTAssertEqual(configuration.distributedRank, count - 1)
        }
        for count in [0, 1, 9] {
            let nodes = (0..<count).map { ["ringAddress": "127.0.0.1:\(5500 + $0)"] }
            let data = try JSONSerialization.data(withJSONObject: ["version": 1, "backend": "ring", "nodes": nodes])
            XCTAssertThrowsError(try DistributedConfiguration.decode(data))
        }
        let duplicate = #"{"version":1,"backend":"ring","nodes":[{"ringAddress":"127.0.0.1:5500"},{"ringAddress":"127.0.0.1:5501"},{"ringAddress":"127.0.0.1:5501"}]}"#
        XCTAssertThrowsError(try decode(duplicate))
        let automatic = duplicate.replacingOccurrences(of: #""ringAddress":"127.0.0.1:5501"}]"#, with: #""ringAddress":"127.0.0.1:5502","launch":"local"}]"#)
        XCTAssertThrowsError(try decode(automatic))
    }

}
