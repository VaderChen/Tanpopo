import Foundation
import MLX
import MLXHuggingFace
import MLXLLM
import MLXLMCommon
import MLXNN
import MLXVLM
import Tokenizers

struct MLXGGUFTensorInfo {
    let name: String
    let dimensions: [Int]
    let type: UInt32
    let offset: UInt64
}

/// 由 GGUF tensor 本身的型別與維度推導出的執行策略。模型名稱與架構名稱不會
/// 參與決策；外部樣本只用於量測這套規則的效果。
struct MLXGGUFQuantizationStrategy: Equatable, Sendable {
    let profile: GGUFQuantizationProfile
    let requestedGroupSize: Int?
    let groupSize: Int
    let targetStorageCounts: [GGUFStorageType: Int]

    var usedResolvedGroup32: Bool {
        groupSize == 32 && requestedGroupSize != 32
    }

    /// 每種目標儲存型別的張量數量，順序固定，可直接當作快取鍵的一部分。
    /// 轉換策略一改變（例如某型別由 INT8 改為無損沿用的 INT4），簽章就會不同，
    /// 既有快取自然失效，不必再靠手動調整 Runtime 版本號讓它過期。
    var storageSignature: String {
        let storageOrder: [GGUFStorageType] = [
            .int4, .int8, .bf16, .fp16, .fp32, .int16, .int32
        ]
        return storageOrder.compactMap { type -> String? in
            guard let count = targetStorageCounts[type], count > 0 else { return nil }
            return "\(type.rawValue):\(count)"
        }.joined(separator: ",")
    }

    var logDescription: String {
        let requested = requestedGroupSize.map(String.init) ?? "auto"
        return "profile=\(profile.rawValue) requested_group=\(requested) "
            + "resolved_group=\(groupSize) storage=[\(storageSignature)]"
    }
}

private enum GGUFRecurrentTensorRole {
    case control
    case data
}

/// GGUF 的 recurrent／SSM tensor 可能保存 Runtime 參數本身，也可能保存已摺疊
/// 的執行值。轉換由來源語意與目標架構契約決定，不依模型檔名判斷。
enum GGUFStateSpaceParameterEncoding: Equatable, Sendable {
    case native
    case negativeExponential
}

struct MLXGGUFArchitectureContract: Equatable, Sendable {
    let stateSpaceParameterEncoding: GGUFStateSpaceParameterEncoding

    static func resolve(modelType: String) -> Self {
        switch modelType.lowercased() {
        // llama.cpp 的 Qwen Gated Delta Net GGUF 將 ssm_a 保存為
        // -exp(A_log)，而 mlx-swift-lm 的參數契約是 A_log。
        case "qwen3_5", "qwen3_5_text", "qwen3_next":
            Self(stateSpaceParameterEncoding: .negativeExponential)
        default:
            // 未經架構契約確認時保留來源語意，避免把某一架構的公式套到
            // Mamba、Jamba、Falcon H1 等不同 SSM 實作。
            Self(stateSpaceParameterEncoding: .native)
        }
    }
}

enum GGUFStateSpaceParameterDecoder {
    /// 轉換後立即 eval，確保以 mmap 建立的來源 tensor 在解除映射前已完成材料化。
    /// 這是所有需要算術轉換的外部映射 tensor 共用的生命週期規則。
    static func decode(
        _ source: MLXArray,
        encoding: GGUFStateSpaceParameterEncoding
    ) -> MLXArray {
        let result: MLXArray
        switch encoding {
        case .native:
            result = source
        case .negativeExponential:
            result = (-source).log()
        }
        result.eval()
        return result
    }
}

enum MLXGGUFWeightContractIssueKind: String, Equatable, Sendable {
    case missing = "missing"
    case unexpected = "unexpected"
    case shapeMismatch = "shape_mismatch"
}

struct MLXGGUFWeightContractIssue: Equatable, Sendable {
    let kind: MLXGGUFWeightContractIssueKind
    let name: String
    let expectedShape: [Int]?
    let actualShape: [Int]?
}

struct MLXGGUFWeightContractReport: Equatable, Sendable {
    let expectedCount: Int
    let actualCount: Int
    let issues: [MLXGGUFWeightContractIssue]

    var isCompatible: Bool { issues.isEmpty }

    func logDescription(maximumIssues: Int = 40) -> String {
        let summary = "GGUF weight contract expected=\(expectedCount) "
            + "actual=\(actualCount) issues=\(issues.count)"
        guard !issues.isEmpty, maximumIssues > 0 else { return summary }
        let details = issues.prefix(maximumIssues).map { issue in
            let expected = issue.expectedShape.map(String.init(describing:)) ?? "-"
            let actual = issue.actualShape.map(String.init(describing:)) ?? "-"
            return "  \(issue.kind.rawValue) \(issue.name) "
                + "expected=\(expected) actual=\(actual)"
        }
        let omitted = issues.count > maximumIssues
            ? ["  ... omitted \(issues.count - maximumIssues) issues"]
            : []
        return ([summary] + details + omitted).joined(separator: "\n")
    }
}

/// 在 `model.update` 前比對 GGUF 權重與量化後 Module 的完整參數合約。
///
/// 這是架構無關的診斷：不依賴模型名稱，只檢查參數路徑及形狀。量化權重的
/// weight／scales／biases 也會個別比對，因此可找出只重排 weight、卻漏掉其
/// companion tensor 的錯誤。
enum MLXGGUFWeightContract {
    static func inspect(
        model: Module,
        weights: [String: MLXArray]
    ) -> MLXGGUFWeightContractReport {
        inspect(expected: model.parameters().flattened(), weights: weights)
    }

    static func inspect(
        expected: [(String, MLXArray)],
        weights: [String: MLXArray]
    ) -> MLXGGUFWeightContractReport {
        let expectedWeights = Dictionary(uniqueKeysWithValues: expected)
        let expectedNames = Set(expectedWeights.keys)
        let actualNames = Set(weights.keys)
        var issues = [MLXGGUFWeightContractIssue]()

        for name in expectedNames.subtracting(actualNames).sorted() {
            issues.append(
                MLXGGUFWeightContractIssue(
                    kind: .missing,
                    name: name,
                    expectedShape: expectedWeights[name]?.shape,
                    actualShape: nil
                )
            )
        }
        for name in actualNames.subtracting(expectedNames).sorted() {
            issues.append(
                MLXGGUFWeightContractIssue(
                    kind: .unexpected,
                    name: name,
                    expectedShape: nil,
                    actualShape: weights[name]?.shape
                )
            )
        }
        for name in expectedNames.intersection(actualNames).sorted() {
            guard let expectedShape = expectedWeights[name]?.shape,
                  let actualShape = weights[name]?.shape,
                  expectedShape != actualShape else { continue }
            issues.append(
                MLXGGUFWeightContractIssue(
                    kind: .shapeMismatch,
                    name: name,
                    expectedShape: expectedShape,
                    actualShape: actualShape
                )
            )
        }
        return MLXGGUFWeightContractReport(
            expectedCount: expectedWeights.count,
            actualCount: weights.count,
            issues: issues
        )
    }
}

enum MLXGGUFMetadataValue: Equatable, Sendable {
    case uint8(UInt8)
    case int8(Int8)
    case uint16(UInt16)
    case int16(Int16)
    case uint32(UInt32)
    case int32(Int32)
    case float32(Float)
    case boolean(Bool)
    case string(String)
    case array([MLXGGUFMetadataValue])
    case uint64(UInt64)
    case int64(Int64)
    case float64(Double)

    var integerValue: Int? {
        switch self {
        case let .uint8(value): Int(value)
        case let .int8(value): Int(value)
        case let .uint16(value): Int(value)
        case let .int16(value): Int(value)
        case let .uint32(value): Int(exactly: value)
        case let .int32(value): Int(value)
        case let .uint64(value): value <= UInt64(Int.max) ? Int(value) : nil
        case let .int64(value): Int(exactly: value)
        case let .float32(value): Int(exactly: value)
        case let .float64(value): Int(exactly: value)
        default: nil
        }
    }

    var floatValue: Float? {
        switch self {
        case let .uint8(value): Float(value)
        case let .int8(value): Float(value)
        case let .uint16(value): Float(value)
        case let .int16(value): Float(value)
        case let .uint32(value): Float(value)
        case let .int32(value): Float(value)
        case let .float32(value): value
        case let .uint64(value): Float(value)
        case let .int64(value): Float(value)
        case let .float64(value): Float(value)
        default: nil
        }
    }

    var booleanValue: Bool? {
        if case let .boolean(value) = self { return value }
        return nil
    }

    var stringValue: String? {
        if case let .string(value) = self { return value }
        return nil
    }

    var arrayValue: [MLXGGUFMetadataValue]? {
        if case let .array(value) = self { return value }
        return nil
    }

    var jsonValue: Any {
        switch self {
        case let .uint8(value): Int(value)
        case let .int8(value): Int(value)
        case let .uint16(value): Int(value)
        case let .int16(value): Int(value)
        case let .uint32(value): Int(value)
        case let .int32(value): Int(value)
        case let .float32(value): value
        case let .boolean(value): value
        case let .string(value): value
        case let .array(value): value.map(\.jsonValue)
        case let .uint64(value): value <= UInt64(Int.max) ? Int(value) : String(value)
        case let .int64(value): value
        case let .float64(value): value
        }
    }
}

private struct MLXGGUFFileLayout {
    let version: UInt32
    let alignment: Int
    let tensorDataOffset: Int
    let metadataCount: Int
    let metadata: [String: MLXGGUFMetadataValue]
    let tensors: [MLXGGUFTensorInfo]
}

private struct MLXGGUFReader {
    let data: Data
    var offset = 0

    mutating func readUInt8() throws -> UInt8 {
        guard offset < data.count else { throw MLXGGUFLoaderError.truncated }
        defer { offset += 1 }
        return data[offset]
    }

    mutating func readUInt16() throws -> UInt16 {
        let first = UInt16(try readUInt8())
        let second = UInt16(try readUInt8()) << 8
        return first | second
    }

    mutating func readUInt32() throws -> UInt32 {
        var value: UInt32 = 0
        for byteIndex in 0..<4 {
            value |= UInt32(try readUInt8()) << UInt32(byteIndex * 8)
        }
        return value
    }

    mutating func readUInt64() throws -> UInt64 {
        var value: UInt64 = 0
        for byteIndex in 0..<8 {
            value |= UInt64(try readUInt8()) << UInt64(byteIndex * 8)
        }
        return value
    }

    mutating func readCount() throws -> Int {
        let value = try readUInt64()
        guard value <= UInt64(Int.max) else { throw MLXGGUFLoaderError.invalidSize }
        return Int(value)
    }

    mutating func readString() throws -> String {
        let length = try readCount()
        guard length <= data.count - offset else { throw MLXGGUFLoaderError.truncated }
        let end = offset + length
        let value = String(data: Data(data[offset..<end]), encoding: .utf8)
        offset = end
        guard let value else { throw MLXGGUFLoaderError.invalidText }
        return value
    }

    mutating func skipString() throws {
        let length = try readCount()
        try skip(bytes: length)
    }

    mutating func skip(bytes: Int) throws {
        guard bytes >= 0, bytes <= data.count - offset else {
            throw MLXGGUFLoaderError.truncated
        }
        offset += bytes
    }

    mutating func skip(valueType: UInt32) throws {
        let byteCount: Int
        switch valueType {
        case 0, 1, 7:
            byteCount = 1
        case 2, 3:
            byteCount = 2
        case 4, 5, 6:
            byteCount = 4
        case 10, 11, 12:
            byteCount = 8
        case 8:
            try skipString()
            return
        case 9:
            let elementType = try readUInt32()
            let count = try readCount()
            for _ in 0..<count {
                try skip(valueType: elementType)
            }
            return
        default:
            throw MLXGGUFLoaderError.unsupportedMetadataType(valueType)
        }
        try skip(bytes: byteCount)
    }

    mutating func readMetadataValue(type: UInt32) throws -> MLXGGUFMetadataValue {
        switch type {
        case 0: return .uint8(try readUInt8())
        case 1: return .int8(Int8(bitPattern: try readUInt8()))
        case 2: return .uint16(try readUInt16())
        case 3: return .int16(Int16(bitPattern: try readUInt16()))
        case 4: return .uint32(try readUInt32())
        case 5: return .int32(Int32(bitPattern: try readUInt32()))
        case 6: return .float32(Float(bitPattern: try readUInt32()))
        case 7: return .boolean(try readUInt8() != 0)
        case 8: return .string(try readString())
        case 9:
            let elementType = try readUInt32()
            let count = try readCount()
            var values = [MLXGGUFMetadataValue]()
            values.reserveCapacity(count)
            for _ in 0..<count {
                values.append(try readMetadataValue(type: elementType))
            }
            return .array(values)
        case 10: return .uint64(try readUInt64())
        case 11: return .int64(Int64(bitPattern: try readUInt64()))
        case 12: return .float64(Double(bitPattern: try readUInt64()))
        default: throw MLXGGUFLoaderError.unsupportedMetadataType(type)
        }
    }
}

enum MLXGGUFLoaderError: LocalizedError, Sendable {
    case invalidMagic
    case unsupportedVersion(UInt32)
    case truncated
    case invalidSize
    case invalidText
    case invalidAlignment
    case unsupportedTensorType(UInt32, String)
    case invalidTensor(String)
    case duplicateWeight(String)
    case unsupportedMetadataType(UInt32)
    case unsupportedArchitectureVariant(architecture: String, reason: String)
    case unmappedWeights(architecture: String, names: [String])
    case embeddedConfigurationUnavailable(URL)
    case embeddedTokenizerUnavailable(URL)
    case missingRuntimeAssets(URL, [String])
    case missingConfiguration(URL)
    case invalidConfiguration(URL)
    case missingTokenizer(URL)
    case missingMultimodalProjector(URL)
    case ambiguousWeights(URL)

    var errorDescription: String? {
        switch self {
        case .invalidMagic:
            "GGUF 檔案標頭不正確。"
        case let .unsupportedVersion(version):
            "不支援 GGUF 版本：\(version)。"
        case .truncated:
            "GGUF 檔案內容不完整。"
        case .invalidSize:
            "GGUF 檔案尺寸超出目前平台可處理範圍。"
        case .invalidText:
            "GGUF 檔案包含無法解碼的文字欄位。"
        case .invalidAlignment:
            "GGUF 檔案的資料對齊設定不正確。"
        case let .unsupportedTensorType(type, name):
            "GGUF 權重「\(name)」使用目前未支援的量化型別 \(type)。"
        case let .invalidTensor(name):
            "GGUF 權重「\(name)」的形狀或資料範圍不正確。"
        case let .duplicateWeight(name):
            "GGUF 權重名稱重複：\(name)。"
        case let .unsupportedMetadataType(type):
            "GGUF metadata 型別 \(type) 不支援。"
        case let .unsupportedArchitectureVariant(architecture, reason):
            "GGUF 架構「\(architecture)」的此模型變體目前無法載入：\(reason)。"
        case let .unmappedWeights(architecture, names):
            "GGUF 架構「\(architecture)」包含尚未對應的權重：\(names.joined(separator: ", "))。"
        case let .embeddedConfigurationUnavailable(url):
            "GGUF 內嵌 metadata 無法建立模型設定：\(url.path)。"
        case let .embeddedTokenizerUnavailable(url):
            "GGUF 內嵌 metadata 無法建立 tokenizer：\(url.path)。"
        case let .missingRuntimeAssets(url, assets):
            "GGUF 模型缺少必要的 Hugging Face runtime 資產（\(assets.joined(separator: ", "))）：\(url.path)"
        case let .missingConfiguration(url):
            "GGUF 模型缺少設定檔：\(url.path)。"
        case let .invalidConfiguration(url):
            "無法解析 GGUF 模型設定檔：\(url.path)。"
        case let .missingTokenizer(url):
            "GGUF 模型缺少 tokenizer 檔案：\(url.path)。"
        case let .missingMultimodalProjector(url):
            "多模態 GGUF 模型缺少 mmproj 視覺投影檔：\(url.path)。"
        case let .ambiguousWeights(url):
            "GGUF 模型目錄中的權重檔不唯一：\(url.path)。"
        }
    }

}

enum MLXGGUFModelSource {
    static func missingRuntimeAssetNames(
        in directoryURL: URL,
        weightURL: URL? = nil
    ) -> [String] {
        let fileManager = FileManager.default
        let ggufFiles = files(in: directoryURL, withExtension: "gguf")
        let mainGGUF = weightURL ?? ggufFiles.first {
            !$0.lastPathComponent.lowercased().contains("mmproj")
        }
        var missing = [String]()
        let hasEmbeddedConfiguration = mainGGUF.map {
            MLXGGUFEmbeddedAssets.recognizesEmbeddedConfiguration(at: $0)
        } ?? false
        if !fileManager.fileExists(atPath: directoryURL.appendingPathComponent("config.json").path),
           !hasEmbeddedConfiguration {
            missing.append("config.json")
        }
        let hasTokenizer = fileManager.fileExists(
            atPath: directoryURL.appendingPathComponent("tokenizer.json").path
        ) || mainGGUF.map(MLXGGUFLoader.hasEmbeddedTokenizer) == true
        if !hasTokenizer {
            missing.append("tokenizer.json")
        }
        return missing
    }

