import Foundation

/// API 圖片只能使用內嵌資料，或管理者明確授權的 HTTPS origin。
/// Origin 授權代表信任該主機及其 DNS；不接受來自 API 的任意 URL 或本機路徑。
enum ImageSource {
    static func origin(_ value: String) -> String? {
        guard let url = URL(string: value), url.scheme?.lowercased() == "https",
              let host = url.host?.lowercased(), !host.isEmpty,
              url.user == nil, url.password == nil, url.fragment == nil,
              url.query == nil, url.path.isEmpty || url.path == "/",
              url.port == nil || (1...65535).contains(url.port!) else { return nil }
        return "https://\(host):\(url.port ?? 443)"
    }

    static func validate(_ url: URL, allowedOrigins: Set<String>) throws {
        guard url.scheme?.lowercased() == "https", let host = url.host?.lowercased(),
              url.user == nil, url.password == nil,
              allowedOrigins.contains("https://\(host):\(url.port ?? 443)") else {
            throw APIError.invalidImageURL("圖片來源未授權；請使用 data URL，或由管理者設定 --image-allowed-origin。")
        }
    }

    static func dataURL(_ value: String, maximumBytes: Int) throws -> Data {
        guard value.hasPrefix("data:image/"), let comma = value.firstIndex(of: ","),
              value[..<comma].hasSuffix(";base64") else {
            throw APIError.invalidImageURL("無效的圖片 data URL")
        }
        let encoded = value[value.index(after: comma)...]
        // 解碼前限制表示長度；解碼後仍檢查實際位元組數。
        guard encoded.utf8.count <= ((maximumBytes + 2) / 3) * 4 else { throw APIError.imageTooLarge }
        guard let data = Data(base64Encoded: String(encoded)) else { throw APIError.invalidImageURL("無效的 Base64 圖片") }
        guard data.count <= maximumBytes else { throw APIError.imageTooLarge }
        return data
    }

    static func load(_ value: String, maximumBytes: Int, allowedOrigins: Set<String>) async throws -> Data {
        if value.hasPrefix("data:") { return try dataURL(value, maximumBytes: maximumBytes) }
        guard let url = URL(string: value) else { throw APIError.invalidImageURL("無效的圖片 URL") }
        try validate(url, allowedOrigins: allowedOrigins)
        return try await BoundedImageDownload(maximumBytes: maximumBytes, allowedOrigins: allowedOrigins).load(url)
    }
}

// Delegate 在每個 chunk 到達時計數；不使用先讀完整回應的 data(from:)。
// NSLock 保護取消回呼、URLSession delegate 與 continuation 的單次完成。
final class BoundedImageDownload: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private let maximumBytes: Int
    private let allowedOrigins: Set<String>
    private let configuration: URLSessionConfiguration
    private let lock = NSLock()
    private var buffer = Data()
    private var continuation: CheckedContinuation<Data, Error>?
    private var session: URLSession?
    private var task: URLSessionDataTask?
    private var finished = false

    init(maximumBytes: Int, allowedOrigins: Set<String>, configuration: URLSessionConfiguration = .ephemeral) {
        self.configuration = configuration
        self.maximumBytes = maximumBytes
        self.allowedOrigins = allowedOrigins
    }

    func load(_ url: URL) async throws -> Data {
        try ImageSource.validate(url, allowedOrigins: allowedOrigins)
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                lock.lock()
                if finished {
                    lock.unlock()
                    continuation.resume(throwing: CancellationError())
                    return
                }
                self.continuation = continuation
                let configuration = self.configuration
                configuration.timeoutIntervalForRequest = 15
                configuration.timeoutIntervalForResource = 30
                configuration.httpCookieStorage = nil
                configuration.urlCredentialStorage = nil
                configuration.urlCache = nil
                configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
                let session = URLSession(configuration: configuration, delegate: self, delegateQueue: nil)
                self.session = session
                var request = URLRequest(url: url)
                request.setValue("image/*", forHTTPHeaderField: "Accept")
                let task = session.dataTask(with: request)
                self.task = task
                task.resume()
                lock.unlock()
            }
        } onCancel: {
            self.finish(.failure(CancellationError()))
        }
    }

    private func finish(_ result: Result<Data, Error>) {
        lock.lock()
        guard !finished else { lock.unlock(); return }
        finished = true
        let continuation = self.continuation
        self.continuation = nil
        let session = self.session
        self.session = nil
        self.task = nil
        buffer = Data()
        lock.unlock()
        session?.invalidateAndCancel()
        continuation?.resume(with: result)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask,
                    didReceive response: URLResponse,
                    completionHandler: @escaping @Sendable (URLSession.ResponseDisposition) -> Void) {
        guard let response = response as? HTTPURLResponse, (200...299).contains(response.statusCode) else {
            completionHandler(.cancel)
            finish(.failure(APIError.invalidImageURL("圖片伺服器未回傳成功狀態")))
            return
        }
        if response.expectedContentLength > Int64(maximumBytes) {
            completionHandler(.cancel)
            finish(.failure(APIError.imageTooLarge))
            return
        }
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        lock.lock()
        guard !finished else { lock.unlock(); return }
        if data.count > maximumBytes - buffer.count {
            lock.unlock()
            finish(.failure(APIError.imageTooLarge))
            return
        }
        buffer.append(data)
        lock.unlock()
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        if let error { finish(.failure(error)); return }
        lock.lock()
        let result = buffer
        lock.unlock()
        finish(.success(result))
    }

    func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                    completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        do {
            guard let url = request.url else { throw APIError.invalidImageURL("無效的圖片重新導向") }
            try ImageSource.validate(url, allowedOrigins: allowedOrigins)
            completionHandler(request)
        } catch {
            completionHandler(nil)
            finish(.failure(error))
        }
    }
}
