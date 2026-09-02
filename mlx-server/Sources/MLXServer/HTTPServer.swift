@preconcurrency import NIOCore
@preconcurrency import NIOHTTP1
@preconcurrency import NIOPosix
import Foundation
import MLXLMCommon

final class HTTPServer {
    private let configuration: ServerConfiguration
    private let router: APIRouter
    private let accessControl: RuntimeAccessControl

    init(configuration: ServerConfiguration, router: APIRouter) {
        self.configuration = configuration
        self.router = router
        self.accessControl = RuntimeAccessControl(path: configuration.accessControlPath)
    }

    func run() async throws {
        let group = MultiThreadedEventLoopGroup(numberOfThreads: System.coreCount)
        do {
            let router = router
            let accessControl = accessControl
            let maximumRequestBytes = configuration.maximumRequestBytes
            let bootstrap = ServerBootstrap(group: group)
                .serverChannelOption(ChannelOptions.backlog, value: 128)
                .serverChannelOption(ChannelOptions.socketOption(.so_reuseaddr), value: 1)
                .childChannelInitializer { channel in
                    // 預設 pipelining assistance 會在回應完成前停止 socket read，
                    // 導致長 Prefill／非串流期間無法及時收到客戶端斷線。
                    // 改由下方 handler 做有界排隊，維持讀取與 HTTP 回應順序。
                    channel.pipeline.configureHTTPServerPipeline(
                        withPipeliningAssistance: false,
                        withErrorHandling: false
                    ).flatMap {
                        channel.pipeline.addHandler(
                            HTTPRequestHandler(
                                router: router,
                                accessControl: accessControl,
                                maximumRequestBytes: maximumRequestBytes
                            )
                        )
                    }
                }
                .childChannelOption(ChannelOptions.socketOption(.so_reuseaddr), value: 1)
                .childChannelOption(ChannelOptions.allowRemoteHalfClosure, value: false)
                .childChannelOption(ChannelOptions.maxMessagesPerRead, value: 1)

            let channel = try await bootstrap.bind(
                host: configuration.host,
                port: configuration.port
            ).get()
            print("mlx-server listening at http://\(configuration.host):\(configuration.port)")
            try await channel.closeFuture.get()
            try await group.shutdownGracefully()
        } catch {
            try? await group.shutdownGracefully()
            throw error
        }
    }
}

private final class HTTPRequestHandler: ChannelInboundHandler, @unchecked Sendable {
    typealias InboundIn = HTTPServerRequestPart
    typealias OutboundOut = HTTPServerResponsePart

    private struct RequestState {
        var head: HTTPRequestHead
        var body: ByteBuffer
        var tooLarge = false
    }

    private let router: APIRouter
    private let accessControl: RuntimeAccessControl
    private let maximumRequestBytes: Int
    private var state: RequestState?
    // 此上限僅限制單一 HTTP/1 連線的等待佇列，與多人生成名額無關。
    private let maximumPendingRequests = 4
    private var pendingRequests: [RequestState] = []
    private var pendingBodyBytes = 0
    private var isClosing = false
    private var requestTask: Task<Void, Never>?
    private var responseStreamTask: Task<Void, Never>?
    private var responseHeartbeat: RepeatedTask?
    private var requestID: UUID?
    private var requestCancellation: GenerationCancellation?

    init(router: APIRouter, accessControl: RuntimeAccessControl, maximumRequestBytes: Int) {
        self.router = router
        self.accessControl = accessControl
        self.maximumRequestBytes = maximumRequestBytes
    }

    func channelRead(context: ChannelHandlerContext, data: NIOAny) {
        guard !isClosing else { return }
        switch unwrapInboundIn(data) {
        case .head(let head):
            guard pendingRequests.count < maximumPendingRequests else {
                cancelActiveRequest(reason: "pipeline_request_limit")
                context.close(promise: nil)
                return
            }
            state = RequestState(
                head: head,
                body: context.channel.allocator.buffer(capacity: 0)
            )
        case .body(var buffer):
            guard var current = state else { return }
            guard !current.tooLarge else { return }
            if current.body.readableBytes + buffer.readableBytes > maximumRequestBytes {
                current.tooLarge = true
                current.body = context.channel.allocator.buffer(capacity: 0)
            } else if pendingBodyBytes + current.body.readableBytes + buffer.readableBytes
                > maximumRequestBytes
            {
                cancelActiveRequest(reason: "pipeline_buffer_limit")
                context.close(promise: nil)
                return
            } else {
                current.body.writeBuffer(&buffer)
            }
            state = current
        case .end:
            guard let current = state else { return }
            state = nil
            pendingRequests.append(current)
            pendingBodyBytes += current.body.readableBytes
            startNextRequest(channel: context.channel)
        }
    }