    private static func files(in directoryURL: URL, withExtension pathExtension: String) -> [URL] {
        guard let enumerator = FileManager.default.enumerator(
            at: directoryURL,
            includingPropertiesForKeys: [.isRegularFileKey],
            options: [.skipsHiddenFiles]
        ) else { return [] }
        return enumerator.compactMap { item in
            guard let url = item as? URL,
                  url.pathExtension.lowercased() == pathExtension,
                  (try? url.resourceValues(forKeys: [.isRegularFileKey]).isRegularFile) == true
            else { return nil }
            return url
        }.sorted { $0.path < $1.path }
    }

}

enum MLXGGUFLoader {
    private final class MetadataCacheEntry: NSObject {
        let value: [String: MLXGGUFMetadataValue]

        init(_ value: [String: MLXGGUFMetadataValue]) {
            self.value = value
        }
    }

    private final class TensorInfoCacheEntry: NSObject {
        let value: [MLXGGUFTensorInfo]

        init(_ value: [MLXGGUFTensorInfo]) {
            self.value = value
        }
    }

    private final class InspectionCache: @unchecked Sendable {
        let metadata = NSCache<NSString, MetadataCacheEntry>()
        let tensorInfos = NSCache<NSString, TensorInfoCacheEntry>()
    }

    private static let inspectionCache = InspectionCache()

    static func metadata(from url: URL) throws -> [String: MLXGGUFMetadataValue] {
        let key = url.standardizedFileURL.path as NSString
        if let cached = inspectionCache.metadata.object(forKey: key) {
            return cached.value
        }
        let value = try readInspectionData(from: url).layout.metadata
        inspectionCache.metadata.setObject(MetadataCacheEntry(value), forKey: key)
        return value
    }

    static func tensorNames(from url: URL) throws -> [String] {
        try readTensorInspectionData(from: url).map(\.name)
    }

    static func hasEmbeddedTokenizer(at url: URL) -> Bool {
        guard let metadata = try? metadata(from: url) else { return false }
        guard let tokens = metadata["tokenizer.ggml.tokens"]?.arrayValue,
              !tokens.isEmpty,
              tokens.allSatisfy({ $0.stringValue != nil }) else {
            return false
        }
        if metadata["tokenizer.ggml.merges"]?.arrayValue != nil {
            return true
        }
        return metadata["tokenizer.ggml.scores"]?.arrayValue?.count == tokens.count
    }

    static func hasEmbeddedModelConfiguration(at url: URL) -> Bool {
        (try? MLXGGUFEmbeddedAssets.configurationData(weightURL: url, mmprojURL: nil)) != nil
    }

    static func inspect(from url: URL) throws -> GGUFBackendInspection {
        let inspectionData = try readInspectionData(from: url)
        let layout = inspectionData.layout
        let fileSize = try fileSize(of: url)
        let descriptors = layout.tensors.map { tensor in
            let elementCount = (try? checkedProduct(tensor.dimensions)) ?? 0
            let byteSize = try? tensorByteCount(
                type: tensor.type,
                elementCount: elementCount
            )
            return GGUFTensorDescriptor(
                name: tensor.name,
                shape: Array(tensor.dimensions.reversed()),
                type: ggufTypeName(tensor.type),
                offset: tensor.offset,
                byteSize: byteSize.map(UInt64.init),
                isMaterializable: GGUFStoragePolicy.isMaterializable(
                    ggufTypeName(tensor.type)
                ),
                storageType: GGUFStoragePolicy.storageType(for: ggufTypeName(tensor.type)),
                preservesSourceQuantization: GGUFStoragePolicy.preservesSourceQuantization(
                    for: ggufTypeName(tensor.type)
                ),
                requiresConversion: GGUFStoragePolicy.requiresConversion(
                    for: ggufTypeName(tensor.type)
                )
            )
        }
        var quantizationCounts = [String: Int]()
        for descriptor in descriptors {
            quantizationCounts[descriptor.type, default: 0] += 1
        }
        let unsupportedTypes = Set(
            descriptors
                .filter { !$0.isMaterializable }
                .map(\.type)
        ).sorted()
        return GGUFBackendInspection(
            version: layout.version,
            alignment: UInt64(layout.alignment),
            dataOffset: UInt64(layout.tensorDataOffset),
            fileSize: fileSize,
            metadataCount: layout.metadataCount,
            tensors: descriptors,
            quantizationCounts: quantizationCounts,
            unsupportedTypes: unsupportedTypes
        )
    }

    static func loadWeights(
        from url: URL,
        targetGroupSize: Int? = nil,
        quantizationProfile: GGUFQuantizationProfile = .automatic,
        stateSpaceParameterEncoding: GGUFStateSpaceParameterEncoding = .native,
        recurrentPromotion: GGUFRecurrentPromotionPolicy = .disabled,
        memoryMapped: Bool = false,
        progress: ((Int64, Int64) -> Void)? = nil
    ) throws -> [String: MLXArray] {
        guard targetGroupSize == nil || targetGroupSize == 32 || targetGroupSize == 64 else {
            throw MLXGGUFLoaderError.invalidTensor("量化群組設定")
        }
        let data: Data
        do {
            data = try Data(contentsOf: url, options: [.mappedIfSafe])
        } catch {
            throw MLXGGUFLoaderError.truncated
        }

        let layout = try parse(data)
        let tensors = layout.tensors
        let tensorDataOffset = layout.tensorDataOffset
        var tensorByteCounts = [Int64]()
        tensorByteCounts.reserveCapacity(tensors.count)
        var totalTensorBytes: Int64 = 0
        for tensor in tensors {
            let elementCount = try checkedProduct(tensor.dimensions)
            let byteCount = Int64(try tensorByteCount(
                type: tensor.type,
                elementCount: elementCount
            ))
            guard totalTensorBytes <= Int64.max - byteCount else {
                throw MLXGGUFLoaderError.invalidSize
            }
            tensorByteCounts.append(byteCount)
            totalTensorBytes += byteCount
        }
        if totalTensorBytes > 0 {
            progress?(0, totalTensorBytes)
        }
        let strategy = try quantizationStrategy(
            for: tensors,
            requestedGroupSize: targetGroupSize,
            profile: quantizationProfile
        )
        let resolvedGroupSize = strategy.groupSize
        var weights = [String: MLXArray]()
        var promotedRecurrentTensorCount = 0
        var completedTensorBytes: Int64 = 0
        for (tensorIndex, tensor) in tensors.enumerated() {
            defer {
                completedTensorBytes += tensorByteCounts[tensorIndex]
                progress?(completedTensorBytes, totalTensorBytes)
            }
            let shape = tensor.dimensions.reversed()
            let mlxShape = Array(shape)
            let elementCount = try checkedProduct(mlxShape)
            guard let support = GGUFStoragePolicy.support(
                for: ggufTypeName(tensor.type),
                profile: quantizationProfile
            ) else {
                throw MLXGGUFLoaderError.unsupportedTensorType(tensor.type, tensor.name)
            }
            if shouldPromoteRecurrentTensor(
                named: tensor.name,
                support: support,
                policy: recurrentPromotion
            ) {
                let value = try dequantizedArray(
                    data,
                    tensor: tensor,
                    shape: mlxShape,
                    elementCount: elementCount,
                    dataOffset: tensorDataOffset
                ).asType(.bfloat16)
                try insert(value, name: tensor.name, into: &weights)
                promotedRecurrentTensorCount += 1
                continue
            }
            switch support.materialization {
            case .dequantizedFP32:
                // quality 是不對外開啟的數值參考路徑：將可完整解碼的 GGUF
                // block 展開為 FP32，用來區分架構／權重映射問題、二次量化誤差
                // 與低精度矩陣累加誤差。
                try insert(
                    try dequantizedArray(
                        data,
                        tensor: tensor,
                        shape: mlxShape,
                        elementCount: elementCount,
                        dataOffset: tensorDataOffset
                    ),
                    name: tensor.name,
                    into: &weights
                )
            case .directFloat32:
                let value = try directArray(
                    from: url,
                    data: data,
                    tensor: tensor,
                    dataOffset: tensorDataOffset,
                    byteCount: try checkedByteCount(elementCount, elementSize: 4),
                    shape: mlxShape,
                    dtype: .float32,
                    memoryMapped: memoryMapped
                )
                let converted: MLXArray
                if Self.isStateSpaceParameterTensorName(tensor.name),
                   stateSpaceParameterEncoding != .native {
                    converted = GGUFStateSpaceParameterDecoder.decode(
                        value,
                        encoding: stateSpaceParameterEncoding
                    )
                } else {
                    converted = value.asType(.bfloat16)
                    converted.eval()
                }
                try insert(converted, name: tensor.name, into: &weights)
            case .directFloat16:
                let value = try directArray(
                    from: url,
                    data: data,
                    tensor: tensor,
                    dataOffset: tensorDataOffset,
                    byteCount: try checkedByteCount(elementCount, elementSize: 2),
                    shape: mlxShape,
                    dtype: .float16,
                    memoryMapped: memoryMapped
                )
                let converted = value.asType(.bfloat16)
                converted.eval()
                try insert(converted, name: tensor.name, into: &weights)
            case .directBFloat16:
                let value = try directArray(
                    from: url,
                    data: data,
                    tensor: tensor,
                    dataOffset: tensorDataOffset,
                    byteCount: try checkedByteCount(elementCount, elementSize: 2),
                    shape: mlxShape,
                    dtype: .bfloat16,
                    memoryMapped: memoryMapped
                )
                let converted: MLXArray
                if Self.isStateSpaceParameterTensorName(tensor.name),
                   stateSpaceParameterEncoding != .native {
                    converted = GGUFStateSpaceParameterDecoder.decode(
                        value,
                        encoding: stateSpaceParameterEncoding
                    )
                } else {
                    converted = value
                }
                try insert(converted, name: tensor.name, into: &weights)
            case .quantized4, .quantized8:
                let quantized = try quantizedArrays(
                    data,
                    tensor: tensor,
                    shape: mlxShape,
                    elementCount: elementCount,
                    dataOffset: tensorDataOffset,
                    targetGroupSize: resolvedGroupSize
                )
                for (name, array) in quantized {
                    try insert(array, name: name, into: &weights)
                }
            case .quantizedMXFP4:
                let quantized = try mxfp4Arrays(
                    data,
                    tensor: tensor,
                    shape: mlxShape,
                    elementCount: elementCount,
                    dataOffset: tensorDataOffset
                )
                let namePrefix = tensor.name.hasSuffix(".weight")
                    ? String(tensor.name.dropLast(".weight".count))
                    : tensor.name
                try insert(quantized.wq, name: tensor.name, into: &weights)
                try insert(quantized.scales, name: namePrefix + ".scales", into: &weights)
            case .requantized4, .requantized8:
                let bits = support.materialization == .requantized4 ? 4 : 8
                let quantized = try directlyRequantizedArrays(
                    data,
                    tensor: tensor,
                    shape: mlxShape,
                    elementCount: elementCount,
                    dataOffset: tensorDataOffset,
                    bits: bits,
                    targetGroupSize: resolvedGroupSize
                )
                let namePrefix = tensor.name.hasSuffix(".weight")
                    ? String(tensor.name.dropLast(".weight".count))
                    : tensor.name
                try insert(
                    quantized.wq,
                    name: tensor.name,
                    into: &weights
                )
                try insert(
                    quantized.scales,
                    name: namePrefix + ".scales",
                    into: &weights
                )
                try insert(
                    quantized.biases,
                    name: namePrefix + ".biases",
                    into: &weights
                )
            case .directInt8:
                try insert(
                    try directArray(
                        from: url,
                        data: data,
                        tensor: tensor,
                        dataOffset: tensorDataOffset,
                        byteCount: elementCount,
                        shape: mlxShape,
                        dtype: .int8,
                        memoryMapped: memoryMapped
                    ),
                    name: tensor.name,
                    into: &weights
                )
            case .directInt16:
                try insert(
                    try directArray(
                        from: url,
                        data: data,
                        tensor: tensor,
                        dataOffset: tensorDataOffset,
                        byteCount: try checkedByteCount(elementCount, elementSize: 2),
                        shape: mlxShape,
                        dtype: .int16,
                        memoryMapped: memoryMapped
                    ),
                    name: tensor.name,
                    into: &weights
                )
            case .directInt32:
                try insert(
                    try directArray(
                        from: url,
                        data: data,
                        tensor: tensor,
                        dataOffset: tensorDataOffset,
                        byteCount: try checkedByteCount(elementCount, elementSize: 4),
                        shape: mlxShape,
                        dtype: .int32,
                        memoryMapped: memoryMapped
                    ),
                    name: tensor.name,
                    into: &weights
                )
            }
        }
        if promotedRecurrentTensorCount > 0 {
            fputs(
                "GGUF recurrent promotion policy=\(recurrentPromotion.rawValue) "
                    + "tensors=\(promotedRecurrentTensorCount) storage=BF16\n",
                stderr
            )
        }
        return weights
    }

    private static func shouldPromoteRecurrentTensor(
        named name: String,
        support: GGUFTypeSupport,
        policy: GGUFRecurrentPromotionPolicy
    ) -> Bool {
        guard policy != .disabled,
              let role = recurrentTensorRole(for: name) else { return false }
        switch support.materialization {
        case .quantized4, .quantized8, .requantized4, .requantized8:
            break
        default:
            return false
        }
        switch (policy, role) {
        case (.controls, .control), (.all, .control), (.all, .data):
            return true
        default:
            return false
        }
    }

    /// GGUF 的 SSM 命名本身即描述 recurrent tensor 角色，因此可跨模型套用；
    /// 無 `blk.N` 與已知 SSM 尾碼的 tensor 不參與實驗。
    private static func recurrentTensorRole(for name: String) -> GGUFRecurrentTensorRole? {
        let parts = name.split(separator: ".", omittingEmptySubsequences: true)
        guard parts.count == 4,
              parts[0] == "blk",
              Int(parts[1]) != nil,
              parts[3] == "weight" else { return nil }
        switch parts[2] {
        case "ssm_alpha", "ssm_beta":
            return .control
        case "attn_qkv", "attn_gate", "ssm_conv1d", "ssm_out":
            return .data
        default:
            return nil
        }
    }

    /// 為一或多個 GGUF（主模型與可選 mmproj）建立同一份量化策略，確保後續
    /// `QuantizedLinear` 不會混用無法從權重形狀辨識的 group size。
    static func quantizationStrategy(
        from urls: [URL],
        requestedGroupSize: Int?,
        profile: GGUFQuantizationProfile
    ) throws -> MLXGGUFQuantizationStrategy {
        let tensors = try tensorInfos(from: urls)
        return try quantizationStrategy(
            for: tensors,
            requestedGroupSize: requestedGroupSize,
            profile: profile
        )
    }

    /// 依轉換後真正寫入 safetensors 的 dtype 與量化 companion tensors
    /// （scales／biases）估算永久快取大小。這是通用 tensor 級估算，未綁定
    /// 模型名稱；另保留少量 safetensors header 與檔案系統配置空間。
    static func estimatedConvertedWeightBytes(
        from urls: [URL],
        requestedGroupSize: Int?,
        profile: GGUFQuantizationProfile,
        recurrentPromotion: GGUFRecurrentPromotionPolicy
    ) throws -> Int64 {
        try conversionStorageAnalysis(
            from: urls,
            requestedGroupSize: requestedGroupSize,
            profile: profile,
            recurrentPromotion: recurrentPromotion
        ).estimatedBytes
    }

