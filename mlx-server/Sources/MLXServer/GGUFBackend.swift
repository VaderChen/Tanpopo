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
    case dequantizedFP32
    case quantized4
    case quantized8
    case quantizedMXFP4
    case requantized4
    case requantized8
}

/// K-quant super-block 是否直接沿用來源布局，由 profile 決定；
/// 沿用時該張量固定 group 32，其餘張量仍用全域 group。
///
/// GGUF 重新量化的通用策略。`.automatic` 以 INT8 處理低位元來源，
/// 作為 fastGGUF 關閉時的穩健預設；`.quality` 將可解碼權重展開為 FP32，
/// 僅供精度診斷；`.mode1` 直接保留可無損映射的來源 block，
/// 必須二次量化的低位元來源則至少使用 INT8。這項規則來自來源
/// block 格式與轉換語意，不依賴模型名稱。
public enum GGUFQuantizationProfile: String, Codable, Hashable, Sendable {
    case automatic = "auto"
    case quality
    /// Mode 1（預設）：K-quant super-block 直接沿用來源 4-bit block
    /// （該張量固定 group 32），其餘張量維持 group 64。速度約 +36%。
    /// 實測量化位元寬度不影響輸出品質，因此以速度為預設取向。
    case mode1
    /// Mode 2：所有必須轉換的低位元來源重新量化為 INT8，group 64。
    /// 保守路徑，記憶體佔用較高但轉換語意最單純。
    case mode2
    /// Mode 3：所有可量化來源一律重新量化為 INT4，並固定 group 32。
    ///
    /// group 必須是 32：K-quant 的 sub-block 就是 32 元素，group 64 會橫跨兩個
    /// 各有 scale／min 的 sub-block，被迫用單一 scale 涵蓋兩組動態範圍。
    /// 實測 group 64 會讓輸出從第一個 token 就變成亂碼（相對 RMSE 8.2%，
    /// group 32 為 4.4%），因此這裡由策略決定 group，不受全域設定影響。
    case mode3

    /// 舊名稱別名，讓既有啟動參數與設定檔不必同步變更；
    /// 舊名對應的實際行為維持不變（speed 仍是全 INT8）。
    public init?(rawValue: String) {
        switch rawValue.lowercased() {
        case "auto", "automatic": self = .automatic
        case "quality": self = .quality
        case "mode1", "speed-passthrough": self = .mode1
        case "mode2", "speed": self = .mode2
        case "mode3": self = .mode3
        default: return nil
        }
    }
}

public struct GGUFTypeSupport: Codable, Hashable, Sendable {
    public let storageType: GGUFStorageType
    public let materialization: GGUFMaterializationKind
    /// 是否保留來源量化的數值表示；不代表來源位元組可零拷貝使用。
    /// 例如 MXFP4 會保留量化值，但仍須重新封裝成 MLX 權重布局。
    public let preservesSourceQuantization: Bool
    /// 是否需要建立不同於來源 GGUF block 的 MLX 權重布局。
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