    /// 僅在 NIO event loop 呼叫；同一連線的後續請求不得覆寫／取消目前工作。
    private func startNextRequest(channel: Channel) {
        guard !isClosing, channel.isActive, requestID == nil, !pendingRequests.isEmpty else { return }
        let current = pendingRequests.removeFirst()
        pendingBodyBytes -= current.body.readableBytes
        let remoteAddress = channel.remoteAddress?.ipAddress
        let cancellation = GenerationCancellation()
        let requestID = cancellation.id
        self.requestID = requestID
        requestCancellation = cancellation
        // ChannelHandlerContext 不跨執行緒，只將純值與 thread-safe Channel 交給 Task。
        requestTask = Task { @MainActor in
            let response = await withTaskCancellationHandler {
                await GenerationSafety.$cancellation.withValue(cancellation) {
                    await self.process(current, remoteAddress: remoteAddress)
                }
            } onCancel: {
                cancellation.cancel()
            }
            guard !Task.isCancelled else {
                cancellation.cancel()
                return
            }
            channel.eventLoop.execute {
                guard self.requestID == requestID, channel.isActive else {
                    cancellation.cancel()
                    return
                }
                self.requestTask = nil
                self.write(
                    response,
                    version: current.head.version,
                    keepAlive: current.head.isKeepAlive,
                    channel: channel,
                    requestID: requestID,
                    cancellation: cancellation
                )
            }
        }
    }

    func channelInactive(context: ChannelHandlerContext) {
        cancelActiveRequest(reason: "connection_closed")
        context.fireChannelInactive()
    }

    func handlerRemoved(context: ChannelHandlerContext) {
        cancelActiveRequest(reason: "handler_removed")
    }

    func errorCaught(context: ChannelHandlerContext, error: Error) {
        cancelActiveRequest(reason: "connection_error")
        context.close(promise: nil)
    }

    /// 只取消這條連線目前對應的請求，不影響其他連線／使用者。
    private func cancelActiveRequest(reason: String) {
        isClosing = true
        state = nil
        pendingRequests.removeAll()
        pendingBodyBytes = 0
        responseHeartbeat?.cancel()
        responseHeartbeat = nil
        if let cancellation = requestCancellation {
            fputs("http request cancelled request_id=\(cancellation.id.uuidString) reason=\(reason)\n", stderr)
        }
        requestCancellation?.cancel()
        requestCancellation = nil
        requestID = nil
        requestTask?.cancel()
        requestTask = nil
        responseStreamTask?.cancel()
        responseStreamTask = nil
    }

    private func finishRequest(_ id: UUID, channel: Channel, keepAlive: Bool) {
        guard requestID == id else { return }
        responseHeartbeat?.cancel()
        responseHeartbeat = nil
        requestID = nil
        requestCancellation = nil
        requestTask = nil
        responseStreamTask = nil
        if keepAlive && channel.isActive {
            startNextRequest(channel: channel)
        } else {
            cancelActiveRequest(reason: "response_closed")
            channel.close(promise: nil)
        }
    }

    private func process(
        _ state: RequestState,
        remoteAddress: String?
    ) async -> HTTPResponse {
        let path = String(
            state.head.uri.split(separator: "?", maxSplits: 1).first
                ?? Substring(state.head.uri)
        )
        let isHealthRequest = state.head.method == .GET
            && (path == "/health" || path == "/v1/health")
        let response: HTTPResponse
        let accessDecision = accessControl.authorize(
            remoteAddress: remoteAddress,
            apiKey: requestAPIKey(from: state.head.headers),
            // 健康端點只公開最小化的存活狀態，不揭露模型或設定內容；
            // 仍保留來源 IP 白名單驗證。
            validateAPIKey: state.head.method != .OPTIONS && !isHealthRequest
        )
        if accessDecision != .allowed {
            response = accessDeniedResponse(accessDecision)
        } else if state.tooLarge {
            response = .json(status: 413, object: [
                "error": ["message": "HTTP 請求內容超過大小限制。"]
            ])
        } else {
            let bytes = state.body.getBytes(
                at: state.body.readerIndex,
                length: state.body.readableBytes
            ) ?? []
            response = await router.handle(
                method: state.head.method.rawValue,
                path: path,
                body: Data(bytes)
            )
        }
        return response
    }