    static func conversionStorageAnalysis(
        from urls: [URL],
        requestedGroupSize: Int?,
        profile: GGUFQuantizationProfile,
        recurrentPromotion: GGUFRecurrentPromotionPolicy
    ) throws -> (strategy: MLXGGUFQuantizationStrategy, estimatedBytes: Int64) {
        let tensors = try tensorInfos(from: urls)
        let strategy = try quantizationStrategy(
            for: tensors,
            requestedGroupSize: requestedGroupSize,
            profile: profile
        )
        var byteCount = 0.0
        for tensor in tensors {
            let elements = Double(try checkedProduct(tensor.dimensions))
            guard let support = GGUFStoragePolicy.support(
                for: ggufTypeName(tensor.type),
                profile: profile
            ) else {
                throw MLXGGUFLoaderError.unsupportedTensorType(tensor.type, tensor.name)
            }
            if shouldPromoteRecurrentTensor(
                named: tensor.name,
                support: support,
                policy: recurrentPromotion
            ) {
                byteCount += elements * 2
                continue
            }
            switch support.materialization {
            case .dequantizedFP32:
                byteCount += elements * 4
            case .directFloat32, .directFloat16, .directBFloat16:
                byteCount += elements * 2
            case .directInt8:
                byteCount += elements
            case .directInt16:
                byteCount += elements * 2
            case .directInt32:
                byteCount += elements * 4
            case .quantized4, .requantized4:
                // Q4_K 走無損沿用時 group 固定 32，scale/bias 數量是 group 64 的兩倍。
                let effectiveGroup = tensor.type == 12 ? 32 : strategy.groupSize
                byteCount += elements * 0.5
                    + (elements / Double(effectiveGroup)) * 4
            case .quantized8, .requantized8:
                byteCount += elements
                    + (elements / Double(strategy.groupSize)) * 4
            case .quantizedMXFP4:
                byteCount += elements * 0.5 + elements / 32
            }
        }
        guard byteCount.isFinite, byteCount >= 0, byteCount <= Double(Int64.max) else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let headerAndAllocationAllowance = max(4_194_304, byteCount * 0.02)
        return (
            strategy,
            Int64((byteCount + headerAndAllocationAllowance).rounded(.up))
        )
    }

    private static func tensorInfos(from urls: [URL]) throws -> [MLXGGUFTensorInfo] {
        var tensors = [MLXGGUFTensorInfo]()
        for url in urls {
            tensors.append(contentsOf: try readTensorInspectionData(from: url))
        }
        return tensors
    }

    static func storedTensorByteCount(from url: URL) throws -> Int64 {
        var total: Int64 = 0
        for tensor in try readTensorInspectionData(from: url) {
            let elementCount = try checkedProduct(tensor.dimensions)
            let byteCount = Int64(try tensorByteCount(
                type: tensor.type,
                elementCount: elementCount
            ))
            guard total <= Int64.max - byteCount else {
                throw MLXGGUFLoaderError.invalidSize
            }
            total += byteCount
        }
        return total
    }

    /// 通用決策矩陣：浮點來源統一以 BF16 運算；Q8 保留 INT8；其餘支援的
    /// 低位元矩陣依 profile 落到 INT4／INT8。未指定時固定使用 Group 64；
    /// Group 32 只接受明確參數，絕不由 Runtime 靜默降級。若任一 affine tensor
    /// 的內層維度不相容，回報該 tensor 並要求使用者自行選擇其他策略。
    static func quantizationStrategy(
        for tensors: [MLXGGUFTensorInfo],
        requestedGroupSize: Int?,
        profile: GGUFQuantizationProfile
    ) throws -> MLXGGUFQuantizationStrategy {
        guard requestedGroupSize == nil
                || requestedGroupSize == 32
                || requestedGroupSize == 64 else {
            throw MLXGGUFLoaderError.invalidTensor("量化群組設定")
        }

        var storageCounts = [GGUFStorageType: Int]()
        var affineTensors = [MLXGGUFTensorInfo]()
        for tensor in tensors {
            guard let support = GGUFStoragePolicy.support(
                for: ggufTypeName(tensor.type),
                profile: profile
            ) else { continue }
            let targetStorage: GGUFStorageType
            switch support.materialization {
            case .directFloat32, .directFloat16, .directBFloat16:
                targetStorage = .bf16
            default:
                targetStorage = support.storageType
            }
            storageCounts[targetStorage, default: 0] += 1

            switch support.materialization {
            case .quantized4, .quantized8:
                affineTensors.append(tensor)
            case .requantized4, .requantized8:
                affineTensors.append(tensor)
            default:
                break
            }
        }

        func firstIncompatibleTensor(groupSize: Int) -> MLXGGUFTensorInfo? {
            affineTensors.first { tensor in
                guard let innerDimension = tensor.dimensions.first,
                      innerDimension > 0 else { return true }
                return innerDimension % groupSize != 0
            }
        }

        // 無損沿用來源 block 的張量各自固定 group 32（由 quantizedArrays 決定），
        // 其餘張量沿用這裡的全域選擇。兩者可以並存——重排流程已改為由實際 shape
        // 反推每個張量的 packing，不再假設全模型共用同一個 group。
        //
        // Mode 3 是例外：INT4 必須對齊 K-quant 的 32 元素 sub-block，group 64
        // 實測會產生亂碼，因此由策略固定為 32，不採用全域預設。
        let defaultGroupSize = profile == .mode3 ? 32 : 64
        let resolvedGroupSize = requestedGroupSize ?? defaultGroupSize
        if let invalid = firstIncompatibleTensor(groupSize: resolvedGroupSize) {
            throw MLXGGUFLoaderError.invalidTensor(invalid.name)
        }

        return MLXGGUFQuantizationStrategy(
            profile: profile,
            requestedGroupSize: requestedGroupSize,
            groupSize: resolvedGroupSize,
            targetStorageCounts: storageCounts
        )
    }

    /// `blk.N.ssm_a` 是 GGUF 的通用 SSM 參數角色；實際編碼公式由架構契約
    /// 決定。需要還原成 A_log 的架構保留 F32，其餘 F32 權重降為 BF16。
    private static func isStateSpaceParameterTensorName(_ name: String) -> Bool {
        let components = name.split(separator: ".")
        return components.count == 3
            && components[0] == "blk"
            && Int(components[1]) != nil
            && components[2] == "ssm_a"
    }

    private static func readInspectionData(
        from url: URL
    ) throws -> (data: Data, layout: MLXGGUFFileLayout) {
        let totalSize = try fileSize(of: url)
        let handle: FileHandle
        do {
            handle = try FileHandle(forReadingFrom: url)
        } catch {
            throw MLXGGUFLoaderError.truncated
        }
        defer { try? handle.close() }

        var readSize = min(totalSize, UInt64(1_048_576))
        while readSize > 0 {
            do {
                try handle.seek(toOffset: 0)
                guard let data = try handle.read(upToCount: Int(readSize)) else {
                    throw MLXGGUFLoaderError.truncated
                }
                do {
                    return (data, try parse(data))
                } catch MLXGGUFLoaderError.truncated where readSize < totalSize {
                    readSize = min(totalSize, readSize * 2)
                }
            } catch let error as MLXGGUFLoaderError {
                throw error
            } catch {
                throw MLXGGUFLoaderError.truncated
            }
        }
        throw MLXGGUFLoaderError.truncated
    }

    /// 預檢只需要 tensor table；metadata 的大型 tokenizer 陣列直接略過，
    /// 不建立數十萬個 Swift value，避免顯示確認視窗前消耗大量 CPU／記憶體。
    private static func readTensorInspectionData(
        from url: URL
    ) throws -> [MLXGGUFTensorInfo] {
        let cacheKey = url.standardizedFileURL.path as NSString
        if let cached = inspectionCache.tensorInfos.object(forKey: cacheKey) {
            return cached.value
        }
        let totalSize = try fileSize(of: url)
        let handle: FileHandle
        do {
            handle = try FileHandle(forReadingFrom: url)
        } catch {
            throw MLXGGUFLoaderError.truncated
        }
        defer { try? handle.close() }
        var readSize = min(totalSize, UInt64(1_048_576))
        while readSize > 0 {
            try handle.seek(toOffset: 0)
            guard let data = try handle.read(upToCount: Int(readSize)) else {
                throw MLXGGUFLoaderError.truncated
            }
            do {
                let value = try parseTensorInfos(data)
                inspectionCache.tensorInfos.setObject(
                    TensorInfoCacheEntry(value),
                    forKey: cacheKey
                )
                return value
            } catch MLXGGUFLoaderError.truncated where readSize < totalSize {
                readSize = min(totalSize, readSize * 2)
            }
        }
        throw MLXGGUFLoaderError.truncated
    }

    private static func parseTensorInfos(_ data: Data) throws -> [MLXGGUFTensorInfo] {
        var reader = MLXGGUFReader(data: data)
        guard try reader.readUInt32() == 0x46554747 else {
            throw MLXGGUFLoaderError.invalidMagic
        }
        let version = try reader.readUInt32()
        guard version == 2 || version == 3 else {
            throw MLXGGUFLoaderError.unsupportedVersion(version)
        }
        let tensorCount = try reader.readCount()
        let metadataCount = try reader.readCount()
        for _ in 0..<metadataCount {
            _ = try reader.readString()
            try reader.skip(valueType: reader.readUInt32())
        }
        var tensors = [MLXGGUFTensorInfo]()
        tensors.reserveCapacity(tensorCount)
        for _ in 0..<tensorCount {
            let name = try reader.readString()
            let dimensionCount = try reader.readUInt32()
            guard UInt64(dimensionCount) <= UInt64(Int.max) else {
                throw MLXGGUFLoaderError.invalidSize
            }
            var dimensions = [Int]()
            dimensions.reserveCapacity(Int(dimensionCount))
            for _ in 0..<dimensionCount {
                dimensions.append(try reader.readCount())
            }
            tensors.append(
                MLXGGUFTensorInfo(
                    name: name,
                    dimensions: dimensions,
                    type: try reader.readUInt32(),
                    offset: try reader.readUInt64()
                )
            )
        }
        return tensors
    }

    private static func fileSize(of url: URL) throws -> UInt64 {
        let values: URLResourceValues
        do {
            values = try url.resourceValues(forKeys: [.fileSizeKey])
        } catch {
            throw MLXGGUFLoaderError.truncated
        }
        guard let size = values.fileSize, size >= 0 else {
            throw MLXGGUFLoaderError.truncated
        }
        return UInt64(size)
    }

    private static func parse(_ data: Data) throws -> MLXGGUFFileLayout {
        var reader = MLXGGUFReader(data: data)
        let magic = try reader.readUInt32()
        guard magic == 0x46554747 else { throw MLXGGUFLoaderError.invalidMagic }
        let version = try reader.readUInt32()
        guard version == 2 || version == 3 else {
            throw MLXGGUFLoaderError.unsupportedVersion(version)
        }
        let tensorCount = try reader.readCount()
        let metadataCount = try reader.readCount()
        var alignment = 32
        var metadata = [String: MLXGGUFMetadataValue]()
        metadata.reserveCapacity(metadataCount)

        for _ in 0..<metadataCount {
            let key = try reader.readString()
            let valueType = try reader.readUInt32()
            let value = try reader.readMetadataValue(type: valueType)
            metadata[key] = value
            if key == "general.alignment", valueType == 4 {
                alignment = value.integerValue ?? 0
            } else if key == "general.alignment", valueType == 10 {
                alignment = value.integerValue ?? 0
            }
        }
        guard alignment > 0 else { throw MLXGGUFLoaderError.invalidAlignment }

        var tensors = [MLXGGUFTensorInfo]()
        tensors.reserveCapacity(tensorCount)
        for _ in 0..<tensorCount {
            let name = try reader.readString()
            let dimensionCount = try reader.readUInt32()
            guard UInt64(dimensionCount) <= UInt64(Int.max) else {
                throw MLXGGUFLoaderError.invalidSize
            }
            var dimensions = [Int]()
            dimensions.reserveCapacity(Int(dimensionCount))
            for _ in 0..<dimensionCount {
                dimensions.append(try reader.readCount())
            }
            let type = try reader.readUInt32()
            let offset = try reader.readUInt64()
            tensors.append(
                MLXGGUFTensorInfo(
                    name: name,
                    dimensions: dimensions,
                    type: type,
                    offset: offset
                )
            )
        }

        let remainder = reader.offset % alignment
        if remainder != 0 {
            try reader.skip(bytes: alignment - remainder)
        }
        return MLXGGUFFileLayout(
            version: version,
            alignment: alignment,
            tensorDataOffset: reader.offset,
            metadataCount: metadataCount,
            metadata: metadata,
            tensors: tensors
        )
    }

    private static func tensorByteCount(
        type: UInt32,
        elementCount: Int
    ) throws -> Int {
        switch type {
        case 0: return try checkedByteCount(elementCount, elementSize: 4)
        case 1: return try checkedByteCount(elementCount, elementSize: 2)
        case 2: return try checkedByteCount(try divisible(elementCount, by: 32), elementSize: 18)
        case 3: return try checkedByteCount(try divisible(elementCount, by: 32), elementSize: 20)
        case 8: return try checkedByteCount(try divisible(elementCount, by: 32), elementSize: 34)
        case 10: return try checkedByteCount(try divisible(elementCount, by: 256), elementSize: 84)
        case 11: return try checkedByteCount(try divisible(elementCount, by: 256), elementSize: 110)
        case 12: return try checkedByteCount(try divisible(elementCount, by: 256), elementSize: 144)
        case 13: return try checkedByteCount(try divisible(elementCount, by: 256), elementSize: 176)
        case 14: return try checkedByteCount(try divisible(elementCount, by: 256), elementSize: 210)
        case 20: return try checkedByteCount(try divisible(elementCount, by: 32), elementSize: 18)
        case 21: return try checkedByteCount(try divisible(elementCount, by: 256), elementSize: 110)
        case 23: return try checkedByteCount(try divisible(elementCount, by: 256), elementSize: 136)
        case 39: return try checkedByteCount(try divisible(elementCount, by: 32), elementSize: 17)
        case 24: return elementCount
        case 25: return try checkedByteCount(elementCount, elementSize: 2)
        case 26: return try checkedByteCount(elementCount, elementSize: 4)
        case 30: return try checkedByteCount(elementCount, elementSize: 2)
        case 41: return try checkedByteCount(try divisible(elementCount, by: 128), elementSize: 18)
        case 42: return try checkedByteCount(try divisible(elementCount, by: 64), elementSize: 18)
        default: throw MLXGGUFLoaderError.unsupportedTensorType(type, "")
        }
    }

    private static func divisible(_ value: Int, by divisor: Int) throws -> Int {
        guard value >= 0, value % divisor == 0 else {
            throw MLXGGUFLoaderError.invalidSize
        }
        return value / divisor
    }

    private static func ggufTypeName(_ type: UInt32) -> String {
        switch type {
        case 0: return "F32"
        case 1: return "F16"
        case 2: return "Q4_0"
        case 3: return "Q4_1"
        case 6: return "Q5_0"
        case 7: return "Q5_1"
        case 8: return "Q8_0"
        case 9: return "Q8_1"
        case 10: return "Q2_K"
        case 11: return "Q3_K"
        case 12: return "Q4_K"
        case 13: return "Q5_K"
        case 14: return "Q6_K"
        case 15: return "Q8_K"
        case 16: return "IQ2_XXS"
        case 17: return "IQ2_XS"
        case 18: return "IQ3_XXS"
        case 19: return "IQ1_S"
        case 20: return "IQ4_NL"
        case 21: return "IQ3_S"
        case 22: return "IQ2_S"
        case 23: return "IQ4_XS"
        case 24: return "I8"
        case 25: return "I16"
        case 26: return "I32"
        case 27: return "I64"
        case 28: return "F64"
        case 29: return "IQ1_M"
        case 30: return "BF16"
        case 34: return "TQ1_0"
        case 35: return "TQ2_0"
        case 39: return "MXFP4"
        case 40: return "NVFP4"
        case 41: return "Q1_0"
        case 42: return "Q2_0"
        default: return "TYPE_\(type)"
        }
    }

