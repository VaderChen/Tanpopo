import Foundation

/// 每個請求擁有自己的私有暫存目錄；錯誤、取消與正常結束使用同一清理路徑。
final class TemporaryImages {
    private let root: URL
    private var directory: URL?

    init(root: URL = FileManager.default.temporaryDirectory) {
        self.root = root
    }

    deinit { removeAll() }

    func store(_ data: Data, maximumBytes: Int) throws -> URL {
        guard data.count <= maximumBytes else { throw APIError.imageTooLarge }
        if directory == nil {
            let path = root.appendingPathComponent("tanpopo-images-\(UUID().uuidString)", isDirectory: true)
            try FileManager.default.createDirectory(
                at: path, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
            directory = path
        }
        let url = directory!.appendingPathComponent("\(UUID().uuidString).image")
        guard FileManager.default.createFile(
            atPath: url.path, contents: data, attributes: [.posixPermissions: 0o600]) else {
            throw APIError.invalidImageURL("無法保存暫存圖片")
        }
        return url
    }

    func removeAll() {
        guard let directory else { return }
        do {
            try FileManager.default.removeItem(at: directory)
            self.directory = nil
        } catch {
            // 保留路徑，讓 deinit 可以再次清理。
        }
    }
}
