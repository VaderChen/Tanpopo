@preconcurrency import NIOCore
@preconcurrency import NIOHTTP1
@preconcurrency import NIOPosix
import Foundation

final class HTTPServer {
    private let configuration: ServerConfiguration
    private let router: APIRouter

    init(configuration: ServerConfiguration, router: APIRouter) {
        self.configuration = configuration
        self.router = router
    }

    func run() async throws {
        let group = MultiThreadedEventLoopGroup(numberOfThreads: System.coreCount)
        do {
            let router = router
            let maximumRequestBytes = configuration.maximumRequestBytes
            let bootstrap = ServerBootstrap(group: group)
                .serverChannelOption(ChannelOptions.backlog, value: 128)
                .serverChannelOption(ChannelOptions.socketOption(.so_reuseaddr), value: 1)
                .childChannelInitializer { channel in
                    channel.pipeline.configureHTTPServerPipeline().flatMap {
                        channel.pipeline.addHandler(
                            HTTPRequestHandler(
                                router: router,
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
    private let maximumRequestBytes: Int
    private var state: RequestState?

    init(router: APIRouter, maximumRequestBytes: Int) {
        self.router = router
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
            nonisolated(unsafe) let context = context
            Task { @MainActor in
                await self.process(current, context: context)
            }
        }
    }

    private func process(
        _ state: RequestState,
        context: ChannelHandlerContext
    ) async {
        let response: HTTPResponse
        if state.tooLarge {
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
        write(response, version: state.head.version, context: context)
    }

    private func write(
        _ response: HTTPResponse,
        version: HTTPVersion,
        context: ChannelHandlerContext
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
        context.write(wrapOutboundOut(.head(head)), promise: nil)
        if !response.body.isEmpty {
            var buffer = context.channel.allocator.buffer(capacity: response.body.count)
            buffer.writeBytes(response.body)
            context.write(wrapOutboundOut(.body(.byteBuffer(buffer))), promise: nil)
        }
        context.writeAndFlush(wrapOutboundOut(.end(nil)), promise: nil)
    }
}
