// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "LlamaLoaderMLXServer",
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
        .executableTarget(
            name: "MLXServer",
            dependencies: [
                .product(name: "MLXLLM", package: "mlx-swift-lm"),
                .product(name: "MLXVLM", package: "mlx-swift-lm"),
                .product(name: "MLXLMCommon", package: "mlx-swift-lm"),
                .product(name: "MLXHuggingFace", package: "mlx-swift-lm"),
                .product(name: "Tokenizers", package: "swift-transformers"),
                .product(name: "NIOCore", package: "swift-nio"),
                .product(name: "NIOHTTP1", package: "swift-nio"),
                .product(name: "NIOPosix", package: "swift-nio")
            ]
        )
    ]
)
