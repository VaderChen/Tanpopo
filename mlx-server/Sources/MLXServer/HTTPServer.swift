@preconcurrency import NIOCore
@preconcurrency import NIOHTTP1
@preconcurrency import NIOPosix
import Foundation

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
                    channel.pipeline.configureHTTPServerPipeline().flatMap {
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

    init(router: APIRouter, accessControl: RuntimeAccessControl, maximumRequestBytes: Int) {
        self.router = router
        self.accessControl = accessControl
        self.maximumRequestBytes = maximumRequestBytes
    }

    func channelRead(context: ChannelHandlerContext, data: NIOAny) {
        switch unwrapInboundIn(data) {
        case .head(let head):
            state = RequestState(
                head: head,
                body: context.channel.allocator.buffer(capacity: 0)
            )
        case .body(var buffer):
            guard var current = state else { return }
            if current.body.readableBytes + buffer.readableBytes > maximumRequestBytes {
                current.tooLarge = true
            } else if !current.tooLarge {
                current.body.writeBuffer(&buffer)
            }
            state = current
        case .end:
            guard let current = state else { return }
            state = nil
            // ChannelHandlerContext 只能在它所屬的 NIO event loop 使用。先在這裡
            // 擷取純值與 thread-safe Channel，再把模型工作交給 MainActor。
            let channel = context.channel
            let remoteAddress = channel.remoteAddress?.ipAddress
            Task { @MainActor in
                let response = await self.process(
                    current,
                    remoteAddress: remoteAddress
                )
                channel.eventLoop.execute {
                    self.write(
                        response,
                        version: current.head.version,
                        channel: channel
                    )
                }
            }
        }
    }

    private func process(
        _ state: RequestState,
        remoteAddress: String?
    ) async -> HTTPResponse {
        let response: HTTPResponse
        let accessDecision = accessControl.authorize(
            remoteAddress: remoteAddress,
            apiKey: requestAPIKey(from: state.head.headers),
            validateAPIKey: state.head.method != .OPTIONS
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
            let path = String(
                state.head.uri.split(separator: "?", maxSplits: 1).first
                    ?? Substring(state.head.uri)
            )
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
        var message = "OpenLoader 安全策略暫時無法使用。"
        if decision == .invalidAPIKey {
            status = 401
            code = "invalid_api_key"
            type = "authentication_error"
            message = "OpenLoader API 金鑰無效或未提供。"
        } else if decision == .ipNotAllowed {
            status = 403
            code = "ip_not_allowed"
            type = "permission_error"
            message = "目前來源 IP 不在 OpenLoader 白名單。"
        }
        var response = HTTPResponse.json(status: status, object: [
            "error": ["message": message, "type": type, "code": code]
        ])
        response.headers.append(("Cache-Control", "no-store"))
        if decision == .invalidAPIKey {
            response.headers.append(("WWW-Authenticate", "Bearer realm=\"OpenLoader Model API\""))
        }
        return response
    }

    private func write(
        _ response: HTTPResponse,
        version: HTTPVersion,
        channel: Channel
    ) {
        var head = HTTPResponseHead(
            version: version,
            status: HTTPResponseStatus(statusCode: response.status)
        )
        for (name, value) in response.headers {
            head.headers.add(name: name, value: value)
        }
        head.headers.add(name: "Content-Length", value: String(response.body.count))
        head.headers.add(name: "Connection", value: "keep-alive")
        channel.write(HTTPServerResponsePart.head(head), promise: nil)
        if !response.body.isEmpty {
            var buffer = channel.allocator.buffer(capacity: response.body.count)
            buffer.writeBytes(response.body)
            channel.write(HTTPServerResponsePart.body(.byteBuffer(buffer)), promise: nil)
        }
        channel.writeAndFlush(HTTPServerResponsePart.end(nil), promise: nil)
    }
}
