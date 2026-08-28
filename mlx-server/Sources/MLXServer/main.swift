import Foundation

@main
enum MLXServerMain {
    static func main() async {
        #if !(os(macOS) && arch(arm64))
        fputs("mlx-server 僅支援 macOS Apple Silicon。\n", stderr)
        Foundation.exit(EXIT_FAILURE)
        #else
        do {
            let configuration = try ServerConfiguration.parse(
                Array(CommandLine.arguments.dropFirst())
            )
            let runtime = try MLXRuntime(configuration: configuration)
            let kind = runtime.kind.rawValue
            print("mlx-server \(ServerConfiguration.version)")
            print("loading \(kind) model from \(configuration.modelPath)")
            try await runtime.prepare()
            print("model loaded")
            let router = APIRouter(runtime: runtime, configuration: configuration)
            let server = HTTPServer(configuration: configuration, router: router)
            try await server.run()
        } catch {
            fputs("mlx-server error: \(error.localizedDescription)\n", stderr)
            fputs("\(ServerConfiguration.usage)\n", stderr)
            Foundation.exit(EXIT_FAILURE)
        }
        #endif
    }
}
