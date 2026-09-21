import Foundation
import XCTest
@testable import MLXServer

final class TemporaryImagesTests: XCTestCase {
    private enum FixtureError: Error { case failed }

    func testCleanupAfterLaterImageFails() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        do {
            let files = TemporaryImages(root: root)
            defer { files.removeAll() }
            let image = try files.store(Data([1, 2, 3]), maximumBytes: 10)
            XCTAssertTrue(FileManager.default.fileExists(atPath: image.path))
            _ = try await ImageSource.load("file:///untrusted", maximumBytes: 10, allowedOrigins: [])
            XCTFail("預期後續圖片失敗")
        } catch {}
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: root.path), [])
    }

    func testPrivatePermissionsAndScopeCleanup() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        func prepare() throws {
            let files = TemporaryImages(root: root)
            let image = try files.store(Data([1]), maximumBytes: 10)
            let fileMode = try FileManager.default.attributesOfItem(atPath: image.path)[.posixPermissions] as? NSNumber
            let dirMode = try FileManager.default.attributesOfItem(atPath: image.deletingLastPathComponent().path)[.posixPermissions] as? NSNumber
            XCTAssertEqual(fileMode?.intValue, 0o600)
            XCTAssertEqual(dirMode?.intValue, 0o700)
            throw FixtureError.failed
        }
        XCTAssertThrowsError(try prepare())
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: root.path), [])
    }

    func testTextOnlyRequestAndOversizedImageCreateNoFiles() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let files = TemporaryImages(root: root)
        XCTAssertThrowsError(try files.store(Data(repeating: 1, count: 11), maximumBytes: 10))
        files.removeAll()
        XCTAssertFalse(FileManager.default.fileExists(atPath: root.path))
    }
}