    private func requestAPIKey(from headers: HTTPHeaders) -> String? {
        if let value = headers.first(name: "X-OpenLoader-Key")?.trimmingCharacters(in: .whitespacesAndNewlines),
           !value.isEmpty {
            return value
        }
        if let authorization = headers.first(name: "Authorization") {
            let parts = authorization.split(whereSeparator: { $0.isWhitespace })
            if parts.count == 2, parts[0].lowercased() == "bearer" {
                return String(parts[1])
            }
        }
        return headers.first(name: "X-Api-Key")?.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func accessDeniedResponse(_ decision: RuntimeAccessDecision) -> HTTPResponse {
        var status = 503
        var code = "access_control_unavailable"
        var type = "server_error"
        var message = "Tanpopo 安全策略暫時無法使用。"
        if decision == .invalidAPIKey {
            status = 401
            code = "invalid_api_key"
            type = "authentication_error"
            message = "Tanpopo API 金鑰無效或未提供。"
        } else if decision == .ipNotAllowed {
            status = 403
            code = "ip_not_allowed"
            type = "permission_error"
            message = "目前來源 IP 不在 Tanpopo 白名單。"
        }
        var response = HTTPResponse.json(status: status, object: [
            "error": ["message": message, "type": type, "code": code]
        ])
        response.headers.append(("Cache-Control", "no-store"))
        if decision == .invalidAPIKey {
            response.headers.append(("WWW-Authenticate", "Bearer realm=\"Tanpopo Model API\""))
        }
        return response
    }

    private func write(
        _ response: HTTPResponse,
        version: HTTPVersion,
        keepAlive: Bool,
        channel: Channel,
        requestID: UUID,
        cancellation: GenerationCancellation
    ) {
        var head = HTTPResponseHead(
            version: version,
            status: HTTPResponseStatus(statusCode: response.status)
        )
        for (name, value) in response.headers {
            head.headers.add(name: name, value: value)
        }
        head.headers.replaceOrAdd(name: "Connection", value: keepAlive ? "keep-alive" : "close")
        switch response.body {
        case .data(let body):
            head.headers.add(name: "Content-Length", value: String(body.count))
            channel.write(HTTPServerResponsePart.head(head), promise: nil)
            if !body.isEmpty {
                var buffer = channel.allocator.buffer(capacity: body.count)
                buffer.writeBytes(body)
                channel.write(HTTPServerResponsePart.body(.byteBuffer(buffer)), promise: nil)
            }
            channel.writeAndFlush(HTTPServerResponsePart.end(nil)).whenComplete { result in
                cancellation.cancel()
                switch result {
                case .success:
                    self.finishRequest(requestID, channel: channel, keepAlive: keepAlive)
                case .failure:
                    self.cancelActiveRequest(reason: "response_write_failed")
                    channel.close(promise: nil)
                }
            }
        case .stream(let stream):
            head.headers.add(name: "Transfer-Encoding", value: "chunked")
            channel.writeAndFlush(HTTPServerResponsePart.head(head), promise: nil)
            // SSE 註解不代表生成文字；即使 Prefill 尚未完成也能確認連線存活。
            writeHeartbeat(channel: channel, requestID: requestID)
            responseHeartbeat = channel.eventLoop.scheduleRepeatedTask(
                initialDelay: .seconds(10), delay: .seconds(10)
            ) { task in
                guard self.requestID == requestID, channel.isActive else {
                    task.cancel()
                    return
                }
                self.writeHeartbeat(channel: channel, requestID: requestID)
            }
            responseStreamTask?.cancel()
            responseStreamTask = Task {
                defer {
                    cancellation.cancel()
                    channel.eventLoop.execute {
                        self.finishRequest(requestID, channel: channel, keepAlive: keepAlive)
                    }
                }
                do {
                    for try await data in stream {
                        try Task.checkCancellation()
                        guard channel.isActive else { break }
                        if data.isEmpty { continue }
                        var buffer = channel.allocator.buffer(capacity: data.count)
                        buffer.writeBytes(data)
                        try await channel.writeAndFlush(
                            HTTPServerResponsePart.body(.byteBuffer(buffer))
                        ).get()
                    }
                } catch is CancellationError {
                    return
                } catch {
                    if channel.isActive {
                        var buffer = channel.allocator.buffer(capacity: 128)
                        buffer.writeString("data: {\"error\":{\"message\":\"stream interrupted\"}}\n\n")
                        try? await channel.writeAndFlush(
                            HTTPServerResponsePart.body(.byteBuffer(buffer))
                        ).get()
                    }
                }
                if channel.isActive {
                    // 停止保活與寫入 end 必須在同一次 event loop 工作中完成，
                    // 避免 end 已送出、Task 尚未恢復時又寫入 heartbeat body。
                    try? await channel.eventLoop.flatSubmit {
                        guard self.requestID == requestID, channel.isActive else {
                            return channel.eventLoop.makeSucceededVoidFuture()
                        }
                        self.responseHeartbeat?.cancel()
                        self.responseHeartbeat = nil
                        return channel.writeAndFlush(HTTPServerResponsePart.end(nil))
                    }.get()
                }
            }
        }
    }

    private func writeHeartbeat(channel: Channel, requestID: UUID) {
        guard self.requestID == requestID, channel.isActive, channel.isWritable else { return }
        var buffer = channel.allocator.buffer(capacity: 16)
        buffer.writeString(": keep-alive\n\n")
        channel.writeAndFlush(HTTPServerResponsePart.body(.byteBuffer(buffer))).whenFailure { _ in
            guard self.requestID == requestID else { return }
            self.cancelActiveRequest(reason: "heartbeat_write_failed")
            channel.close(promise: nil)
        }
    }
}
