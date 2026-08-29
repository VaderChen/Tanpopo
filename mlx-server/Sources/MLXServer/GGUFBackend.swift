import Foundation

public enum GGUFStorageType: String, Codable, Hashable, Sendable {
    case int4 = "INT4"
    case int8 = "INT8"
    case int16 = "INT16"
    case int32 = "INT32"
    case fp16 = "FP16"
    case bf16 = "BF16"
    case fp32 = "FP32"
}

public enum GGUFMaterializationKind: String, Codable, Hashable, Sendable {
    case directFloat32
    case directFloat16
    case directBFloat16
    case directInt8
    case directInt16
    case directInt32
    case quantized4
    case quantized8
    case quantizedMXFP4
    case requantized4
    case requantized8
}

/// GGUF 重新量化的品質／速度取捨。`.quality` 維持來源型別較高的精度；
/// `.speed` 將 Q5_K／Q6_K 改以 INT4 儲存，以降低 decode 時的記憶體頻寬。
public enum GGUFQuantizationProfile: String, Codable, Hashable, Sendable {
    case quality
    case speed
}

public struct GGUFTypeSupport: Codable, Hashable, Sendable {
    public let storageType: GGUFStorageType
    public let materialization: GGUFMaterializationKind
    public let preservesSourceQuantization: Bool
    public let requiresConversion: Bool

    public init(
        storageType: GGUFStorageType,
        materialization: GGUFMaterializationKind,
        preservesSourceQuantization: Bool,
        requiresConversion: Bool
    ) {
        self.storageType = storageType
        self.materialization = materialization
        self.preservesSourceQuantization = preservesSourceQuantization
        self.requiresConversion = requiresConversion
    }
}

public struct GGUFStoragePlan: Codable, Hashable, Sendable {
    public let storageTypeCounts: [GGUFStorageType: Int]
    public let conversionTensorCount: Int
    public let preservedQuantizedTensorCount: Int

    public var requiresRequantization: Bool {
        conversionTensorCount > 0
    }

    public var requiresFP32Storage: Bool {
        storageTypeCounts[.fp32, default: 0] > 0
    }

    init(tensors: [GGUFTensorDescriptor]) {
        storageTypeCounts = tensors.reduce(into: [:]) { counts, tensor in
            if let storageType = tensor.storageType {
                counts[storageType, default: 0] += 1
            }
        }
        conversionTensorCount = tensors.reduce(into: 0) { count, tensor in
            if tensor.requiresConversion {
                count += 1
            }
        }
        preservedQuantizedTensorCount = tensors.reduce(into: 0) { count, tensor in
            if tensor.preservesSourceQuantization {
                count += 1
            }
        }
    }
}

public enum GGUFStoragePolicy {
    private static let fp8SourceTypeNames: Set<String> = [
        "FP8",
        "FP8_E4M3",
        "FP8_E4M3FN",
        "FP8_E5M2",
        "F8",
        "F8_E4M3",
        "F8_E4M3FN",
        "F8_E5M2",
        "FLOAT8_E4M3FN",
        "FLOAT8_E5M2"
    ]