    private static func quantizedArrays(
        _ data: Data,
        tensor: MLXGGUFTensorInfo,
        shape: [Int],
        elementCount: Int,
        dataOffset: Int,
        targetGroupSize: Int
    ) throws -> [String: MLXArray] {
        guard let lastDimension = shape.last,
              lastDimension > 0,
              lastDimension % 32 == 0 else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }
        let bits: Int
        let bytesPerBlock: Int
        let elementsPerBlock: Int
        switch tensor.type {
        case 2:
            bits = 4
            bytesPerBlock = 18
            elementsPerBlock = 32
        case 3:
            bits = 4
            bytesPerBlock = 20
            elementsPerBlock = 32
        case 8:
            bits = 8
            bytesPerBlock = 34
            elementsPerBlock = 32
        case 12:
            // Q4_K：256 元素 super-block，內含 8 個 32 元素 sub-block。
            bits = 4
            bytesPerBlock = 144
            elementsPerBlock = 256
        default:
            throw MLXGGUFLoaderError.unsupportedTensorType(tensor.type, tensor.name)
        }
        guard lastDimension % elementsPerBlock == 0 else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }
        // 無損沿用來源量化值的前提是 MLX group 與來源 block 對齊；這些格式的
        // block（Q4_K 則是 sub-block）都是 32 元素，因此固定 32。Q4_K 的結構
        // 決定它一定要走 32，不受 auto=64 影響。
        let preservedGroupSize = 32
        let canPreserve = tensor.type == 12 || targetGroupSize == preservedGroupSize
        let blockCount = elementCount / elementsPerBlock
        let byteCount = try checkedByteCount(blockCount, elementSize: bytesPerBlock)
        let raw = try dataSlice(data, tensor: tensor, dataOffset: dataOffset, byteCount: byteCount)
        var weightShape = shape
        weightShape[weightShape.count - 1] /= bits == 4 ? 8 : 4
        var scaleShape = shape
        scaleShape[scaleShape.count - 1] /= canPreserve ? preservedGroupSize : targetGroupSize
        let namePrefix = tensor.name.hasSuffix(".weight")
            ? String(tensor.name.dropLast(".weight".count))
            : tensor.name
        let packed: (wq: MLXArray, scales: MLXArray, biases: MLXArray)
        if canPreserve {
            packed = try MLXGGUFMetalQuantizer.packPreserved(
                raw: raw,
                sourceType: tensor.type,
                sourceShape: shape,
                targetWeightShape: weightShape,
                targetScaleShape: scaleShape
            )
        } else {
            packed = try MLXGGUFMetalQuantizer.quantize(
                raw: raw,
                sourceType: tensor.type,
                targetBits: bits,
                sourceShape: shape,
                targetGroupSize: targetGroupSize,
                targetWeightShape: weightShape,
                targetScaleShape: scaleShape
            )
        }
        return [
            tensor.name: packed.wq,
            namePrefix + ".scales": packed.scales,
            namePrefix + ".biases": packed.biases
        ]
    }

    private static func mxfp4Arrays(
        _ data: Data,
        tensor: MLXGGUFTensorInfo,
        shape: [Int],
        elementCount: Int,
        dataOffset: Int
    ) throws -> (wq: MLXArray, scales: MLXArray) {
        guard tensor.type == 39,
              let lastDimension = shape.last,
              lastDimension > 0,
              lastDimension % 32 == 0,
              elementCount % 32 == 0 else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }
        let blockCount = elementCount / 32
        let byteCount = try checkedByteCount(blockCount, elementSize: 17)
        let raw = try dataSlice(data, tensor: tensor, dataOffset: dataOffset, byteCount: byteCount)
        var weightShape = shape
        weightShape[weightShape.count - 1] /= 8
        var scaleShape = shape
        scaleShape[scaleShape.count - 1] /= 32
        return try MLXGGUFMetalQuantizer.packMXFP4(
            raw: raw,
            sourceShape: shape,
            targetWeightShape: weightShape,
            targetScaleShape: scaleShape
        )
    }

    private static func directlyRequantizedArrays(
        _ data: Data,
        tensor: MLXGGUFTensorInfo,
        shape: [Int],
        elementCount: Int,
        dataOffset: Int,
        bits: Int,
        targetGroupSize: Int
    ) throws -> (wq: MLXArray, scales: MLXArray, biases: MLXArray) {
        guard bits == 4 || bits == 8,
              targetGroupSize == 32 || targetGroupSize == 64,
              let lastDimension = shape.last,
              lastDimension > 0,
              lastDimension % targetGroupSize == 0,
              elementCount % targetGroupSize == 0 else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }

        let elementsPerBlock: Int
        let bytesPerBlock: Int
        switch tensor.type {
        case 2:
            elementsPerBlock = 32
            bytesPerBlock = 18
        case 3:
            elementsPerBlock = 32
            bytesPerBlock = 20
        case 8:
            elementsPerBlock = 32
            bytesPerBlock = 34
        case 10:
            elementsPerBlock = 256
            bytesPerBlock = 84
        case 11:
            elementsPerBlock = 256
            bytesPerBlock = 110
        case 12:
            elementsPerBlock = 256
            bytesPerBlock = 144
        case 13:
            elementsPerBlock = 256
            bytesPerBlock = 176
        case 14:
            elementsPerBlock = 256
            bytesPerBlock = 210
        case 20:
            elementsPerBlock = 32
            bytesPerBlock = 18
        case 21:
            elementsPerBlock = 256
            bytesPerBlock = 110
        case 23:
            elementsPerBlock = 256
            bytesPerBlock = 136
        case 41, 42:
            elementsPerBlock = tensor.type == 41 ? 128 : 64
            bytesPerBlock = 18
        default:
            throw MLXGGUFLoaderError.unsupportedTensorType(tensor.type, tensor.name)
        }
        guard elementCount % elementsPerBlock == 0 else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }

        let blockCount = elementCount / elementsPerBlock
        let byteCount = try checkedByteCount(blockCount, elementSize: bytesPerBlock)
        let raw = try dataSlice(data, tensor: tensor, dataOffset: dataOffset, byteCount: byteCount)
        var weightShape = shape
        weightShape[weightShape.count - 1] /= bits == 4 ? 8 : 4
        var scaleShape = shape
        scaleShape[scaleShape.count - 1] /= targetGroupSize
        return try MLXGGUFMetalQuantizer.quantize(
            raw: raw,
            sourceType: tensor.type,
            targetBits: bits,
            sourceShape: shape,
            targetGroupSize: targetGroupSize,
            targetWeightShape: weightShape,
            targetScaleShape: scaleShape
        )
    }

    private static func directlyQuantizeQ1_0(
        _ raw: Data,
        rawOffset: Int,
        targetGroupOffset: Int,
        values: inout [Float],
        packedWeights: inout [UInt8],
        scales: inout [UInt16],
        biases: inout [UInt16],
        bits: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        for group in 0..<4 {
            let sourceOffset = group * 32
            for index in 0..<32 {
                let sourceIndex = sourceOffset + index
                let bit = (raw[rawOffset + 2 + sourceIndex / 8] >> (sourceIndex % 8)) & 1
                values[index] = Float(Float16(bit == 0 ? -scale : scale))
            }
            appendAffineQuantizedGroup(
                values,
                groupIndex: targetGroupOffset + group,
                bits: bits,
                packedWeights: &packedWeights,
                scales: &scales,
                biases: &biases
            )
        }
    }

    private static func directlyQuantizeQ2_0(
        _ raw: Data,
        rawOffset: Int,
        targetGroupOffset: Int,
        values: inout [Float],
        packedWeights: inout [UInt8],
        scales: inout [UInt16],
        biases: inout [UInt16],
        bits: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        for group in 0..<2 {
            let sourceOffset = group * 32
            for index in 0..<32 {
                let sourceIndex = sourceOffset + index
                let quantized = (raw[rawOffset + 2 + sourceIndex / 4]
                    >> ((sourceIndex % 4) * 2)) & 3
                values[index] = Float(Float16(Float(Int(quantized) - 1) * scale))
            }
            appendAffineQuantizedGroup(
                values,
                groupIndex: targetGroupOffset + group,
                bits: bits,
                packedWeights: &packedWeights,
                scales: &scales,
                biases: &biases
            )
        }
    }

    private static func directlyQuantizeQ2K(
        _ raw: Data,
        rawOffset: Int,
        targetGroupOffset: Int,
        values: inout [Float],
        packedWeights: inout [UInt8],
        scales: inout [UInt16],
        biases: inout [UInt16],
        bits: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        let minimumScale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 2)))
        let scalesOffset = rawOffset + 4
        let quantizedOffset = rawOffset + 20

        for half in 0..<2 {
            let quantizedHalfOffset = quantizedOffset + half * 32
            for chunk in 0..<4 {
                let targetGroup = targetGroupOffset + half * 4 + chunk
                let shift = chunk * 2
                let firstScaleByte = raw[scalesOffset + (half * 8) + chunk * 2]
                let secondScaleByte = raw[scalesOffset + (half * 8) + chunk * 2 + 1]
                let first = scale * Float(firstScaleByte & 0x0f)
                let firstMinimum = minimumScale * Float(firstScaleByte >> 4)
                let second = scale * Float(secondScaleByte & 0x0f)
                let secondMinimum = minimumScale * Float(secondScaleByte >> 4)
                for index in 0..<16 {
                    let firstQuantized = (raw[quantizedHalfOffset + index] >> shift) & 3
                    let secondQuantized = (raw[quantizedHalfOffset + index + 16] >> shift) & 3
                    values[index] = Float(Float16(
                        first * Float(firstQuantized) - firstMinimum
                    ))
                    values[index + 16] = Float(Float16(
                        second * Float(secondQuantized) - secondMinimum
                    ))
                }
                appendAffineQuantizedGroup(
                    values,
                    groupIndex: targetGroup,
                    bits: bits,
                    packedWeights: &packedWeights,
                    scales: &scales,
                    biases: &biases
                )
            }
        }
    }

    private static func directlyQuantizeQ3K(
        _ raw: Data,
        rawOffset: Int,
        targetGroupOffset: Int,
        values: inout [Float],
        packedWeights: inout [UInt8],
        scales: inout [UInt16],
        biases: inout [UInt16],
        bits: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 108)))
        let scaleWords = q3KScaleWords(raw, offset: rawOffset + 96)
        let quantizedOffset = rawOffset + 32

        for half in 0..<2 {
            let quantizedHalfOffset = quantizedOffset + half * 32
            for chunk in 0..<4 {
                let targetGroup = targetGroupOffset + half * 4 + chunk
                let shift = chunk * 2
                let highBitMask = UInt8(1 << (half * 4 + chunk))
                let firstScale = scale * Float(q3KScaleValue(scaleWords, index: half * 8 + chunk * 2))
                let secondScale = scale * Float(q3KScaleValue(scaleWords, index: half * 8 + chunk * 2 + 1))
                for index in 0..<16 {
                    let firstLow = (raw[quantizedHalfOffset + index] >> shift) & 3
                    let firstHigh = (raw[rawOffset + index] & highBitMask) == 0 ? 4 : 0
                    let secondLow = (raw[quantizedHalfOffset + index + 16] >> shift) & 3
                    let secondHigh = (raw[rawOffset + index + 16] & highBitMask) == 0 ? 4 : 0
                    values[index] = Float(Float16(
                        firstScale * Float(Int(firstLow) - firstHigh)
                    ))
                    values[index + 16] = Float(Float16(
                        secondScale * Float(Int(secondLow) - secondHigh)
                    ))
                }
                appendAffineQuantizedGroup(
                    values,
                    groupIndex: targetGroup,
                    bits: bits,
                    packedWeights: &packedWeights,
                    scales: &scales,
                    biases: &biases
                )
            }
        }
    }

    private static func directlyQuantizeQ4K(
        _ raw: Data,
        rawOffset: Int,
        targetGroupOffset: Int,
        values: inout [Float],
        packedWeights: inout [UInt8],
        scales: inout [UInt16],
        biases: inout [UInt16],
        bits: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        let minimumScale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 2)))
        let scalesOffset = rawOffset + 4
        let quantizedOffset = rawOffset + 16
        for segment in 0..<4 {
            let firstScaleIndex = segment * 2
            let first = kScaleAndMin(index: firstScaleIndex, raw: raw, offset: scalesOffset)
            let second = kScaleAndMin(index: firstScaleIndex + 1, raw: raw, offset: scalesOffset)
            let segmentOffset = quantizedOffset + segment * 32
            for index in 0..<32 {
                let quantized = raw[segmentOffset + index]
                values[index] = Float(Float16(
                    scale * Float(first.0) * Float(quantized & 0x0f)
                        - minimumScale * Float(first.1)
                ))
            }
            appendAffineQuantizedGroup(
                values,
                groupIndex: targetGroupOffset + segment * 2,
                bits: bits,
                packedWeights: &packedWeights,
                scales: &scales,
                biases: &biases
            )
            for index in 0..<32 {
                let quantized = raw[segmentOffset + index]
                values[index] = Float(Float16(
                    scale * Float(second.0) * Float(quantized >> 4)
                        - minimumScale * Float(second.1)
                ))
            }
            appendAffineQuantizedGroup(
                values,
                groupIndex: targetGroupOffset + segment * 2 + 1,
                bits: bits,
                packedWeights: &packedWeights,
                scales: &scales,
                biases: &biases
            )
        }
    }

    private static func directlyQuantizeQ5K(
        _ raw: Data,
        rawOffset: Int,
        targetGroupOffset: Int,
        values: inout [Float],
        packedWeights: inout [UInt8],
        scales: inout [UInt16],
        biases: inout [UInt16],
        bits: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        let minimumScale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 2)))
        let highBitsOffset = rawOffset + 16
        let lowBitsOffset = rawOffset + 48
        let scalesOffset = rawOffset + 4
        for segment in 0..<4 {
            let first = kScaleAndMin(index: segment * 2, raw: raw, offset: scalesOffset)
            let second = kScaleAndMin(index: segment * 2 + 1, raw: raw, offset: scalesOffset)
            let lowOffset = lowBitsOffset + segment * 32
            let highBit1 = UInt8(1 << (segment * 2))
            let highBit2 = UInt8(2 << (segment * 2))
            for index in 0..<32 {
                let high = raw[highBitsOffset + index]
                let firstQuantized = Int(raw[lowOffset + index] & 0x0f)
                    + ((high & highBit1) == 0 ? 0 : 16)
                values[index] = Float(Float16(
                    scale * Float(first.0) * Float(firstQuantized)
                        - minimumScale * Float(first.1)
                ))
            }
            appendAffineQuantizedGroup(
                values,
                groupIndex: targetGroupOffset + segment * 2,
                bits: bits,
                packedWeights: &packedWeights,
                scales: &scales,
                biases: &biases
            )
            for index in 0..<32 {
                let high = raw[highBitsOffset + index]
                let secondQuantized = Int(raw[lowOffset + index] >> 4)
                    + ((high & highBit2) == 0 ? 0 : 16)
                values[index] = Float(Float16(
                    scale * Float(second.0) * Float(secondQuantized)
                        - minimumScale * Float(second.1)
                ))
            }
            appendAffineQuantizedGroup(
                values,
                groupIndex: targetGroupOffset + segment * 2 + 1,
                bits: bits,
                packedWeights: &packedWeights,
                scales: &scales,
                biases: &biases
            )
        }
    }

    private static func directlyQuantizeQ6K(
        _ raw: Data,
        rawOffset: Int,
        targetGroupOffset: Int,
        values: inout [Float],
        packedWeights: inout [UInt8],
        scales: inout [UInt16],
        biases: inout [UInt16],
        bits: Int
    ) {
        let lowBitsOffset = rawOffset
        let highBitsOffset = rawOffset + 128
        let scalesOffset = rawOffset + 192
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 208)))
        for half in 0..<2 {
            let lowOffset = lowBitsOffset + half * 64
            let highOffset = highBitsOffset + half * 32
            let scaleOffset = scalesOffset + half * 8
            for plane in 0..<4 {
                for index in 0..<32 {
                    let high = raw[highOffset + index]
                    let quantized: Int
                    switch plane {
                    case 0:
                        quantized = Int((raw[lowOffset + index] & 0x0f)
                            | ((high & 0x03) << 4)) - 32
                    case 1:
                        quantized = Int((raw[lowOffset + index + 32] & 0x0f)
                            | (((high >> 2) & 0x03) << 4)) - 32
                    case 2:
                        quantized = Int((raw[lowOffset + index] >> 4)
                            | (((high >> 4) & 0x03) << 4)) - 32
                    default:
                        quantized = Int((raw[lowOffset + index + 32] >> 4)
                            | (((high >> 6) & 0x03) << 4)) - 32
                    }
                    let sourceScale = Float(Int8(bitPattern: raw[scaleOffset + plane * 2 + index / 16]))
                    values[index] = Float(Float16(scale * sourceScale * Float(quantized)))
                }
                appendAffineQuantizedGroup(
                    values,
                    groupIndex: targetGroupOffset + half * 4 + plane,
                    bits: bits,
                    packedWeights: &packedWeights,
                    scales: &scales,
                    biases: &biases
                )
            }
        }
    }

    private static func appendAffineQuantizedGroup(
        _ values: [Float],
        groupIndex: Int,
        bits: Int,
        packedWeights: inout [UInt8],
        scales: inout [UInt16],
        biases: inout [UInt16]
    ) {
        let maximumQuantizedValue = Float((1 << bits) - 1)
        var minimum = values[0]
        var maximum = values[0]
        for value in values.dropFirst() {
            minimum = min(minimum, value)
            maximum = max(maximum, value)
        }

        var scale = max((maximum - minimum) / maximumQuantizedValue, 1e-7)
        let usesMinimumAsEdge = abs(minimum) > abs(maximum)
        scale = usesMinimumAsEdge ? scale : -scale
        let edge = usesMinimumAsEdge ? minimum : maximum
        let initialQuantizedEdge = (edge / scale).rounded()
        let bias: Float
        if initialQuantizedEdge == 0 {
            bias = 0
        } else {
            scale = edge / initialQuantizedEdge
            bias = edge
        }
        scales[groupIndex] = Float16(scale).bitPattern
        biases[groupIndex] = Float16(bias).bitPattern

        if bits == 8 {
            let outputOffset = groupIndex * 32
            for index in 0..<32 {
                let quantized = min(
                    max(((values[index] - bias) / scale).rounded(), 0),
                    maximumQuantizedValue
                )
                packedWeights[outputOffset + index] = UInt8(quantized)
            }
        } else {
            let outputOffset = groupIndex * 16
            for index in stride(from: 0, to: 32, by: 2) {
                let first = min(
                    max(((values[index] - bias) / scale).rounded(), 0),
                    maximumQuantizedValue
                )
                let second = min(
                    max(((values[index + 1] - bias) / scale).rounded(), 0),
                    maximumQuantizedValue
                )
                packedWeights[outputOffset + index / 2] =
                    UInt8(first) | (UInt8(second) << 4)
            }
        }
    }

    private static func q3KScaleWords(
        _ raw: Data,
        offset: Int
    ) -> (UInt32, UInt32, UInt32, UInt32) {
        var first = littleEndianUInt32(raw, at: offset)
        var second = littleEndianUInt32(raw, at: offset + 4)
        let packed = littleEndianUInt32(raw, at: offset + 8)
        let mask1: UInt32 = 0x03030303
        let mask2: UInt32 = 0x0f0f0f0f
        let third = ((first >> 4) & mask2) | (((packed >> 4) & mask1) << 4)
        let fourth = ((second >> 4) & mask2) | (((packed >> 6) & mask1) << 4)
        first = (first & mask2) | (((packed >> 0) & mask1) << 4)
        second = (second & mask2) | (((packed >> 2) & mask1) << 4)
        return (first, second, third, fourth)
    }

    private static func q3KScaleValue(
        _ words: (UInt32, UInt32, UInt32, UInt32),
        index: Int
    ) -> Int8 {
        let word: UInt32
        switch index / 4 {
        case 0: word = words.0
        case 1: word = words.1
        case 2: word = words.2
        default: word = words.3
        }
        return Int8(bitPattern: UInt8(truncatingIfNeeded: word >> ((index % 4) * 8))) - 32
    }

    private static func dequantizedArray(
        _ data: Data,
        tensor: MLXGGUFTensorInfo,
        shape: [Int],
        elementCount: Int,
        dataOffset: Int
    ) throws -> MLXArray {
        let bytesPerBlock: Int
        let elementsPerBlock: Int
        switch tensor.type {
        case 2:
            bytesPerBlock = 18
            elementsPerBlock = 32
        case 3:
            bytesPerBlock = 20
            elementsPerBlock = 32
        case 8:
            bytesPerBlock = 34
            elementsPerBlock = 32
        case 10:
            bytesPerBlock = 84
            elementsPerBlock = 256
        case 11:
            bytesPerBlock = 110
            elementsPerBlock = 256
        case 12:
            bytesPerBlock = 144
            elementsPerBlock = 256
        case 13:
            bytesPerBlock = 176
            elementsPerBlock = 256
        case 14:
            bytesPerBlock = 210
            elementsPerBlock = 256
        case 41:
            bytesPerBlock = 18
            elementsPerBlock = 128
        case 42:
            bytesPerBlock = 18
            elementsPerBlock = 64
        default:
            throw MLXGGUFLoaderError.unsupportedTensorType(tensor.type, tensor.name)
        }
        guard let lastDimension = shape.last,
              lastDimension > 0,
              lastDimension % elementsPerBlock == 0 else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }
        guard elementCount % elementsPerBlock == 0 else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }
        let blockCount = elementCount / elementsPerBlock
        let byteCount = try checkedByteCount(blockCount, elementSize: bytesPerBlock)
        let raw = try dataSlice(data, tensor: tensor, dataOffset: dataOffset, byteCount: byteCount)
        var values = [Float16](repeating: 0, count: elementCount)

        for block in 0..<blockCount {
            let rawOffset = block * bytesPerBlock
            if tensor.type == 2 {
                dequantizeQ40(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * elementsPerBlock
                )
            } else if tensor.type == 3 {
                dequantizeQ41(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * elementsPerBlock
                )
            } else if tensor.type == 8 {
                dequantizeQ80(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * elementsPerBlock
                )
            } else if tensor.type == 10 {
                dequantizeQ2K(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * elementsPerBlock
                )
            } else if tensor.type == 11 {
                dequantizeQ3K(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * elementsPerBlock
                )
            } else if tensor.type == 12 {
                dequantizeQ4K(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * 256
                )
            } else if tensor.type == 13 {
                dequantizeQ5K(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * 256
                )
            } else if tensor.type == 14 {
                dequantizeQ6K(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * 256
                )
            } else if tensor.type == 41 {
                dequantizeQ1_0(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * elementsPerBlock
                )
            } else if tensor.type == 42 {
                dequantizeQ2_0(
                    raw,
                    rawOffset: rawOffset,
                    values: &values,
                    outputOffset: block * elementsPerBlock
                )
            }
        }

        let valuesArray = MLXArray(
            Data(bytes: values, count: values.count * MemoryLayout<Float16>.stride),
            shape,
            dtype: .float16
        )
        return valuesArray.asType(.float32)
    }

    private static func dequantizeQ40(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        for index in 0..<32 {
            let packed = raw[rawOffset + 2 + index % 16]
            let quantized = index < 16 ? packed & 15 : packed >> 4
            values[outputOffset + index] = Float16(scale * Float(Int(quantized) - 8))
        }
    }

    private static func dequantizeQ41(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        let minimum = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 2)))
        for index in 0..<32 {
            let packed = raw[rawOffset + 4 + index % 16]
            let quantized = index < 16 ? packed & 15 : packed >> 4
            values[outputOffset + index] = Float16(scale * Float(quantized) + minimum)
        }
    }

    private static func dequantizeQ80(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        for index in 0..<32 {
            let quantized = Int8(bitPattern: raw[rawOffset + 2 + index])
            values[outputOffset + index] = Float16(scale * Float(quantized))
        }
    }

    private static func dequantizeQ1_0(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        for index in 0..<128 {
            let bit = (raw[rawOffset + 2 + index / 8] >> (index % 8)) & 1
            values[outputOffset + index] = Float16(bit == 0 ? -scale : scale)
        }
    }

    private static func dequantizeQ2_0(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        for index in 0..<64 {
            let quantized = (raw[rawOffset + 2 + index / 4] >> ((index % 4) * 2)) & 3
            values[outputOffset + index] = Float16(Float(Int(quantized) - 1) * scale)
        }
    }

    private static func dequantizeQ2K(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        let minimumScale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 2)))
        let scalesOffset = rawOffset + 4
        let quantizedOffset = rawOffset + 20
        var scaleIndex = 0

        for half in 0..<2 {
            let quantizedHalfOffset = quantizedOffset + half * 32
            let outputHalfOffset = outputOffset + half * 128
            var shift = 0
            for _ in 0..<4 {
                let outputChunkOffset = outputHalfOffset + (shift / 2) * 32
                let firstScale = raw[scalesOffset + scaleIndex]
                scaleIndex += 1
                let first = scale * Float(firstScale & 0x0f)
                let firstMinimum = minimumScale * Float(firstScale >> 4)
                for index in 0..<16 {
                    let quantized = (raw[quantizedHalfOffset + index] >> shift) & 3
                    values[outputChunkOffset + index] = Float16(
                        first * Float(quantized) - firstMinimum
                    )
                }

                let secondScale = raw[scalesOffset + scaleIndex]
                scaleIndex += 1
                let second = scale * Float(secondScale & 0x0f)
                let secondMinimum = minimumScale * Float(secondScale >> 4)
                for index in 0..<16 {
                    let quantized = (raw[quantizedHalfOffset + index + 16] >> shift) & 3
                    values[outputChunkOffset + 16 + index] = Float16(
                        second * Float(quantized) - secondMinimum
                    )
                }
                shift += 2
            }
        }
    }

    private static func dequantizeQ3K(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 108)))
        let highMaskOffset = rawOffset
        let quantizedOffset = rawOffset + 32
        let scalesOffset = rawOffset + 96
        var auxiliary = [UInt32](repeating: 0, count: 4)
        for index in 0..<3 {
            auxiliary[index] = UInt32(raw[scalesOffset + index * 4])
                | UInt32(raw[scalesOffset + index * 4 + 1]) << 8
                | UInt32(raw[scalesOffset + index * 4 + 2]) << 16
                | UInt32(raw[scalesOffset + index * 4 + 3]) << 24
        }
        let mask1: UInt32 = 0x03030303
        let mask2: UInt32 = 0x0f0f0f0f
        let temporary = auxiliary[2]
        auxiliary[2] = ((auxiliary[0] >> 4) & mask2) | (((temporary >> 4) & mask1) << 4)
        auxiliary[3] = ((auxiliary[1] >> 4) & mask2) | (((temporary >> 6) & mask1) << 4)
        auxiliary[0] = (auxiliary[0] & mask2) | (((temporary >> 0) & mask1) << 4)
        auxiliary[1] = (auxiliary[1] & mask2) | (((temporary >> 2) & mask1) << 4)

        var scaleIndex = 0
        var highBitMask: UInt8 = 1
        for half in 0..<2 {
            let quantizedHalfOffset = quantizedOffset + half * 32
            let outputHalfOffset = outputOffset + half * 128
            var shift = 0
            for _ in 0..<4 {
                let outputChunkOffset = outputHalfOffset + (shift / 2) * 32
                let firstScale = Int8(bitPattern: UInt8(truncatingIfNeeded: auxiliary[scaleIndex / 4] >> ((scaleIndex % 4) * 8)))
                scaleIndex += 1
                let firstDequantizedScale = scale * (Float(firstScale) - 32)
                for index in 0..<16 {
                    let low = (raw[quantizedHalfOffset + index] >> shift) & 3
                    let high = (raw[highMaskOffset + index] & highBitMask) == 0 ? 4 : 0
                    values[outputChunkOffset + index] = Float16(
                        firstDequantizedScale * Float(Int(low) - high)
                    )
                }

                let secondScale = Int8(bitPattern: UInt8(truncatingIfNeeded: auxiliary[scaleIndex / 4] >> ((scaleIndex % 4) * 8)))
                scaleIndex += 1
                let secondDequantizedScale = scale * (Float(secondScale) - 32)
                for index in 0..<16 {
                    let low = (raw[quantizedHalfOffset + index + 16] >> shift) & 3
                    let high = (raw[highMaskOffset + index + 16] & highBitMask) == 0 ? 4 : 0
                    values[outputChunkOffset + 16 + index] = Float16(
                        secondDequantizedScale * Float(Int(low) - high)
                    )
                }
                shift += 2
                highBitMask <<= 1
            }
        }
    }

    private static func dequantizeQ4K(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let d = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        let dMin = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 2)))
        let scalesOffset = rawOffset + 4
        let quantizedOffset = rawOffset + 16
        var scaleIndex = 0

        for segment in 0..<4 {
            let (scale1, min1) = kScaleAndMin(
                index: scaleIndex,
                raw: raw,
                offset: scalesOffset
            )
            let (scale2, min2) = kScaleAndMin(
                index: scaleIndex + 1,
                raw: raw,
                offset: scalesOffset
            )
            let segmentOffset = quantizedOffset + segment * 32
            let firstOutput = outputOffset + segment * 64

            for index in 0..<32 {
                let quantized = raw[segmentOffset + index]
                values[firstOutput + index] = Float16(
                    d * Float(scale1) * Float(quantized & 0x0f) - dMin * Float(min1)
                )
                values[firstOutput + 32 + index] = Float16(
                    d * Float(scale2) * Float(quantized >> 4) - dMin * Float(min2)
                )
            }
            scaleIndex += 2
        }
    }

    private static func dequantizeQ5K(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let d = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset)))
        let dMin = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 2)))
        let scalesOffset = rawOffset + 4
        let highBitsOffset = rawOffset + 16
        let lowBitsOffset = rawOffset + 48
        var scaleIndex = 0

        for segment in 0..<4 {
            let (scale1, min1) = kScaleAndMin(
                index: scaleIndex,
                raw: raw,
                offset: scalesOffset
            )
            let (scale2, min2) = kScaleAndMin(
                index: scaleIndex + 1,
                raw: raw,
                offset: scalesOffset
            )
            let lowOffset = lowBitsOffset + segment * 32
            let highBit1 = UInt8(1 << (segment * 2))
            let highBit2 = UInt8(2 << (segment * 2))
            let firstOutput = outputOffset + segment * 64

            for index in 0..<32 {
                let high = raw[highBitsOffset + index]
                let q1 = Int(raw[lowOffset + index] & 0x0f)
                    + ((high & highBit1) == 0 ? 0 : 16)
                let q2 = Int(raw[lowOffset + index] >> 4)
                    + ((high & highBit2) == 0 ? 0 : 16)
                values[firstOutput + index] = Float16(
                    d * Float(scale1) * Float(q1) - dMin * Float(min1)
                )
                values[firstOutput + 32 + index] = Float16(
                    d * Float(scale2) * Float(q2) - dMin * Float(min2)
                )
            }
            scaleIndex += 2
        }
    }

    private static func kScaleAndMin(
        index: Int,
        raw: Data,
        offset: Int
    ) -> (UInt8, UInt8) {
        if index < 4 {
            return (raw[offset + index] & 63, raw[offset + index + 4] & 63)
        }
        let scale = (raw[offset + index + 4] & 0x0f)
            | ((raw[offset + index - 4] >> 6) << 4)
        let min = (raw[offset + index + 4] >> 4)
            | ((raw[offset + index] >> 6) << 4)
        return (scale, min)
    }

    private static func dequantizeQ6K(
        _ raw: Data,
        rawOffset: Int,
        values: inout [Float16],
        outputOffset: Int
    ) {
        let lowBitsOffset = rawOffset
        let highBitsOffset = rawOffset + 128
        let scalesOffset = rawOffset + 192
        let scale = Float(Float16(bitPattern: littleEndianUInt16(raw, at: rawOffset + 208)))

        for half in 0..<2 {
            let valueOffset = outputOffset + half * 128
            let lowOffset = lowBitsOffset + half * 64
            let highOffset = highBitsOffset + half * 32
            let scaleOffset = scalesOffset + half * 8
            for index in 0..<32 {
                let high = raw[highOffset + index]
                let q1 = Int((raw[lowOffset + index] & 0x0f)
                    | ((high & 0x03) << 4)) - 32
                let q2 = Int((raw[lowOffset + 32 + index] & 0x0f)
                    | (((high >> 2) & 0x03) << 4)) - 32
                let q3 = Int((raw[lowOffset + index] >> 4)
                    | (((high >> 4) & 0x03) << 4)) - 32
                let q4 = Int((raw[lowOffset + 32 + index] >> 4)
                    | (((high >> 6) & 0x03) << 4)) - 32
                values[valueOffset + index] = Float16(
                    scale * Float(Int8(bitPattern: raw[scaleOffset + index / 16])) * Float(q1)
                )
                values[valueOffset + 32 + index] = Float16(
                    scale * Float(Int8(bitPattern: raw[scaleOffset + 2 + index / 16])) * Float(q2)
                )
                values[valueOffset + 64 + index] = Float16(
                    scale * Float(Int8(bitPattern: raw[scaleOffset + 4 + index / 16])) * Float(q3)
                )
                values[valueOffset + 96 + index] = Float16(
                    scale * Float(Int8(bitPattern: raw[scaleOffset + 6 + index / 16])) * Float(q4)
                )
            }
        }
    }

    private static func insert(
        _ array: MLXArray,
        name: String,
        into weights: inout [String: MLXArray]
    ) throws {
        guard weights[name] == nil else { throw MLXGGUFLoaderError.duplicateWeight(name) }
        weights[name] = array
    }

    private static func directArray(
        from url: URL,
        data: Data,
        tensor: MLXGGUFTensorInfo,
        dataOffset: Int,
        byteCount: Int,
        shape: [Int],
        dtype: DType,
        memoryMapped: Bool
    ) throws -> MLXArray {
        guard tensor.offset <= UInt64(Int.max) else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }
        if memoryMapped {
            let (fileOffset, overflow) = dataOffset.addingReportingOverflow(Int(tensor.offset))
            guard !overflow else {
                throw MLXGGUFLoaderError.invalidTensor(tensor.name)
            }
            return try MemoryMappedTensorArray.load(
                from: url,
                fileOffset: fileOffset,
                byteCount: byteCount,
                shape: shape,
                dtype: dtype
            )
        }
        return MLXArray(
            try dataSlice(
                data,
                tensor: tensor,
                dataOffset: dataOffset,
                byteCount: byteCount
            ),
            shape,
            dtype: dtype
        )
    }

    private static func dataSlice(
        _ data: Data,
        tensor: MLXGGUFTensorInfo,
        dataOffset: Int,
        byteCount: Int
    ) throws -> Data {
        guard dataOffset >= 0,
              dataOffset <= data.count,
              byteCount >= 0,
              tensor.offset <= UInt64(data.count - dataOffset),
              tensor.offset <= UInt64(Int.max) else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }
        let (start, overflow) = dataOffset.addingReportingOverflow(Int(tensor.offset))
        guard !overflow,
              start <= data.count,
              byteCount <= data.count - start else {
            throw MLXGGUFLoaderError.invalidTensor(tensor.name)
        }
        return data.subdata(in: start..<(start + byteCount))
    }

    private static func littleEndianUInt16(_ data: Data, at offset: Int) -> UInt16 {
        UInt16(data[offset]) | UInt16(data[offset + 1]) << 8
    }

    private static func littleEndianUInt32(_ data: Data, at offset: Int) -> UInt32 {
        UInt32(data[offset])
            | UInt32(data[offset + 1]) << 8
            | UInt32(data[offset + 2]) << 16
            | UInt32(data[offset + 3]) << 24
    }

    private static func checkedProduct(_ values: [Int]) throws -> Int {
        var product = 1
        for value in values {
            guard value >= 0 else { throw MLXGGUFLoaderError.invalidSize }
            let result = product.multipliedReportingOverflow(by: value)
            guard !result.overflow else { throw MLXGGUFLoaderError.invalidSize }
            product = result.partialValue
        }
        return product
    }

    private static func checkedByteCount(_ count: Int, elementSize: Int) throws -> Int {
        let result = count.multipliedReportingOverflow(by: elementSize)
        guard !result.overflow, count >= 0, elementSize >= 0 else {
            throw MLXGGUFLoaderError.invalidSize
        }
        return result.partialValue
    }
}

