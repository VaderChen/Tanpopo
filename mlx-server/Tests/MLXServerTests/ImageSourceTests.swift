import Foundation
import XCTest
@testable import MLXServer

private final class ImageProtocolFixture: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let path = request.url!.path
        if path == "/hold" { return }
        let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: "HTTP/1.1",
            headerFields: path == "/length" ? ["Content-Length": "65536"] : nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        let chunks = path == "/small" ? 1 : 64
        for _ in 0..<chunks { client?.urlProtocol(self, didLoad: Data(repeating: 1, count: 64)) }
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

final class ImageSourceTests: XCTestCase {
    func testDefaultRejectsLocalPathsAndRemoteURLs() async {
        for value in ["/tmp/image.png", "file:///tmp/image.png", "http://127.0.0.1/private", "https://example.com/image.png"] {
            do {
                _ = try await ImageSource.load(value, maximumBytes: 1024, allowedOrigins: [])
                XCTFail("接受了未授權來源：\(value)")
            } catch {}
        }
    }

    func testOriginPolicyRejectsRedirectToOtherOriginOrCredentials() throws {
        let origins: Set<String> = [try XCTUnwrap(ImageSource.origin("https://images.example.com"))]
        try ImageSource.validate(URL(string: "https://images.example.com/a.png")!, allowedOrigins: origins)
        for value in ["http://images.example.com/a.png", "https://images.example.com:8443/a.png",
                      "https://images.example.com.evil.test/a.png", "https://127.0.0.1/a.png",
                      "https://user:password@images.example.com/a.png"] {
            XCTAssertThrowsError(try ImageSource.validate(URL(string: value)!, allowedOrigins: origins))
        }
        for value in ["https://images.example.com/path", "https://images.example.com?q=x", "http://example.com", "file:///tmp"] {
            XCTAssertNil(ImageSource.origin(value))
        }
    }

    func testInlineImageBoundsBeforeAndAfterDecoding() throws {
        XCTAssertEqual(try ImageSource.dataURL("data:image/png;base64,AQID", maximumBytes: 3), Data([1,2,3]))
        XCTAssertThrowsError(try ImageSource.dataURL("data:image/png;base64,AQID", maximumBytes: 2))
        XCTAssertThrowsError(try ImageSource.dataURL("data:image/png;base64,"+String(repeating:"A", count: 4096), maximumBytes: 3))
        XCTAssertThrowsError(try ImageSource.dataURL("data:text/plain;base64,AQID", maximumBytes: 3))
        XCTAssertThrowsError(try ImageSource.dataURL("data:image/png;base64,!!!!", maximumBytes: 3))
    }

    func testRemoteDownloadCountsChunksWithoutContentLength() async throws {
        for path in ["/small", "/length", "/stream"] {
            let configuration = URLSessionConfiguration.ephemeral
            configuration.protocolClasses = [ImageProtocolFixture.self]
            let downloader = BoundedImageDownload(maximumBytes: 128, allowedOrigins: ["https://fixture.test:443"], configuration: configuration)
            do {
                let data = try await downloader.load(URL(string:"https://fixture.test"+path)!)
                XCTAssertEqual(path, "/small")
                XCTAssertEqual(data.count, 64)
            } catch {
                XCTAssertNotEqual(path, "/small")
                guard case APIError.imageTooLarge = error else { XCTFail("非預期錯誤：\(error)"); continue }
            }
        }
    }

    func testCancellationStopsPendingDownload() async {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [ImageProtocolFixture.self]
        let downloader = BoundedImageDownload(maximumBytes: 128, allowedOrigins: ["https://fixture.test:443"], configuration: configuration)
        let task = Task { try await downloader.load(URL(string: "https://fixture.test/hold")!) }
        try? await Task.sleep(for: .milliseconds(20))
        task.cancel()
        do { _ = try await task.value; XCTFail("取消後仍成功") }
        catch { XCTAssertTrue(error is CancellationError) }
    }

    func testContextLimitIsIndependentOfRotatingCache() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let configuration = try ServerConfiguration.parse(["--model", directory.path, "--context-size", "512"])
        XCTAssertEqual(configuration.contextSize, 512)
        XCTAssertNil(configuration.maxKVSize)
        XCTAssertThrowsError(try ServerConfiguration.parse(["--model", directory.path, "--context-size", "0"]))
    }
}
