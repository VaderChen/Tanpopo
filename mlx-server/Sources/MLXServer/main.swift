import Foundation
import MLXLLM
import MLXVLM

@main
enum MLXServerMain {
    static func main() async {
        #if !(os(macOS) && arch(arm64))
        fputs("mlx-server 僅支援 macOS Apple Silicon。\n", stderr)
        Foundation.exit(EXIT_FAILURE)
        #else
        let arguments = Array(CommandLine.arguments.dropFirst())
        if arguments == ["--distributed-capabilities"] {
            let object: [String: Any] = ["version": ServerConfiguration.version,
                "ring_available": DistributedGroup.available("ring"),
                "managed_parent_stdin": true,
                "max_ring_nodes": 8,
                "generic_linear_sharding": true,
                "gguf_model_types": MLXGGUFEmbeddedAssets.supportedGGUFArchitectures,
                "gguf_sharding": true, "speculative_coordinator": true,
                "text_model_types": await LLMTypeRegistry.shared.registeredModelTypes.sorted(),
                "vision_model_types": await VLMTypeRegistry.shared.registeredModelTypes.sorted(),
                "jaccl_library_available": DistributedGroup.available("jaccl"),
                "hardware_verified": false,
                "note": "後端可用不代表 Thunderbolt 線路或雙機推論已通過驗證。"]
            if let data = try? JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]),
                let output = String(data: data, encoding: .utf8) { print(output) }
            return
        }
        // 型別註冊表是 actor，必須在 async 內容裡讀，因此不走 ServerConfiguration.parse。
        if arguments.contains("--supported-model-types") {
            await SupportedModelTypes.emit()
            Foundation.exit(EXIT_SUCCESS)
        }
        do {
            let configuration = try ServerConfiguration.parse(arguments)
            var worker: DistributedWorkerProcess?
            var distributedDirectory: URL?
            defer {
                worker?.stop()
                if let distributedDirectory { try? FileManager.default.removeItem(at: distributedDirectory) }
            }
            var distributedSession: DistributedTensorSession?
            if let distributed = configuration.distributed {
                distributedDirectory = try distributed.configureEnvironment(rank: configuration.distributedRank)
                if configuration.distributedRank == 0, distributed.nodes[1].launchMode != .manual {
                    worker = try DistributedWorkerProcess(configuration: distributed,
                        modelPath: configuration.modelPath, modelKind: configuration.modelKind, smoke: configuration.distributedSmoke,
                        targetArguments: configuration.distributedTargetArguments(workerModelPath: distributed.nodes[1].modelPath))
                }
                if configuration.distributedParentStdin { DistributedWorkerProcess.monitorParentInput() }
                let group = try DistributedGroup(configuration: distributed, rank: configuration.distributedRank)
                let session = DistributedTensorSession(group: group, configuration: distributed)
                distributedSession = session
                if configuration.distributedSmoke {
                    try DistributedSmoke.run(session: session)
                    return
                }
                let deadline = DistributedDeadline(seconds: distributed.startupTimeout, operation: "模型核對")
                defer { withExtendedLifetime(deadline) {} }
                let digest = try DistributedTensorSession.modelDigest(configuration: configuration)
                try group.verifyDigest(digest, label: "Runtime 版本與模型內容")
            }
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
            let runtime = try MLXRuntime(configuration: configuration, distributedSession: distributedSession)
            let kind = runtime.kind.rawValue
            print("mlx-server \(ServerConfiguration.version)")
            print("loading \(kind) model from \(configuration.modelPath)")
            let loadingDeadline = configuration.distributed.map {
                DistributedDeadline(seconds: $0.startupTimeout, operation: "模型分片載入")
            }
            try await runtime.prepare()
            loadingDeadline?.cancel()
            if let distributedSession, distributedSession.group.rank != 0 {
                await runtime.releaseWorkerModel()
                try distributedSession.workerLoop()
                return
            }
            try await runtime.distributedHealth()
            distributedSession?.startHeartbeat()
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