    private static let supportByType: [String: GGUFTypeSupport] = [
        "F32": GGUFTypeSupport(
            storageType: .fp32,
            materialization: .directFloat32,
            preservesSourceQuantization: false,
            requiresConversion: false
        ),
        "F16": GGUFTypeSupport(
            storageType: .fp16,
            materialization: .directFloat16,
            preservesSourceQuantization: false,
            requiresConversion: false
        ),
        "BF16": GGUFTypeSupport(
            storageType: .bf16,
            materialization: .directBFloat16,
            preservesSourceQuantization: false,
            requiresConversion: false
        ),
        "I8": GGUFTypeSupport(
            storageType: .int8,
            materialization: .directInt8,
            preservesSourceQuantization: false,
            requiresConversion: false
        ),
        "I16": GGUFTypeSupport(
            storageType: .int16,
            materialization: .directInt16,
            preservesSourceQuantization: false,
            requiresConversion: false
        ),
        "I32": GGUFTypeSupport(
            storageType: .int32,
            materialization: .directInt32,
            preservesSourceQuantization: false,
            requiresConversion: false
        ),
        "Q4_0": GGUFTypeSupport(
            storageType: .int4,
            materialization: .quantized4,
            preservesSourceQuantization: true,
            requiresConversion: false
        ),
        "Q4_1": GGUFTypeSupport(
            storageType: .int4,
            materialization: .quantized4,
            preservesSourceQuantization: true,
            requiresConversion: false
        ),
        "Q8_0": GGUFTypeSupport(
            storageType: .int8,
            materialization: .quantized8,
            preservesSourceQuantization: true,
            requiresConversion: false
        ),
        "MXFP4": GGUFTypeSupport(
            storageType: .int4,
            materialization: .quantizedMXFP4,
            preservesSourceQuantization: true,
            requiresConversion: true
        ),
        "Q1_0": GGUFTypeSupport(
            storageType: .int4,
            materialization: .requantized4,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "Q2_0": GGUFTypeSupport(
            storageType: .int4,
            materialization: .requantized4,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "Q2_K": GGUFTypeSupport(
            storageType: .int4,
            materialization: .requantized4,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "Q3_K": GGUFTypeSupport(
            storageType: .int4,
            materialization: .requantized4,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "Q4_K": GGUFTypeSupport(
            storageType: .int4,
            materialization: .requantized4,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "IQ4_NL": GGUFTypeSupport(
            storageType: .int4,
            materialization: .requantized4,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "IQ3_S": GGUFTypeSupport(
            storageType: .int4,
            materialization: .requantized4,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "IQ4_XS": GGUFTypeSupport(
            storageType: .int4,
            materialization: .requantized4,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "Q5_K": GGUFTypeSupport(
            storageType: .int8,
            materialization: .requantized8,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "Q6_K": GGUFTypeSupport(
            storageType: .int8,
            materialization: .requantized8,
            preservesSourceQuantization: false,
            requiresConversion: true
        )
    ]

    public static func support(for sourceType: String) -> GGUFTypeSupport? {
        support(for: sourceType, profile: .quality)
    }

    public static func support(
        for sourceType: String,
        profile: GGUFQuantizationProfile
    ) -> GGUFTypeSupport? {
        let normalized = sourceType.uppercased()
        guard let support = supportByType[normalized] else { return nil }
        guard profile == .speed else { return support }
        switch normalized {
        case "Q5_K", "Q6_K":
            return GGUFTypeSupport(
                storageType: .int4,
                materialization: .requantized4,
                preservesSourceQuantization: false,
                requiresConversion: true
            )
        default:
            return support
        }
    }

    public static func isMaterializable(_ sourceType: String) -> Bool {
        support(for: sourceType) != nil
    }

    public static var materializableTypes: Set<String> {
        Set(supportByType.keys)
    }

    public static func storageType(for sourceType: String) -> GGUFStorageType? {
        support(for: sourceType)?.storageType
    }

    public static func storageType(
        for sourceType: String,
        profile: GGUFQuantizationProfile
    ) -> GGUFStorageType? {
        support(for: sourceType, profile: profile)?.storageType
    }

    public static func targetStorageType(for sourceType: String) -> GGUFStorageType? {
        targetStorageType(for: sourceType, profile: .quality)
    }

    public static func targetStorageType(
        for sourceType: String,
        profile: GGUFQuantizationProfile
    ) -> GGUFStorageType? {
        let normalized = sourceType.uppercased()
        if fp8SourceTypeNames.contains(normalized) {
            return .int8
        }
        return storageType(for: normalized, profile: profile)
    }

    public static func preservesSourceQuantization(for sourceType: String) -> Bool {
        support(for: sourceType)?.preservesSourceQuantization ?? false
    }

    public static func preservesSourceQuantization(
        for sourceType: String,
        profile: GGUFQuantizationProfile
    ) -> Bool {
        support(for: sourceType, profile: profile)?.preservesSourceQuantization ?? false
    }

    public static func requiresConversion(for sourceType: String) -> Bool {
        support(for: sourceType)?.requiresConversion ?? false
    }

    public static func requiresConversion(
        for sourceType: String,
        profile: GGUFQuantizationProfile
    ) -> Bool {
        support(for: sourceType, profile: profile)?.requiresConversion ?? false
    }
}

public struct GGUFTensorDescriptor: Codable, Hashable, Sendable {
    public let name: String
    public let shape: [Int]
    public let type: String
    public let offset: UInt64
    public let byteSize: UInt64?
    public let isMaterializable: Bool
    public let storageType: GGUFStorageType?
    public let preservesSourceQuantization: Bool
    public let requiresConversion: Bool
}

public struct GGUFBackendInspection: Codable, Hashable, Sendable {
    public let version: UInt32
    public let alignment: UInt64
    public let dataOffset: UInt64
    public let fileSize: UInt64
    public let metadataCount: Int
    public let tensors: [GGUFTensorDescriptor]
    public let quantizationCounts: [String: Int]
    public let unsupportedTypes: [String]

    public var tensorCount: Int { tensors.count }

    public var storageTypeCounts: [GGUFStorageType: Int] {
        tensors.reduce(into: [:]) { counts, tensor in
            if let storageType = tensor.storageType {
                counts[storageType, default: 0] += 1
            }
        }
    }

    public var conversionTensorCount: Int {
        tensors.reduce(into: 0) { count, tensor in
            if tensor.requiresConversion {
                count += 1
            }
        }
    }

    public var storagePlan: GGUFStoragePlan {
        GGUFStoragePlan(tensors: tensors)
    }
}

public enum GGUFBackendError: LocalizedError, Sendable {
    case unsupportedMaterialization(types: [String])

    public var errorDescription: String? {
        switch self {
        case let .unsupportedMaterialization(types):
            return "目前的 MLX GGUF loader 無法材料化型別：\(types.joined(separator: ", "))。"
        }
    }
}