enum MLXGGUFWeightNameNormalizer {
    static func normalize(
        _ weights: [String: MLXArray],
        modelType: String? = nil,
        maximumLayerIndex: Int? = nil
    ) throws -> [String: MLXArray] {
        var normalized = [String: MLXArray]()
        normalized.reserveCapacity(weights.count)
        for (name, array) in weights {
            guard let normalizedName = normalizedName(
                name,
                modelType: modelType,
                maximumLayerIndex: maximumLayerIndex
            ) else { continue }
            var normalizedArray = array
            if normalizedName.hasSuffix("linear_attn.conv1d.weight"), array.ndim == 2 {
                normalizedArray = array.expandedDimensions(axis: -1)
            }
            guard normalized[normalizedName] == nil else {
                throw MLXGGUFLoaderError.duplicateWeight(normalizedName)
            }
            normalized[normalizedName] = normalizedArray
        }
        return normalized
    }

    /// 回傳無法對應到 Hugging Face 參數名稱的 GGUF 權重。
    ///
    /// 通用架構路徑用它判斷這份 GGUF 的權重佈局是否落在標準稠密 Transformer 的
    /// 範圍內；只要有任何一個權重對應不到，就不替它產生內建設定。
    static func unmappedNames(
        _ names: [String],
        modelType: String? = nil
    ) -> [String] {
        names.filter { name in
            guard let normalized = normalizedName(
                name,
                modelType: modelType,
                maximumLayerIndex: nil
            ) else { return false }
            return normalized == name
        }
    }

