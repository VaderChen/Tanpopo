import Foundation

@main
enum MLXServerMain {
    static func main() async {
        #if !(os(macOS) && arch(arm64))
        fputs("mlx-server 僅支援 macOS Apple Silicon。\n", stderr)
        Foundation.exit(EXIT_FAILURE)
        #else
        let arguments = Array(CommandLine.arguments.dropFirst())
        // 型別註冊表是 actor，必須在 async 內容裡讀，因此不走 ServerConfiguration.parse。
        if arguments.contains("--supported-model-types") {
            await SupportedModelTypes.emit()
            Foundation.exit(EXIT_SUCCESS)
        }
        do {
            let configuration = try ServerConfiguration.parse(arguments)
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
