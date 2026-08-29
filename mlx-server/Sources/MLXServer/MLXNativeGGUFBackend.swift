import Foundation

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
}