    static func normalizedName(
        _ name: String,
        modelType: String? = nil,
        maximumLayerIndex: Int? = nil
    ) -> String? {
        guard !name.contains(".nextn.") else { return nil }
        // llama.cpp 會把預先算好的 rope 頻率表寫進 GGUF；MLX 於執行期自行計算，
        // 留著只會在權重比對時被判為多餘參數。
        guard !name.hasPrefix("rope_freqs") else { return nil }
        if let maximumLayerIndex,
           let layerIndex = layerIndex(in: name),
           layerIndex >= maximumLayerIndex {
            return nil
        }
        if name.hasSuffix(".attn_sinks.weight") {
            return normalizeBase(
                String(name.dropLast(".weight".count)),
                modelType: modelType
            )
        }
        let suffixes = [".scales", ".biases", ".weight", ".bias"]
        if let suffix = suffixes.first(where: { name.hasSuffix($0) }) {
            let base = String(name.dropLast(suffix.count))
            let normalizedBase = normalizeBase(base, modelType: modelType)
            if normalizedBase.hasSuffix(".linear_attn.dt_bias") {
                return normalizedBase
            }
            if normalizedBase.hasSuffix(".layer_scalar") {
                return normalizedBase
            }
            return normalizedBase + suffix
        }
        return normalizeBase(name, modelType: modelType)
    }

    private static func layerIndex(in name: String) -> Int? {
        let parts = name.split(separator: ".", omittingEmptySubsequences: true)
        guard parts.count >= 2, parts[0] == "blk" else { return nil }
        return Int(parts[1])
    }

    private static func normalizeBase(_ base: String, modelType: String?) -> String {
        if isGemma4ModelType(modelType) {
            return normalizeGemma4Base(base)
        }
        if isGemma3ModelType(modelType) {
            return normalizeGemma3Base(base)
        }
        if modelType == "apertus" {
            return normalizeApertusBase(base)
        }
        switch base {
        case "token_embd": return "model.embed_tokens"
        case "output_norm": return "model.norm"
        case "output": return "lm_head"
        default: break
        }

        let parts = base.split(separator: ".", omittingEmptySubsequences: true)
        guard parts.count >= 3,
              parts[0] == "blk",
              let layerIndex = Int(parts[1]) else {
            return base
        }
        let tail = parts.dropFirst(2).joined(separator: ".")
        let mappedTail: String
        switch tail {
        case "attn_norm": mappedTail = "self_attn.input_layernorm"
        case "attn_sinks": mappedTail = "self_attn.sinks"
        case "attn_gate": mappedTail = "linear_attn.in_proj_z"
        case "attn_qkv": mappedTail = "linear_attn.in_proj_qkv"
        case "attn_q": mappedTail = "self_attn.q_proj"
        case "attn_k": mappedTail = "self_attn.k_proj"
        case "attn_v": mappedTail = "self_attn.v_proj"
        case "attn_output": mappedTail = "self_attn.o_proj"
        case "attn_q_norm": mappedTail = "self_attn.q_norm"
        case "attn_k_norm": mappedTail = "self_attn.k_norm"
        case "ffn_norm": mappedTail = "post_attention_layernorm"
        case "post_attention_norm": mappedTail = "post_attention_layernorm"
        case "ffn_gate": mappedTail = "mlp.gate_proj"
        case "ffn_down": mappedTail = "mlp.down_proj"
        case "ffn_up": mappedTail = "mlp.up_proj"
        case "ffn_gate_exps": mappedTail = "mlp.experts.gate_proj"
        case "ffn_down_exps": mappedTail = "mlp.experts.down_proj"
        case "ffn_up_exps": mappedTail = "mlp.experts.up_proj"
        case "ffn_gate_inp": mappedTail = "mlp.router"
        case "ssm_a": mappedTail = "linear_attn.A_log"
        case "ssm_alpha": mappedTail = "linear_attn.in_proj_a"
        case "ssm_beta": mappedTail = "linear_attn.in_proj_b"
        case "ssm_conv1d": mappedTail = "linear_attn.conv1d"
        case "ssm_dt": mappedTail = "linear_attn.dt_bias"
        case "ssm_norm": mappedTail = "linear_attn.norm"
        case "ssm_out": mappedTail = "linear_attn.out_proj"
        default: return base
        }
        if mappedTail == "self_attn.input_layernorm" {
            return "model.layers.\(layerIndex).input_layernorm"
        }
        return "model.layers.\(layerIndex).\(mappedTail)"
    }

    private static func isGemma4ModelType(_ modelType: String?) -> Bool {
        guard let modelType else { return false }
        return modelType == "gemma4"
            || modelType == "gemma4_unified"
            || modelType == "gemma4_text"
    }

    private static func isGemma3ModelType(_ modelType: String?) -> Bool {
        guard let modelType else { return false }
        return modelType == "gemma3" || modelType == "gemma3_text"
    }

    /// Gemma 3 在 GGUF 中把 MLP 前後的 norm 分別命名為 `ffn_norm` 與
    /// `post_ffw_norm`；通用 Llama 映射會漏掉後者並把前者放到錯誤位置。
    private static func normalizeGemma3Base(_ base: String) -> String {
        switch base {
        case "token_embd": return "model.embed_tokens"
        case "output_norm": return "model.norm"
        case "output": return "lm_head"
        default: break
        }

        let parts = base.split(separator: ".", omittingEmptySubsequences: true)
        guard parts.count >= 3,
              parts[0] == "blk",
              let layerIndex = Int(parts[1]) else {
            return base
        }
        let tail = parts.dropFirst(2).joined(separator: ".")
        let mappedTail: String
        switch tail {
        case "attn_norm": mappedTail = "input_layernorm"
        case "attn_q": mappedTail = "self_attn.q_proj"
        case "attn_k": mappedTail = "self_attn.k_proj"
        case "attn_v": mappedTail = "self_attn.v_proj"
        case "attn_output": mappedTail = "self_attn.o_proj"
        case "attn_q_norm": mappedTail = "self_attn.q_norm"
        case "attn_k_norm": mappedTail = "self_attn.k_norm"
        case "post_attention_norm": mappedTail = "post_attention_layernorm"
        case "ffn_norm": mappedTail = "pre_feedforward_layernorm"
        case "post_ffw_norm": mappedTail = "post_feedforward_layernorm"
        case "ffn_gate": mappedTail = "mlp.gate_proj"
        case "ffn_down": mappedTail = "mlp.down_proj"
        case "ffn_up": mappedTail = "mlp.up_proj"
        default: return base
        }
        return "model.layers.\(layerIndex).\(mappedTail)"
    }

    /// Apertus 使用 xIELU MLP（沒有 gate）以及獨立的 attention／feedforward norm
    /// 名稱，必須保留這個拓樸，不能套用標準 Llama block 的參數路徑。
    private static func normalizeApertusBase(_ base: String) -> String {
        switch base {
        case "token_embd": return "model.embed_tokens"
        case "output_norm": return "model.norm"
        case "output": return "lm_head"
        default: break
        }

        let parts = base.split(separator: ".", omittingEmptySubsequences: true)
        guard parts.count >= 3,
              parts[0] == "blk",
              let layerIndex = Int(parts[1]) else {
            return base
        }
        let tail = parts.dropFirst(2).joined(separator: ".")
        let mappedTail: String
        switch tail {
        case "attn_norm": mappedTail = "attention_layernorm"
        case "attn_q": mappedTail = "self_attn.q_proj"
        case "attn_k": mappedTail = "self_attn.k_proj"
        case "attn_v": mappedTail = "self_attn.v_proj"
        case "attn_output": mappedTail = "self_attn.o_proj"
        case "attn_q_norm": mappedTail = "self_attn.q_norm"
        case "attn_k_norm": mappedTail = "self_attn.k_norm"
        case "ffn_norm": mappedTail = "feedforward_layernorm"
        case "ffn_up": mappedTail = "mlp.up_proj"
        case "ffn_down": mappedTail = "mlp.down_proj"
        default: return base
        }
        return "model.layers.\(layerIndex).\(mappedTail)"
    }

    /// Gemma 4 的 GGUF 名稱與標準稠密 Transformer 不同，尤其 `ffn_norm` 是
    /// MLP 前正規化，而不是 attention 後正規化；PLE 也有額外的頂層與逐層權重。
    private static func normalizeGemma4Base(_ base: String) -> String {
        switch base {
        case "token_embd": return "model.embed_tokens"
        case "output_norm": return "model.norm"
        case "output": return "lm_head"
        case "per_layer_token_embd": return "model.embed_tokens_per_layer"
        case "per_layer_model_proj": return "model.per_layer_model_projection"
        case "per_layer_proj_norm": return "model.per_layer_projection_norm"
        default: break
        }

        let parts = base.split(separator: ".", omittingEmptySubsequences: true)
        guard parts.count >= 3,
              parts[0] == "blk",
              let layerIndex = Int(parts[1]) else {
            return base
        }
        let tail = parts.dropFirst(2).joined(separator: ".")
        let mappedTail: String
        switch tail {
        case "attn_norm": mappedTail = "input_layernorm"
        case "attn_q": mappedTail = "self_attn.q_proj"
        case "attn_k": mappedTail = "self_attn.k_proj"
        case "attn_v": mappedTail = "self_attn.v_proj"
        case "attn_output": mappedTail = "self_attn.o_proj"
        case "attn_q_norm": mappedTail = "self_attn.q_norm"
        case "attn_k_norm": mappedTail = "self_attn.k_norm"
        case "post_attention_norm": mappedTail = "post_attention_layernorm"
        case "ffn_norm": mappedTail = "pre_feedforward_layernorm"
        case "post_ffw_norm": mappedTail = "post_feedforward_layernorm"
        case "ffn_gate": mappedTail = "mlp.gate_proj"
        case "ffn_down": mappedTail = "mlp.down_proj"
        case "ffn_up": mappedTail = "mlp.up_proj"
        case "inp_gate": mappedTail = "per_layer_input_gate"
        case "proj": mappedTail = "per_layer_projection"
        case "post_norm": mappedTail = "post_per_layer_input_norm"
        case "layer_output_scale": mappedTail = "layer_scalar"
        default: return base
        }
        return "model.layers.\(layerIndex).\(mappedTail)"
    }
}

enum MLXGGUFMultimodalWeightMapper {
    static func map(_ weights: [String: MLXArray]) throws -> [String: MLXArray] {
        var mapped = [String: MLXArray]()
        mapped.reserveCapacity(weights.count)

        for (name, value) in weights {
            if name == "v.patch_embd.weight" || name == "v.patch_embd.weight.1" {
                continue
            }
            let mappedName: String
            switch name {
            case "v.patch_embd.bias":
                mappedName = "vision_tower.patch_embed.proj.bias"
            case "v.position_embd.weight":
                mappedName = "vision_tower.pos_embed.weight"
            case "v.post_ln.weight":
                mappedName = "vision_tower.merger.norm.weight"
            case "v.post_ln.bias":
                mappedName = "vision_tower.merger.norm.bias"
            case "mm.0.weight":
                mappedName = "vision_tower.merger.linear_fc1.weight"
            case "mm.0.bias":
                mappedName = "vision_tower.merger.linear_fc1.bias"
            case "mm.2.weight":
                mappedName = "vision_tower.merger.linear_fc2.weight"
            case "mm.2.bias":
                mappedName = "vision_tower.merger.linear_fc2.bias"
            default:
                mappedName = try mapVisionBlockName(name)
            }
            guard mapped[mappedName] == nil else {
                throw MLXGGUFLoaderError.duplicateWeight(mappedName)
            }
            mapped[mappedName] = value
        }

        guard let firstPatch = weights["v.patch_embd.weight"],
              let secondPatch = weights["v.patch_embd.weight.1"],
              firstPatch.ndim == 4,
              secondPatch.ndim == 4,
              firstPatch.shape == secondPatch.shape else {
            throw MLXGGUFLoaderError.invalidTensor("v.patch_embd.weight")
        }
        let patchKernel = stacked(
            [
                firstPatch.transposed(0, 2, 3, 1),
                secondPatch.transposed(0, 2, 3, 1)
            ],
            axis: 1
        )
        mapped["vision_tower.patch_embed.proj.weight"] = patchKernel
        return mapped
    }

    private static func mapVisionBlockName(_ name: String) throws -> String {
        let components = name.split(separator: ".")
        guard components.count == 5,
              components[0] == "v",
              components[1] == "blk",
              let layerIndex = Int(components[2]) else {
            throw MLXGGUFLoaderError.invalidTensor(name)
        }
        let moduleName: String
        switch components[3] {
        case "attn_out": moduleName = "attn.proj"
        case "attn_qkv": moduleName = "attn.qkv"
        case "ffn_up": moduleName = "mlp.linear_fc1"
        case "ffn_down": moduleName = "mlp.linear_fc2"
        case "ln1": moduleName = "norm1"
        case "ln2": moduleName = "norm2"
        default: throw MLXGGUFLoaderError.invalidTensor(name)
        }
        guard components[4] == "weight" || components[4] == "bias" else {
            throw MLXGGUFLoaderError.invalidTensor(name)
        }
        return "vision_tower.blocks.\(layerIndex).\(moduleName).\(components[4])"
    }
}

private struct MLXGGUFUserInputProcessor: UserInputProcessor {
    let tokenizer: any MLXLMCommon.Tokenizer
    let messageGenerator: any MessageGenerator

    func prepare(input: UserInput) async throws -> LMInput {
        let messages = messageGenerator.generate(from: input)
        do {
            let promptTokens = try tokenizer.applyChatTemplate(
                messages: messages,
                tools: input.tools,
                additionalContext: input.additionalContext
            )
            return LMInput(tokens: MLXArray(promptTokens))
        } catch MLXLMCommon.TokenizerError.missingChatTemplate {
            let prompt = messages
                .compactMap { $0["content"] as? String }
                .joined(separator: "\n\n")
            return LMInput(tokens: MLXArray(tokenizer.encode(text: prompt)))
        }
    }
}

