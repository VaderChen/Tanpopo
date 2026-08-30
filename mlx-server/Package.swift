// swift-tools-version: 6.0

import PackageDescription
import Foundation

// POC 以隔離 Target 重現 MLX Core 最新版 GGUF Q4/Q8 打包邏輯。
// mlx-swift 0.31.6 的 SwiftPM 建置會排除 GGUF backend，因此先用此
// Reference Packer 驗證位元佈局，不直接接管正式 Runtime。
let packageRoot = URL(fileURLWithPath: #filePath).deletingLastPathComponent().path
let mlxCoreRoot = packageRoot + "/.build/checkouts/mlx-swift/Source/Cmlx"

let package = Package(
    name: "TanpopoMLXServer",
    platforms: [
        .macOS(.v14)
    ],
    products: [
        .executable(name: "mlx-server", targets: ["MLXServer"])
    ],
    dependencies: [
        // 固定專案內維護的 3.31.4 fork；DFlash 需要 target 中間層輸出，
        // 不能依賴 SwiftPM checkout 上的暫時性修改。
        .package(path: "Vendor/mlx-swift-lm"),
        .package(
            url: "https://github.com/ml-explore/mlx-swift.git",
            exact: "0.31.6"
        ),
        .package(
            url: "https://github.com/huggingface/swift-transformers",
            exact: "1.1.9"
        ),
        .package(
            url: "https://github.com/apple/swift-nio.git",
            exact: "2.101.3"
        )
    ],
    targets: [
        .target(
            name: "MLXCoreGGUFBridge",
            dependencies: [
                .product(name: "MLX", package: "mlx-swift")
            ],
            publicHeadersPath: "include",
            cxxSettings: [
                .define("TANPOPO_MLX_GGUF_BRIDGE_POC"),
                .unsafeFlags([
                    "-I", mlxCoreRoot + "/mlx",
                    "-I", mlxCoreRoot + "/mlx-c",
                    "-I", mlxCoreRoot + "/json/single_include/nlohmann",
                    "-I", mlxCoreRoot + "/fmt/include"
                ])
            ]
        ),
        .executableTarget(
            name: "MLXServer",
            dependencies: [
                "MLXCoreGGUFBridge",
                .product(name: "MLX", package: "mlx-swift"),
                .product(name: "MLXNN", package: "mlx-swift"),
                .product(name: "MLXLLM", package: "mlx-swift-lm"),
                .product(name: "MLXVLM", package: "mlx-swift-lm"),
                .product(name: "MLXLMCommon", package: "mlx-swift-lm"),
                .product(name: "MLXHuggingFace", package: "mlx-swift-lm"),
                .product(name: "Hub", package: "swift-transformers"),
                .product(name: "Tokenizers", package: "swift-transformers"),
                .product(name: "NIOCore", package: "swift-nio"),
                .product(name: "NIOHTTP1", package: "swift-nio"),
                .product(name: "NIOPosix", package: "swift-nio")
            ]
        ),
        .testTarget(
            name: "MLXServerTests",
            dependencies: ["MLXServer"]
        )
    ],
    cxxLanguageStandard: .gnucxx20
)