    /// 能完整解碼成 FP32 參考權重的 GGUF block。集中在 storage policy
    /// 內，避免新呼叫端忘記 quality 的診斷語意。
    private static let fp32ReferenceSourceTypes: Set<String> = [
        "Q4_0", "Q4_1", "Q8_0", "Q2_K", "Q3_K", "Q4_K", "Q5_K", "Q6_K",
        "Q1_0", "Q2_0",
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
            storageType: .int8,
            materialization: .requantized8,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "Q2_0": GGUFTypeSupport(
            storageType: .int8,
            materialization: .requantized8,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "Q2_K": GGUFTypeSupport(
            storageType: .int8,
            materialization: .requantized8,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "Q3_K": GGUFTypeSupport(
            storageType: .int8,
            materialization: .requantized8,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        // Q4_K 的 32 元素 sub-block 與 MLX affine group 32 同構：
        //   w = (d * sc) * q - (dmin * m)  ≡  w = q * scale + bias
        // 量化值可原樣沿用，只需把階層式 scale 改寫成扁平的 (scale, bias)，
        // 因此不算二次量化。此路徑必須搭配 group 32。
        "Q4_K": GGUFTypeSupport(
            storageType: .int4,
            materialization: .quantized4,
            preservesSourceQuantization: true,
            requiresConversion: false
        ),
        "IQ4_NL": GGUFTypeSupport(
            storageType: .int8,
            materialization: .requantized8,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "IQ3_S": GGUFTypeSupport(
            storageType: .int8,
            materialization: .requantized8,
            preservesSourceQuantization: false,
            requiresConversion: true
        ),
        "IQ4_XS": GGUFTypeSupport(
            storageType: .int8,
            materialization: .requantized8,
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
        support(for: sourceType, profile: .automatic)
    }

    public static func support(
        for sourceType: String,
        profile: GGUFQuantizationProfile
    ) -> GGUFTypeSupport? {
        let normalized = sourceType.uppercased()
        guard let support = supportByType[normalized] else { return nil }
        if profile == .quality, fp32ReferenceSourceTypes.contains(normalized) {
            return GGUFTypeSupport(
                storageType: .fp32,
                materialization: .dequantizedFP32,
                preservesSourceQuantization: false,
                requiresConversion: true
            )
        }
        // 只要來源 block 必須轉成另一種 affine 布局，就不再壓回
        // INT4。這同時覆蓋 Q1～Q6 K-quant 與 IQ 格式，也避免未來擴充
        // 新格式時忘記更新型別清單。MXFP4 雖需重新封裝，但能保留來源
        // 量化值，因此先以 preservesSourceQuantization 判斷。
        // Mode 3 一律降到 INT4：不沿用來源 block，也不升 INT8。
        if profile == .mode3 {
            switch support.materialization {
            case .quantized4, .quantized8, .requantized4, .requantized8:
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
        // K-quant super-block 雖然能無損沿用（基礎表如實描述格式），但沿用等於把
        // 該張量的位元寬度從 INT8 降到 INT4，實測精度低於重新量化為 INT8，
        // 因此只有明確選用 speed-passthrough 才採用，其餘策略一律升 INT8。
        if support.preservesSourceQuantization,
           usesSuperBlockLayout(normalized),
           profile != .mode1 {
            return GGUFTypeSupport(
                storageType: .int8,
                materialization: .requantized8,
                preservesSourceQuantization: false,
                requiresConversion: true
            )
        }
        let requiresINT8Requantization = !support.preservesSourceQuantization
            && support.requiresConversion
        // 保守模式額外將可直接沿用的 4-bit block 展開後量化為
        // INT8；快速模式則保留這些來源 block，維持格式原有精度。
        let conservativeQ4Promotion = profile == .automatic
            && support.materialization == .quantized4
        if requiresINT8Requantization || conservativeQ4Promotion {
            return GGUFTypeSupport(
                storageType: .int8,
                materialization: .requantized8,
                preservesSourceQuantization: false,
                requiresConversion: true
            )
        }
        return support
    }

    /// 來源 block 大於 MLX 單一 affine group 的格式（K-quant super-block）。
    ///
    /// 這類格式的 sub-block 是 32 元素，無損沿用時必須固定 group 32；它們也沒有
    /// group 64 的退路——32 元素 block 的 Q4_0／Q8_0 在 group 64 時可以改走重新
    /// 量化，super-block 格式一旦選擇沿用就只能是 32。判斷依來源 block 結構，
    /// 不依模型名稱。
    private static let superBlockSourceTypes: Set<String> = ["Q4_K"]

    static func usesSuperBlockLayout(_ sourceType: String) -> Bool {
        superBlockSourceTypes.contains(sourceType.uppercased())
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
        targetStorageType(for: sourceType, profile: .automatic)
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