enum MLXGGUFModelLoader {
    struct ConversionInspection: Codable, Sendable {
        let applicable: Bool
        let requiresConversion: Bool
        let cacheHit: Bool
        let estimatedCacheBytes: Int64
        let cacheDirectory: String
        let cacheKey: String
        let model: String

        enum CodingKeys: String, CodingKey {
            case applicable
            case requiresConversion = "requires_conversion"
            case cacheHit = "cache_hit"
            case estimatedCacheBytes = "estimated_cache_bytes"
            case cacheDirectory = "cache_directory"
            case cacheKey = "cache_key"
            case model
        }
    }

    static func inspectConversion(
        from directoryURL: URL,
        weightURL: URL,
        mmprojURL: URL?,
        quantizationGroupSize: Int?,
        quantizationProfile: GGUFQuantizationProfile,
        recurrentPromotion: GGUFRecurrentPromotionPolicy,
        conversionCacheDirectory: String?
    ) throws -> ConversionInspection {
        let configurationURL = directoryURL.appendingPathComponent("config.json")
        let urls = [weightURL] + (mmprojURL.map { [$0] } ?? [])
        let analysis = try MLXGGUFLoader.conversionStorageAnalysis(
            from: urls,
            requestedGroupSize: quantizationGroupSize,
            profile: quantizationProfile,
            recurrentPromotion: recurrentPromotion
        )
        let plan = try MLXGGUFConversionCache.makePlan(
            cacheDirectory: conversionCacheDirectory,
            weightURL: weightURL,
            mmprojURL: mmprojURL,
            configurationURL: configurationURL,
            profile: quantizationProfile,
            groupSize: analysis.strategy.groupSize,
            recurrentPromotion: recurrentPromotion,
            storageSignature: analysis.strategy.storageSignature
        )
        let cacheHit = MLXGGUFConversionCache.contains(plan: plan)
        return ConversionInspection(
            applicable: true,
            requiresConversion: !cacheHit,
            cacheHit: cacheHit,
            estimatedCacheBytes: cacheHit ? 0 : analysis.estimatedBytes,
            cacheDirectory: plan.entryURL.path,
            cacheKey: plan.key,
            model: weightURL.lastPathComponent
        )
    }

    static func loadContainer(
        from directoryURL: URL,
        weightURL: URL,
        quantizationGroupSize: Int? = nil,
        quantizationProfile: GGUFQuantizationProfile = .automatic,
        recurrentPromotion: GGUFRecurrentPromotionPolicy = .disabled,
        conversionCacheDirectory: String? = nil,
        conversionCacheEnabled: Bool = true,
        memoryMapped: Bool = false
    ) async throws -> ModelContainer {
        try await loadContainer(
            from: directoryURL,
            weightURL: weightURL,
            mmprojURL: nil,
            useVLMProcessor: false,
            quantizationGroupSize: quantizationGroupSize,
            quantizationProfile: quantizationProfile,
            recurrentPromotion: recurrentPromotion,
            conversionCacheDirectory: conversionCacheDirectory,
            conversionCacheEnabled: conversionCacheEnabled,
            memoryMapped: memoryMapped
        )
    }

    static func loadVLMContainer(
        from directoryURL: URL,
        weightURL: URL,
        mmprojURL: URL,
        quantizationGroupSize: Int? = nil,
        quantizationProfile: GGUFQuantizationProfile = .automatic,
        recurrentPromotion: GGUFRecurrentPromotionPolicy = .disabled,
        conversionCacheDirectory: String? = nil,
        conversionCacheEnabled: Bool = true,
        memoryMapped: Bool = false
    ) async throws -> ModelContainer {
        try await loadContainer(
            from: directoryURL,
            weightURL: weightURL,
            mmprojURL: mmprojURL,
            useVLMProcessor: true,
            quantizationGroupSize: quantizationGroupSize,
            quantizationProfile: quantizationProfile,
            recurrentPromotion: recurrentPromotion,
            conversionCacheDirectory: conversionCacheDirectory,
            conversionCacheEnabled: conversionCacheEnabled,
            memoryMapped: memoryMapped
        )
    }

    private static func loadContainer(
        from directoryURL: URL,
        weightURL: URL,
        mmprojURL: URL?,
        useVLMProcessor: Bool,
        quantizationGroupSize: Int?,
        quantizationProfile: GGUFQuantizationProfile,
        recurrentPromotion: GGUFRecurrentPromotionPolicy,
        conversionCacheDirectory: String?,
        conversionCacheEnabled: Bool,
        memoryMapped: Bool
    ) async throws -> ModelContainer {
        guard quantizationGroupSize == nil
                || quantizationGroupSize == 32
                || quantizationGroupSize == 64 else {
            throw MLXGGUFLoaderError.invalidTensor("量化群組設定")
        }
        let configurationURL = directoryURL.appendingPathComponent("config.json")
        let missingAssets = MLXGGUFModelSource.missingRuntimeAssetNames(
            in: directoryURL,
            weightURL: weightURL
        )
        guard missingAssets.isEmpty else {
            throw MLXGGUFLoaderError.missingRuntimeAssets(directoryURL, missingAssets)
        }
        let quantizationStrategy = try MLXGGUFLoader.quantizationStrategy(
            from: [weightURL] + (mmprojURL.map { [$0] } ?? []),
            requestedGroupSize: quantizationGroupSize,
            profile: quantizationProfile
        )
        let resolvedGroupSize = quantizationStrategy.groupSize
        fputs("GGUF strategy \(quantizationStrategy.logDescription)\n", stderr)
        let configData: Data
        do {
            configData = try MLXGGUFEmbeddedAssets.configurationData(
                weightURL: weightURL,
                mmprojURL: mmprojURL
            )
        } catch {
            guard FileManager.default.fileExists(atPath: configurationURL.path) else {
                throw error
            }
            do {
                configData = try Data(contentsOf: configurationURL)
            } catch {
                throw MLXGGUFLoaderError.invalidConfiguration(configurationURL)
            }
        }
        let baseConfiguration: BaseConfiguration
        do {
            baseConfiguration = try JSONDecoder.json5().decode(
                BaseConfiguration.self,
                from: configData
            )
        } catch {
            throw MLXGGUFLoaderError.invalidConfiguration(configurationURL)
        }
        let model: LanguageModel
        if mmprojURL != nil || baseConfiguration.modelType == "qwen3_5" {
            model = try await VLMTypeRegistry.shared.createModel(
                configuration: configData,
                modelType: baseConfiguration.modelType
            )
        } else {
            model = try await LLMModelFactory.shared.typeRegistry.createModel(
                configuration: configData,
                modelType: baseConfiguration.modelType
            )
        }
        async let tokenizerTask = MLXGGUFEmbeddedAssets.tokenizer(
            directoryURL: directoryURL,
            weightURL: weightURL
        )

        fputs("TANPOPO_GGUF_CACHE state=checking\n", stderr)
        let cachePlan: MLXGGUFConversionCache.Plan?
        if conversionCacheEnabled {
            do {
                cachePlan = try MLXGGUFConversionCache.makePlan(
                    cacheDirectory: conversionCacheDirectory,
                    weightURL: weightURL,
                    mmprojURL: mmprojURL,
                    configurationURL: configurationURL,
                    profile: quantizationProfile,
                    groupSize: resolvedGroupSize,
                    recurrentPromotion: recurrentPromotion,
                    storageSignature: quantizationStrategy.storageSignature
                )
            } catch {
                cachePlan = nil
                fputs(
                    "TANPOPO_GGUF_CACHE state=unavailable reason=\(singleLine(error))\n",
                    stderr
                )
            }
        } else {
            cachePlan = nil
            fputs("TANPOPO_GGUF_CACHE state=disabled\n", stderr)
        }

        var weights: [String: MLXArray]?
        if let cachePlan {
            do {
                if MLXGGUFConversionCache.contains(plan: cachePlan) {
                    fputs(
                        "TANPOPO_GGUF_CACHE state=loading key=\(cachePlan.key)\n",
                        stderr
                    )
                }
                weights = try MLXGGUFConversionCache.load(
                    plan: cachePlan,
                    memoryMapped: memoryMapped,
                    progress: ggufProgressReporter(phase: "loading_cache")
                )
                if weights != nil {
                    fputs(
                        "TANPOPO_GGUF_CACHE state=hit key=\(cachePlan.key)\n",
                        stderr
                    )
                }
            } catch {
                MLXGGUFConversionCache.invalidate(plan: cachePlan)
                fputs(
                    "TANPOPO_GGUF_CACHE state=invalid key=\(cachePlan.key) "
                        + "reason=\(singleLine(error))\n",
                    stderr
                )
            }
        }

        if weights == nil {
            if let cachePlan {
                fputs(
                    "TANPOPO_GGUF_CACHE state=miss key=\(cachePlan.key)\n",
                    stderr
                )
            } else {
                fputs("TANPOPO_GGUF_CACHE state=miss cache=disabled\n", stderr)
            }
            weights = try convertAndNormalizeWeights(
                weightURL: weightURL,
                mmprojURL: mmprojURL,
                model: model,
                modelType: baseConfiguration.modelType,
                configurationData: configData,
                configurationURL: configurationURL,
                resolvedGroupSize: resolvedGroupSize,
                quantizationProfile: quantizationProfile,
                recurrentPromotion: recurrentPromotion,
                memoryMapped: memoryMapped,
                progress: ggufProgressReporter(phase: "converting")
            )
            if let cachePlan, let convertedWeights = weights {
                fputs(
                    "TANPOPO_GGUF_CACHE state=saving key=\(cachePlan.key)\n",
                    stderr
                )
                do {
                    try MLXGGUFConversionCache.store(
                        weights: convertedWeights,
                        plan: cachePlan,
                        progress: ggufProgressReporter(phase: "saving_cache")
                    )
                    fputs(
                        "TANPOPO_GGUF_CACHE state=stored key=\(cachePlan.key)\n",
                        stderr
                    )
                    // 新轉換權重與後續啟動必須走同一條 FGGUF 解碼／MMap 路徑。
                    // 直接沿用轉換期間的暫存 MLXArray，可能讓首次啟動與快取命中
                    // 得到不同的材料化結果；存檔後立即重載可保證兩者一致。
                    if let reloadedWeights = try MLXGGUFConversionCache.load(
                        plan: cachePlan,
                        memoryMapped: memoryMapped,
                        progress: ggufProgressReporter(phase: "loading_cache")
                    ) {
                        weights = reloadedWeights
                        fputs(
                            "TANPOPO_GGUF_CACHE state=reloaded key=\(cachePlan.key)\n",
                            stderr
                        )
                    } else {
                        throw MLXGGUFLoaderError.invalidTensor("FGGUF 轉換快取")
                    }
                } catch {
                    fputs(
                        "TANPOPO_GGUF_CACHE state=unavailable key=\(cachePlan.key) "
                            + "reason=\(singleLine(error))\n",
                        stderr
                    )
                }
            }
        }
        guard let weights else {
            throw MLXGGUFLoaderError.invalidTensor("GGUF 轉換權重")
        }
        let modelLoadingProgress = ggufProgressReporter(
            phase: "loading",
            unit: "steps"
        )
        modelLoadingProgress(0, 100)
        quantizeGGUFModel(
            model,
            weights: weights,
            groupSize: resolvedGroupSize
        )
        modelLoadingProgress(15, 100)
        let weightContract = MLXGGUFWeightContract.inspect(
            model: model,
            weights: weights
        )
        modelLoadingProgress(25, 100)
        let diagnosticsEnabled = ProcessInfo.processInfo.environment[
            "TANPOPO_GGUF_DIAGNOSTICS"
        ] == "1"
        if diagnosticsEnabled || !weightContract.isCompatible {
            fputs(weightContract.logDescription() + "\n", stderr)
        }
        let parameters = ModuleParameters.unflattened(weights)
        try model.update(parameters: parameters, verify: [.all])
        modelLoadingProgress(50, 100)
        eval(model)
        modelLoadingProgress(85, 100)

        let tokenizer = try await tokenizerTask
        modelLoadingProgress(95, 100)
        let generationConfigURL = directoryURL.appendingPathComponent("generation_config.json")
        let generationConfig = try? JSONDecoder.json5().decode(
            GenerationConfigFile.self,
            from: Data(contentsOf: generationConfigURL)
        )
        var modelConfiguration = ModelConfiguration(
            directory: directoryURL,
            stopStrings: generationConfig?.stopStrings,
            toolCallFormat: ToolCallFormat.infer(
                from: baseConfiguration.modelType,
                configData: configData
            )
        )
        var eosTokenIDs = Set(baseConfiguration.eosTokenIds?.values ?? [])
        if let generationEOS = generationConfig?.eosTokenIds?.values {
            eosTokenIDs = Set(generationEOS)
        }
        modelConfiguration.eosTokenIds = eosTokenIDs
        let messageGenerator = (model as? LLMModel)?.messageGenerator(tokenizer: tokenizer)
            ?? DefaultMessageGenerator()
        let processor: any UserInputProcessor
        if useVLMProcessor {
            processor = try await makeVLMProcessor(
                from: directoryURL,
                tokenizer: tokenizer,
                mmprojURL: mmprojURL
            )
        } else {
            processor = MLXGGUFUserInputProcessor(
                tokenizer: tokenizer,
                messageGenerator: messageGenerator
            )
        }
        modelLoadingProgress(100, 100)
        return ModelContainer(
            context: ModelContext(
                configuration: modelConfiguration,
                model: model,
                processor: processor,
                tokenizer: tokenizer
            )
        )
    }

    private static func convertAndNormalizeWeights(
        weightURL: URL,
        mmprojURL: URL?,
        model: LanguageModel,
        modelType: String,
        configurationData: Data,
        configurationURL: URL,
        resolvedGroupSize: Int,
        quantizationProfile: GGUFQuantizationProfile,
        recurrentPromotion: GGUFRecurrentPromotionPolicy,
        memoryMapped: Bool,
        progress: ((Int64, Int64) -> Void)? = nil
    ) throws -> [String: MLXArray] {
        let mainTensorBytes = try MLXGGUFLoader.storedTensorByteCount(from: weightURL)
        let projectorTensorBytes = try mmprojURL.map {
            try MLXGGUFLoader.storedTensorByteCount(from: $0)
        } ?? 0
        guard mainTensorBytes <= Int64.max - projectorTensorBytes else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let totalTensorBytes = mainTensorBytes + projectorTensorBytes
        if totalTensorBytes > 0 {
            progress?(0, totalTensorBytes)
        }
        var weights = try MLXGGUFLoader.loadWeights(
            from: weightURL,
            targetGroupSize: resolvedGroupSize,
            quantizationProfile: quantizationProfile,
            stateSpaceParameterEncoding: MLXGGUFArchitectureContract.resolve(
                modelType: modelType
            ).stateSpaceParameterEncoding,
            recurrentPromotion: recurrentPromotion,
            memoryMapped: memoryMapped,
            progress: { completed, _ in
                progress?(completed, totalTensorBytes)
            }
        )
        weights = applyArchitectureWeightLayout(
            weights,
            modelType: modelType,
            configurationData: configurationData
        )
        weights = try MLXGGUFWeightNameNormalizer.normalize(
            weights,
            modelType: modelType,
            maximumLayerIndex: mainLayerCount(from: configurationData)
        )
        if let mmprojURL {
            guard model is any VLMModel else {
                throw MLXGGUFLoaderError.invalidConfiguration(configurationURL)
            }
            let projectorWeights = try MLXGGUFLoader.loadWeights(
                from: mmprojURL,
                targetGroupSize: resolvedGroupSize,
                quantizationProfile: quantizationProfile,
                recurrentPromotion: recurrentPromotion,
                memoryMapped: memoryMapped,
                progress: { completed, _ in
                    progress?(mainTensorBytes + completed, totalTensorBytes)
                }
            )
            let mappedProjectorWeights = try MLXGGUFMultimodalWeightMapper.map(projectorWeights)
            for (name, value) in mappedProjectorWeights {
                guard weights[name] == nil else {
                    throw MLXGGUFLoaderError.duplicateWeight(name)
                }
                weights[name] = value
            }
        }
        weights = try addingArchitectureMetadataParameters(
            in: weights,
            modelType: modelType,
            metadata: MLXGGUFLoader.metadata(from: weightURL)
        )
        if totalTensorBytes > 0 {
            progress?(totalTensorBytes, totalTensorBytes)
        }
        let sanitized = model.sanitize(weights: weights)
        // sanitize 可能建立 transpose、log、offset 等 lazy graph。來源可能是 mmap，
        // 必須在原始 weights 仍存活時依穩定順序完成材料化，避免快取寫入來源值。
        for name in sanitized.keys.sorted() {
            sanitized[name]?.eval()
        }
        return sanitized
    }

