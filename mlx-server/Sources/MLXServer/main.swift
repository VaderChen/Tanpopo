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
            if configuration.inspectGGUFCache {
                let weightURL = URL(fileURLWithPath: configuration.modelPath)
                guard weightURL.pathExtension.lowercased() == "gguf" else {
                    throw ConfigurationError.invalidModelPath(configuration.modelPath)
                }
                let inspection = try MLXGGUFModelLoader.inspectConversion(
                    from: weightURL.deletingLastPathComponent(),
                    weightURL: weightURL,
                    mmprojURL: configuration.mmprojPath.map(URL.init(fileURLWithPath:)),
                    quantizationGroupSize: configuration.ggufGroupSize,
                    quantizationProfile: configuration.ggufProfile,
                    recurrentPromotion: configuration.ggufRecurrentPromotion,
                    conversionCacheDirectory: configuration.ggufCacheDirectory
                )
                let encoder = JSONEncoder()
                encoder.outputFormatting = [.sortedKeys]
                guard let output = String(
                    data: try encoder.encode(inspection),
                    encoding: .utf8
                ) else {
                    throw CocoaError(.fileWriteInapplicableStringEncoding)
                }
                print(output)
                Foundation.exit(EXIT_SUCCESS)
            }
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
