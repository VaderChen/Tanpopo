import Foundation
import Cmlx
import MLX
import MLXCoreGGUFBridge

public enum MLXNativeGGUFBridgeError: LocalizedError, Sendable {
    case packFailed(String)

    public var errorDescription: String? {
        switch self {
        case .packFailed(let message):
            return "MLX Core GGUF Reference Packer 失敗：\(message)"
        }
    }
}

public struct MLXNativeGGUFBackend: Sendable {
    public init() {}

    public func inspect(fileURL: URL) throws -> GGUFBackendInspection {
        try MLXGGUFLoader.inspect(from: fileURL)
    }

    public func canMaterialize(_ inspection: GGUFBackendInspection) -> Bool {
        inspection.unsupportedTypes.isEmpty
            && inspection.tensors.allSatisfy {
                GGUFStoragePolicy.isMaterializable($0.type)
            }
    }

    /// Returns the configuration synthesized from GGUF metadata. This is exposed
    /// for diagnostics so the generated model contract can be compared with the
    /// Hugging Face config shipped beside a GGUF checkpoint.
    public func configurationData(fileURL: URL, mmprojURL: URL? = nil) throws -> Data {
        try MLXGGUFEmbeddedAssets.configurationData(
            weightURL: fileURL,
            mmprojURL: mmprojURL
        )
    }

    /// POC 診斷路徑：依 MLX Core 最新版 `gguf_quants.cpp`
    /// 將單一 GGUF Tensor 打包為 MLX quantized() 格式。
    ///
    /// 目前只用來對照 Tanpopo 自有 Loader，不接管正式 Runtime。
    func packCoreReference(
        raw: Data,
        sourceType: UInt32,
        rows: Int,
        columns: Int
    ) throws -> (wq: MLXArray, scales: MLXArray, biases: MLXArray) {
        var weights = tanpopo_mlx_array_handle(context: nil)
        var scales = tanpopo_mlx_array_handle(context: nil)
        var biases = tanpopo_mlx_array_handle(context: nil)
        let status = raw.withUnsafeBytes { bytes in
            tanpopo_mlx_gguf_pack_reference(
                bytes.bindMemory(to: UInt8.self).baseAddress,
                bytes.count,
                sourceType,
                rows,
                columns,
                &weights,
                &scales,
                &biases
            )
        }
        guard status == 0 else {
            throw MLXNativeGGUFBridgeError.packFailed(Self.bridgeErrorMessage())
        }
        guard let weightContext = weights.context,
              let scaleContext = scales.context,
              let biasContext = biases.context else {
            throw MLXNativeGGUFBridgeError.packFailed("橋接未回傳完整 Array。")
        }
        return (
            MLXArray(mlx_array(ctx: weightContext)),
            MLXArray(mlx_array(ctx: scaleContext)),
            MLXArray(mlx_array(ctx: biasContext))
        )
    }

    private static func bridgeErrorMessage() -> String {
        guard let message = tanpopo_mlx_gguf_last_error() else {
            return "未知錯誤"
        }
        return String(cString: message)
    }
}
