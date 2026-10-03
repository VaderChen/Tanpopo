import XCTest
import MLX
@testable import MLXServer

final class DistributedZeroBuffersTests: XCTestCase {
    func testBuffersReuseOnlyMatchingShapeAndDType() {
        let buffers = DistributedZeroBuffers(byteLimit: 1024)
        let first = buffers.zeros(shape: [8], dtype: .float32)
        eval(first)
        XCTAssertTrue(first === buffers.zeros(shape: [8], dtype: .float32))
        XCTAssertFalse(first === buffers.zeros(shape: [2, 4], dtype: .float32))
        XCTAssertFalse(first === buffers.zeros(shape: [8], dtype: .float16))
        XCTAssertEqual(first.asArray(Float.self), Array(repeating: 0, count: 8))
        XCTAssertEqual(buffers.retainedBytes, 80)
    }

    func testEvictionBoundsBytesAndEntriesWithoutChangingReturnedArrays() {
        let buffers = DistributedZeroBuffers(byteLimit: 64, entryLimit: 2)
        let first = buffers.zeros(shape: [8], dtype: .float32)
        let second = buffers.zeros(shape: [2, 4], dtype: .float32)
        _ = buffers.zeros(shape: [8], dtype: .float32)
        _ = buffers.zeros(shape: [4, 2], dtype: .float32)
        XCTAssertTrue(first === buffers.zeros(shape: [8], dtype: .float32))
        XCTAssertEqual(buffers.retainedBytes, 64)
        XCTAssertFalse(second === buffers.zeros(shape: [2, 4], dtype: .float32))
        let oversized = buffers.zeros(shape: [32], dtype: .float32)
        XCTAssertLessThanOrEqual(buffers.retainedBytes, 64)
        XCTAssertFalse(oversized === buffers.zeros(shape: [32], dtype: .float32))
        eval(first, second, oversized)
        XCTAssertTrue(all(first .== 0).item(Bool.self))
        XCTAssertTrue(all(second .== 0).item(Bool.self))
        XCTAssertTrue(all(oversized .== 0).item(Bool.self))
        let disabled = DistributedZeroBuffers(entryLimit: 0)
        _ = disabled.zeros(shape: [8], dtype: .float32)
        XCTAssertEqual(disabled.retainedBytes, 0)
    }
}