    private static func ggufProgressReporter(
        phase: String,
        unit: String = "bytes"
    ) -> (Int64, Int64) -> Void {
        var lastPercent = -1
        return { completed, total in
            guard completed >= 0, total > 0 else { return }
            let boundedCompleted = min(completed, total)
            let percent = Int((Double(boundedCompleted) / Double(total) * 100).rounded(.down))
            guard percent != lastPercent || boundedCompleted == total else { return }
            lastPercent = percent
            fputs(
                "TANPOPO_GGUF_PROGRESS phase=\(phase) "
                    + "completed=\(boundedCompleted) total=\(total) unit=\(unit)\n",
                stderr
            )
        }
    }

    private static func singleLine(_ error: Error) -> String {
        error.localizedDescription
            .replacingOccurrences(of: "\n", with: " ")
            .replacingOccurrences(of: "\r", with: " ")
    }

    private static func makeVLMProcessor(
        from directoryURL: URL,
        tokenizer: any MLXLMCommon.Tokenizer,
        mmprojURL: URL?
    ) async throws -> any UserInputProcessor {
        let preprocessorURL = directoryURL.appendingPathComponent("preprocessor_config.json")
        let processorURL = directoryURL.appendingPathComponent("processor_config.json")
        let configurationURL = FileManager.default.fileExists(atPath: preprocessorURL.path)
            ? preprocessorURL
            : processorURL
        let configurationData: Data
        if FileManager.default.fileExists(atPath: configurationURL.path) {
            do {
                configurationData = try Data(contentsOf: configurationURL)
            } catch {
                throw MLXGGUFLoaderError.missingConfiguration(configurationURL)
            }
        } else if let mmprojURL {
            configurationData = try MLXGGUFEmbeddedAssets.processorConfigurationData(mmprojURL: mmprojURL)
        } else {
            throw MLXGGUFLoaderError.missingConfiguration(configurationURL)
        }
        let configuration: BaseProcessorConfiguration
        do {
            configuration = try JSONDecoder.json5().decode(
                BaseProcessorConfiguration.self,
                from: configurationData
            )
        } catch {
            throw MLXGGUFLoaderError.invalidConfiguration(configurationURL)
        }
        return try await VLMProcessorTypeRegistry.shared.createModel(
            configuration: configurationData,
            processorType: configuration.processorClass,
            tokenizer: tokenizer
        )
    }

    private static func mainLayerCount(from configurationData: Data) -> Int? {
        guard let object = try? JSONSerialization.jsonObject(with: configurationData),
              let root = object as? [String: Any] else { return nil }
        if let count = root["num_hidden_layers"] as? Int {
            return count
        }
        guard let textConfig = root["text_config"] as? [String: Any] else { return nil }
        return textConfig["num_hidden_layers"] as? Int
    }

    private static func isQwen35Architecture(_ modelType: String) -> Bool {
        modelType == "qwen3_5" || modelType == "qwen3_5_text"
    }

    /// 集中處理 GGUF converter 與 MLX 模型之間的架構佈局差異。
    ///
    /// 生產載入與數值對照共用同一入口，避免診斷程式重寫一份
    /// 模型特例後，比對到與實際 Runtime 不同的資料。
    static func applyArchitectureWeightLayout(
        _ weights: [String: MLXArray],
        modelType: String,
        configurationData: Data
    ) -> [String: MLXArray] {
        if isQwen35Architecture(modelType),
           let layout = linearAttentionLayout(from: configurationData) {
            return reorderQwen35LinearAttentionWeights(weights, layout: layout)
        }
        if isGemma3Architecture(modelType) {
            // llama.cpp 的 Gemma 3 GGUF 會把 Hugging Face 的 delta-style RMSNorm
            // 權重先加 1；mlx-swift 執行時也會加 1，必須先還原。
            return undoConverterNormOffset(weights)
        }
        return weights
    }

    private struct LinearAttentionLayout {
        let numKeyHeads: Int
        let numValueHeads: Int
        let keyHeadDimension: Int
        let valueHeadDimension: Int

        var valuesPerKeyHead: Int {
            numValueHeads / numKeyHeads
        }

        var keyRows: Int {
            numKeyHeads * keyHeadDimension
        }

        var valueRows: Int {
            numValueHeads * valueHeadDimension
        }
    }

    /// GGUF's Qwen3.5 converter reorders value heads from grouped HF order to
    /// tiled GGML order.  The mlx-swift model uses the original grouped order,
    /// so reverse that permutation before sanitizing the weight names.
    private static func linearAttentionLayout(from configurationData: Data)
        -> LinearAttentionLayout?
    {
        guard let root = try? JSONSerialization.jsonObject(with: configurationData)
                as? [String: Any] else { return nil }
        let values = (root["text_config"] as? [String: Any]) ?? root
        guard let numKeyHeads = values["linear_num_key_heads"] as? Int,
              let numValueHeads = values["linear_num_value_heads"] as? Int,
              let keyHeadDimension = values["linear_key_head_dim"] as? Int,
              let valueHeadDimension = values["linear_value_head_dim"] as? Int,
              numKeyHeads > 0,
              numValueHeads > 0,
              numValueHeads % numKeyHeads == 0,
              keyHeadDimension > 0,
              valueHeadDimension > 0 else { return nil }
        return LinearAttentionLayout(
            numKeyHeads: numKeyHeads,
            numValueHeads: numValueHeads,
            keyHeadDimension: keyHeadDimension,
            valueHeadDimension: valueHeadDimension
        )
    }

    private static func undoConverterNormOffset(
        _ weights: [String: MLXArray]
    ) -> [String: MLXArray] {
        var corrected = weights
        for (name, value) in weights where isConverterShiftedNorm(name) {
            switch value.dtype {
            case .float16, .float32, .bfloat16:
                corrected[name] = value - MLXArray(1, dtype: value.dtype)
            default:
                break
            }
        }
        return corrected
    }

    static func isConverterShiftedNorm(_ name: String) -> Bool {
        name.hasSuffix("norm.weight")
            && !name.hasSuffix("linear_attn.norm.weight")
            && !name.hasSuffix(".ssm_norm.weight")
    }

    private static func isGemma3Architecture(_ modelType: String) -> Bool {
        modelType == "gemma3" || modelType == "gemma3_text"
    }

    /// 少數架構有不屬於 GGUF tensor、但保存在 metadata 的逐層參數。Apertus 的
    /// xIELU 係數就是其中一例；把它們精確還原成模型參數後，其餘權重仍以
    /// `verify: [.all]` 嚴格檢查，不以模型預設值掩蓋缺漏。
    private static func addingArchitectureMetadataParameters(
        in weights: [String: MLXArray],
        modelType: String,
        metadata: [String: MLXGGUFMetadataValue]
    ) throws -> [String: MLXArray] {
        guard modelType == "apertus" else { return weights }
        guard let architecture = metadata["general.architecture"]?.stringValue,
              let layerCount = metadata["\(architecture).block_count"]?.integerValue,
              layerCount > 0 else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let keys = ["alpha_p", "alpha_n", "beta", "eps"]
        var valuesByKey = [String: [Float]]()
        for key in keys {
            guard let values = metadata["xielu.\(key)"]?.arrayValue?.compactMap(\.floatValue),
                  values.count == layerCount else {
                throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                    architecture: architecture,
                    reason: "缺少完整的逐層 xIELU \(key) 參數"
                )
            }
            valuesByKey[key] = values
        }

        var completed = weights
        for layerIndex in 0..<layerCount {
            let prefix = "model.layers.\(layerIndex).mlp.act_fn"
            completed["\(prefix).alpha_p"] = MLXArray(
                converting: [Double(valuesByKey["alpha_p"]![layerIndex])]
            )
            completed["\(prefix).alpha_n"] = MLXArray(
                converting: [Double(valuesByKey["alpha_n"]![layerIndex])]
            )
            completed["\(prefix).beta"] = MLXArray(valuesByKey["beta"]![layerIndex])
            completed["\(prefix).eps"] = MLXArray(valuesByKey["eps"]![layerIndex])
        }
        return completed
    }

    private static func reorderQwen35LinearAttentionWeights(
        _ weights: [String: MLXArray],
        layout: LinearAttentionLayout
    ) -> [String: MLXArray] {
        var reordered = weights
        for (name, value) in weights {
            let baseName: String
            if name.hasSuffix(".scales") {
                baseName = String(name.dropLast(".scales".count)) + ".weight"
            } else if name.hasSuffix(".biases") {
                baseName = String(name.dropLast(".biases".count)) + ".weight"
            } else {
                baseName = name
            }

            let transformed: MLXArray?
            if baseName.hasSuffix(".attn_qkv.weight") {
                transformed = reorderQKV(value, layout: layout)
            } else if baseName.hasSuffix(".attn_gate.weight") {
                transformed = reorderValueRows(
                    value,
                    layout: layout,
                    headDimension: layout.valueHeadDimension
                )
            } else if baseName.hasSuffix(".ssm_alpha.weight")
                        || baseName.hasSuffix(".ssm_beta.weight") {
                transformed = reorderValueRows(
                    value,
                    layout: layout,
                    headDimension: 1
                )
            } else if baseName.hasSuffix(".ssm_a")
                        || baseName.hasSuffix(".ssm_dt.bias") {
                transformed = reorderValueRows(
                    value,
                    layout: layout,
                    headDimension: 1
                )
            } else if baseName.hasSuffix(".ssm_conv1d.weight") {
                transformed = reorderConv1D(value, layout: layout)
            } else if baseName.hasSuffix(".ssm_out.weight") {
                transformed = reorderValueColumns(value, layout: layout)
            } else {
                transformed = nil
            }
            if let transformed {
                reordered[name] = transformed
            }
        }
        return reordered
    }

    private static func reorderQKV(
        _ value: MLXArray,
        layout: LinearAttentionLayout
    ) -> MLXArray {
        guard value.ndim >= 2,
              value.dim(0) == layout.keyRows * 2 + layout.valueRows else { return value }
        // Qwen3.5's fused projection is [q, k, v].  Both q and k use
        // keyRows; only the v rows need to be restored from GGUF's tiled
        // order to the grouped order expected by mlx-swift-lm.
        let keyRows = layout.keyRows
        let keyPart = value[0..<(keyRows * 2), .ellipsis]
        let valuePart = value[(keyRows * 2)..., .ellipsis]
        let reorderedValue = reorderValueRows(
            valuePart,
            layout: layout,
            headDimension: layout.valueHeadDimension
        )
        return concatenated([keyPart, reorderedValue], axis: 0)
    }

    private static func reorderConv1D(
        _ value: MLXArray,
        layout: LinearAttentionLayout
    ) -> MLXArray {
        guard value.ndim >= 2,
              value.dim(0) == layout.keyRows * 2 + layout.valueRows else { return value }
        let valueStart = layout.keyRows * 2
        let prefix = value[0..<valueStart, .ellipsis]
        let valuePart = value[valueStart..., .ellipsis]
        let reorderedValue = reorderValueRows(
            valuePart,
            layout: layout,
            headDimension: layout.valueHeadDimension
        )
        return concatenated([prefix, reorderedValue], axis: 0)
    }

    private static func reorderValueRows(
        _ value: MLXArray,
        layout: LinearAttentionLayout,
        headDimension: Int
    ) -> MLXArray {
        guard value.ndim >= 1,
              headDimension > 0,
              value.dim(0) == layout.numValueHeads * headDimension else { return value }
        return reorderHeadAxis(
            value,
            axis: 0,
            headDimension: headDimension,
            layout: layout
        )
    }

    private static func reorderValueColumns(
        _ value: MLXArray,
        layout: LinearAttentionLayout
    ) -> MLXArray {
        guard value.ndim >= 2 else { return value }
        let dimensionLength = value.dim(-1)
        // 最後一維相對 valueRows 的壓縮倍率：權重是每個 uint32 打包 32/bits 個元素，
        // scales／biases 則是每個量化群組一個值。倍率直接由實際長度反推，
        // 不能沿用全域 group size——同一個模型內各層的 group 可以不同，
        // 用全域值比對會認不出 scales，導致權重被重排而 scales 沒有，
        // 兩者錯位後輸出即為亂碼。
        guard dimensionLength > 0, layout.valueRows % dimensionLength == 0 else {
            return value
        }
        let packingFactor = layout.valueRows / dimensionLength
        guard layout.valueHeadDimension % packingFactor == 0 else { return value }
        return reorderHeadAxis(
            value,
            axis: value.ndim - 1,
            headDimension: layout.valueHeadDimension / packingFactor,
            layout: layout
        )
    }

    private static func reorderHeadAxis(
        _ value: MLXArray,
        axis: Int,
        headDimension: Int,
        layout: LinearAttentionLayout
    ) -> MLXArray {
        let axis = axis >= 0 ? axis : value.ndim + axis
        guard axis >= 0,
              axis < value.ndim,
              value.dim(axis) == layout.numValueHeads * headDimension else {
            return value
        }

        var reshapedShape = value.shape
        reshapedShape.replaceSubrange(
            axis...axis,
            with: [layout.valuesPerKeyHead, layout.numKeyHeads, headDimension]
        )
        let rank = reshapedShape.count
        var permutation = Array(0..<rank)
        permutation[axis] = axis + 1
        permutation[axis + 1] = axis
        let transposed = value.reshaped(reshapedShape).transposed(axes: permutation)
        return transposed.reshaped(value.shape)
    }

    /// 統計每層實際套用的量化設定，用來確認混合 group 是否如預期。
    private final class QuantizationLayerCounters: @unchecked Sendable {
        private let lock = NSLock()
        private var counts: [String: Int] = [:]
        private var visitedPaths: Set<String> = []
        private var skippedPaths: [String] = []

        func record(group: Int, bits: Int) {
            lock.lock()
            counts["group\(group)/\(bits)bit", default: 0] += 1
            lock.unlock()
        }

        func visit(_ path: String) {
            lock.lock()
            visitedPaths.insert(path)
            lock.unlock()
        }

        func skip(_ path: String, reason: String) {
            lock.lock()
            skippedPaths.append("\(path)[\(reason)]")
            lock.unlock()
        }


        func report(quantizedWeightPrefixes: Set<String>) {
            lock.lock()
            let summary = counts.sorted { $0.key < $1.key }
                .map { "\($0.key):\($0.value)" }
                .joined(separator: ",")
            let visited = visitedPaths
            lock.unlock()
            fputs(
                "GGUF quantized layers [\(summary)] total=\(quantizedWeightPrefixes.count)\n",
                stderr
            )
            // 權重帶了 scales 卻沒有對應的量化 module，代表載入路徑與策略不一致，
            // 屬於必須立刻察覺的異常；正常情況不輸出，避免淹沒啟動日誌。
            let unvisited = quantizedWeightPrefixes.subtracting(visited).sorted()
            guard !unvisited.isEmpty else { return }
            fputs(
                "GGUF quantize MISSING \(unvisited.count) 個量化權重沒有套用設定："
                + unvisited.prefix(8).joined(separator: ", ")
                + (unvisited.count > 8 ? " …" : "") + "\n",
                stderr
            )
        }
    }

    private static func quantizeGGUFModel(
        _ model: LanguageModel,
        weights: [String: MLXArray],
        groupSize: Int
    ) {
        let counters = QuantizationLayerCounters()
        let quantizedWeightPrefixes = Set(
            weights.keys.compactMap { key -> String? in
                key.hasSuffix(".scales") ? String(key.dropLast(".scales".count)) : nil
            }
        )
        defer { counters.report(quantizedWeightPrefixes: quantizedWeightPrefixes) }
        quantize(model: model) { path, _ in
            counters.visit(path)
            guard let weight = weights["\(path).weight"] else {
                counters.skip(path, reason: "no-weight")
                return nil
            }
            guard let scales = weights["\(path).scales"], scales.dim(-1) > 0 else {
                counters.skip(path, reason: "no-scales")
                return nil
            }
            if weights["\(path).biases"] == nil,
               scales.dtype == .uint8,
               weight.dim(-1) * 8 == scales.dim(-1) * 32 {
                return (32, 4, .mxfp4)
            }
            // wq 是 uint32，因此 weight.dim(-1) / scales.dim(-1) == group * bits / 32。
            // 無損沿用來源 block 的層固定 group 32／4 bit（比值 4），其餘層沿用
            // 全域 group；在 group 64 下三種比值 4／8／16 互不重疊，可唯一還原。
            guard scales.dim(-1) > 0, weight.dim(-1) % scales.dim(-1) == 0 else {
                counters.skip(path, reason: "shape")
                return nil
            }
            let packedRatio = weight.dim(-1) / scales.dim(-1)
            if packedRatio == 4 {
                counters.record(group: 32, bits: 4)
                return (32, 4, .affine)
            }
            guard groupSize > 0, packedRatio * 32 % groupSize == 0 else { return nil }
            let bits = packedRatio * 32 / groupSize
            guard bits == 4 || bits == 8 else { return nil }
            counters.record(group: groupSize, bits: bits)
            return (groupSize, bits, .affine)
        }
    }
}
