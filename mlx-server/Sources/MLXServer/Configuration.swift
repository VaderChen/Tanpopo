import Foundation

enum ModelKind: String, Sendable {
    case auto
    case text
    case vision
}

/// fastGGUF 的 recurrent 混合精度策略。判斷依據是 GGUF tensor 的語意角色，
/// 不綁定模型名稱；`controls` 僅將 decay／update 控制投影保留為 BF16。
enum GGUFRecurrentPromotionPolicy: String, Sendable {
    case disabled = "off"
    case controls
    case all
}

struct ServerConfiguration: Sendable {
    static let version = "1.5.0-mlxswiftlm-3.31.4-gguf-dflash2-mmap-fastgguf-cache4"

    var modelPath = ""
    var mmprojPath: String?
    var host = "0.0.0.0"
    var port = 8080
    var modelKind = ModelKind.auto
    var maximumRequestBytes = 32 * 1_024 * 1_024
    var maximumImageBytes = 25 * 1_024 * 1_024
    var maxTokens = 4096
    var maxKVSize: Int?
    var kvBits: Int?
    var kvGroupSize = 64
    // KV Cache 量化以節省長 Context 記憶體為主；先讓短 Context 維持 BF16，
    // 避免在最常見的 decode 路徑過早支付量化／反量化成本。
    var quantizedKVStart = 2048
    var kvScheme: String?
    var prefillStepSize = 512
    var temperature: Float = 0.6
    var topP: Float = 1
    var topK = 0
    var minP: Float = 0
    var repetitionPenalty: Float?
    var thinkingEnabled: Bool?
    var accessControlPath: String?
    var dflashDraftPath: String?
    var dflashBlockSize = 5
    // nil 代表依所有量化 tensor 的實際維度自動選擇：優先 64，不相容才降級 32。
    // 這是資料驅動的全模型策略，不依賴模型名稱或固定樣本。
    var ggufGroupSize: Int?
    var ggufProfile = GGUFQuantizationProfile.automatic
    var ggufRecurrentPromotion = GGUFRecurrentPromotionPolicy.disabled
    var ggufCacheDirectory: String?
    var ggufCacheEnabled = true
    var inspectGGUFCache = false
    // Tanpopo 進階設定控制是否啟用，Profile 則提供記憶體保留目標。
    var memoryMappingEnabled = false
    var mmapReserveGB = 0

    static func parse(_ arguments: [String]) throws -> Self {
        var result = Self()
        var index = 0

        func nextValue(for option: String) throws -> String {
            guard index + 1 < arguments.count else {
                throw ConfigurationError.missingValue(option)
            }
            index += 1
            return arguments[index]
        }

        while index < arguments.count {
            let option = arguments[index]
            switch option {
            case "--model", "-m":
                result.modelPath = try nextValue(for: option)
            case "--mmproj":
                let value = try nextValue(for: option).trimmingCharacters(in: .whitespacesAndNewlines)
                guard !value.isEmpty else {
                    throw ConfigurationError.invalidValue(option, value)
                }
                result.mmprojPath = value
            case "--host":
                result.host = try nextValue(for: option)
            case "--port":
                result.port = try parseInteger(nextValue(for: option), option: option, range: 1...65_535)
            case "--model-type":
                let value = try nextValue(for: option)
                guard let kind = ModelKind(rawValue: value) else {
                    throw ConfigurationError.invalidValue(option, value)
                }
                result.modelKind = kind
            case "--max-tokens":
                result.maxTokens = try parseInteger(nextValue(for: option), option: option, range: 1...1_048_576)
            case "--max-kv-size", "--ctx-size":
                result.maxKVSize = try parseInteger(nextValue(for: option), option: option, range: 1...1_048_576)
            case "--kv-bits":
                let value = try parseInteger(nextValue(for: option), option: option, range: 4...8)
                guard value == 4 || value == 8 else {
                    throw ConfigurationError.invalidValue(option, String(value))
                }
                result.kvBits = value
            case "--kv-group-size":
                result.kvGroupSize = try parseInteger(nextValue(for: option), option: option, range: 16...256)
            case "--quantized-kv-start":
                result.quantizedKVStart = try parseInteger(
                    nextValue(for: option), option: option, range: 0...1_048_576)
            case "--kv-scheme":
                let value = try nextValue(for: option).lowercased()
                guard Self.supportedKVSchemes.contains(value) else {
                    throw ConfigurationError.invalidValue(option, value)
                }
                result.kvScheme = value
            case "--prefill-step-size":
                result.prefillStepSize = try parseInteger(nextValue(for: option), option: option, range: 1...65_536)
            case "--temperature":
                result.temperature = try parseFloat(nextValue(for: option), option: option, range: 0...2)
            case "--top-p":
                result.topP = try parseFloat(nextValue(for: option), option: option, range: 0...1)
            case "--top-k":
                result.topK = try parseInteger(nextValue(for: option), option: option, range: 0...100_000)
            case "--min-p":
                result.minP = try parseFloat(nextValue(for: option), option: option, range: 0...1)
            case "--repetition-penalty":
                result.repetitionPenalty = try parseFloat(nextValue(for: option), option: option, range: 0...10)
            case "--thinking":
                result.thinkingEnabled = true
            case "--no-thinking", "--disable-thinking":
                result.thinkingEnabled = false
            case "--maximum-request-bytes":
                result.maximumRequestBytes = try parseInteger(nextValue(for: option), option: option, range: 1...1_073_741_824)
            case "--maximum-image-bytes":
                result.maximumImageBytes = try parseInteger(nextValue(for: option), option: option, range: 1...1_073_741_824)
            case "--openloader-access-control":
                let value = try nextValue(for: option).trimmingCharacters(in: .whitespacesAndNewlines)
                guard !value.isEmpty else {
                    throw ConfigurationError.invalidValue(option, value)
                }
                result.accessControlPath = value
            case "--dflash-draft":
                let value = try nextValue(for: option).trimmingCharacters(in: .whitespacesAndNewlines)
                guard !value.isEmpty else {
                    throw ConfigurationError.invalidValue(option, value)
                }
                result.dflashDraftPath = value
            case "--dflash-block-size":
                result.dflashBlockSize = try parseInteger(
                    nextValue(for: option), option: option, range: 2...256)
            case "--gguf-group-size":
                let rawValue = try nextValue(for: option).lowercased()
                if rawValue == "auto" {
                    result.ggufGroupSize = nil
                    break
                }
                let value = try parseInteger(rawValue, option: option, range: 32...64)
                guard value == 32 || value == 64 else {
                    throw ConfigurationError.invalidValue(option, rawValue)
                }
                result.ggufGroupSize = value
            case "--gguf-profile":
                let value = try nextValue(for: option).lowercased()
                guard let profile = GGUFQuantizationProfile(rawValue: value) else {
                    throw ConfigurationError.invalidValue(option, value)
                }
                result.ggufProfile = profile
            case "--gguf-recurrent-promotion":
                let value = try nextValue(for: option).lowercased()
                guard let policy = GGUFRecurrentPromotionPolicy(rawValue: value) else {
                    throw ConfigurationError.invalidValue(option, value)
                }
                result.ggufRecurrentPromotion = policy
            case "--gguf-cache-dir":
                let value = try nextValue(for: option)
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                guard !value.isEmpty else {
                    throw ConfigurationError.invalidValue(option, value)
                }
                result.ggufCacheDirectory = value
            case "--no-gguf-cache":
                result.ggufCacheEnabled = false
            case "--inspect-gguf-cache":
                result.inspectGGUFCache = true
            case "--mmap":
                result.memoryMappingEnabled = true
            case "--no-mmap":
                result.memoryMappingEnabled = false
            case "--mmap-reserve-gb":
                let value = try parseInteger(
                    nextValue(for: option), option: option, range: 0...128)
                guard Self.supportedMMapReserveGB.contains(value) else {
                    throw ConfigurationError.invalidValue(option, String(value))
                }
                result.mmapReserveGB = value
            case "--version", "-v":
                print(Self.version)
                Foundation.exit(EXIT_SUCCESS)
            case "--help", "-h":
                print(usage)
                Foundation.exit(EXIT_SUCCESS)
            default:
                throw ConfigurationError.unknownOption(option)
            }
            index += 1
        }

        result.modelPath = NSString(string: result.modelPath).expandingTildeInPath
        if let mmprojPath = result.mmprojPath {
            result.mmprojPath = NSString(string: mmprojPath).expandingTildeInPath
        }
        if let accessControlPath = result.accessControlPath {
            result.accessControlPath = NSString(string: accessControlPath).expandingTildeInPath
        }
        if let dflashDraftPath = result.dflashDraftPath {
            result.dflashDraftPath = NSString(string: dflashDraftPath).expandingTildeInPath
        }
        if let ggufCacheDirectory = result.ggufCacheDirectory {
            result.ggufCacheDirectory = NSString(string: ggufCacheDirectory).expandingTildeInPath
        }
        guard !result.modelPath.isEmpty else {
            throw ConfigurationError.missingModel
        }
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: result.modelPath, isDirectory: &isDirectory) else {
            throw ConfigurationError.invalidModelPath(result.modelPath)
        }
        let isGGUF = !isDirectory.boolValue
            && URL(fileURLWithPath: result.modelPath).pathExtension.lowercased() == "gguf"
        guard isDirectory.boolValue || isGGUF else {
            throw ConfigurationError.invalidModelPath(result.modelPath)
        }
        if let mmprojPath = result.mmprojPath {
            var isMMProjDirectory: ObjCBool = false
            guard isGGUF,
                  FileManager.default.fileExists(atPath: mmprojPath, isDirectory: &isMMProjDirectory),
                  !isMMProjDirectory.boolValue,
                  URL(fileURLWithPath: mmprojPath).pathExtension.lowercased() == "gguf" else {
                throw ConfigurationError.invalidMMProjFile(mmprojPath)
            }
        }
        if let dflashDraftPath = result.dflashDraftPath {
            var isDraftDirectory: ObjCBool = false
            guard FileManager.default.fileExists(
                atPath: dflashDraftPath, isDirectory: &isDraftDirectory),
                isDraftDirectory.boolValue else {
                throw ConfigurationError.invalidDraftDirectory(dflashDraftPath)
            }
        }
        if result.mmapReserveGB > 0, !result.memoryMappingEnabled {
            throw ConfigurationError.mmapReserveRequiresMMap
        }
        return result
    }

    /// 只有 affine4／affine8 會被 `resolveAffineScheme` 認得；其他字串會靜默地
    /// 不做任何量化，因此在啟動時就擋下來。
    private static let supportedKVSchemes: Set<String> = ["affine4", "affine8"]

    private static let supportedMMapReserveGB: Set<Int> = [
        0, 4, 8, 16, 24, 32, 48, 64, 96, 128
    ]

    private static func parseInteger(
        _ value: String,
        option: String,
        range: ClosedRange<Int>
    ) throws -> Int {
        guard let parsed = Int(value), range.contains(parsed) else {
            throw ConfigurationError.invalidValue(option, value)
        }
        return parsed
    }

    private static func parseFloat(
        _ value: String,
        option: String,
        range: ClosedRange<Float>
    ) throws -> Float {
        guard let parsed = Float(value), range.contains(parsed) else {
            throw ConfigurationError.invalidValue(option, value)
        }
        return parsed
    }

    static let usage = """
    用法：mlx-server --model <MLX 模型目錄或 GGUF 檔案> [選項]

      --host <IP>                 監聽位址，預設 0.0.0.0
      --port <Port>               監聽連接埠，預設 8080
      --model-type <類型>          auto、text 或 vision，預設 auto
      --mmproj <GGUF 檔案>         GGUF 多模態視覺投影檔
      --gguf-group-size <auto|32|64>
                                   GGUF 權重量化群組大小，預設 auto（優先 64）
      --gguf-profile <auto|quality|speed>
                                   GGUF 轉換策略，預設 auto
      --gguf-recurrent-promotion <off|controls|all>
                                   recurrent tensor 混合精度策略，預設 off
      --gguf-cache-dir <目錄>      GGUF 轉換後權重的永久快取目錄
      --no-gguf-cache              不讀寫永久快取，僅在本次啟動轉換權重
      --inspect-gguf-cache         只輸出本次轉換與快取預檢 JSON，不啟動服務
      --mmap                        以檔案映射載入支援的模型權重
      --no-mmap                     關閉模型權重檔案映射（預設）
      --mmap-reserve-gb <數值>      記憶體保留目標：0、4、8、16、24、32、48、64、96 或 128 GB
      --max-tokens <數量>          預設最大輸出 Token，預設 4096
      --max-kv-size <數量>         KV Cache 最大 Token 數
      --kv-bits <4|8>             KV Cache 量化位元
      --kv-group-size <數量>       KV Cache 量化群組大小，預設 64
      --kv-scheme <affine4|affine8>
                                   KV Cache 壓縮格式，會覆蓋 --kv-bits 與 --kv-group-size
      --quantized-kv-start <數量>  KV Cache 累積超過此 Token 數才開始量化，預設 2048
      --prefill-step-size <數量>   Prompt 預填批次，預設 512
      --temperature <數值>         預設 0.6
      --top-p <數值>               預設 1.0
      --top-k <數量>               預設 0（停用）
      --min-p <數值>               預設 0（停用）
      --repetition-penalty <數值>  重複懲罰
      --thinking                   強制開啟思考
      --no-thinking                強制關閉思考
      --openloader-access-control <檔案>
                                   Tanpopo 金鑰與 IP 白名單策略快照
      --dflash-draft <模型目錄>    啟用原生 MLX DFlash 1／2（Qwen3／Qwen3.5）
      --dflash-block-size <數量>   DFlash block size，預設 5，且不超過訓練值
    """
}

enum ConfigurationError: LocalizedError {
    case missingModel
    case invalidModelPath(String)
    case invalidMMProjFile(String)
    case invalidDraftDirectory(String)
    case missingValue(String)
    case invalidValue(String, String)
    case mmapReserveRequiresMMap
    case unknownOption(String)

    var errorDescription: String? {
        switch self {
        case .missingModel:
            "必須使用 --model 指定本機 MLX 模型目錄或 GGUF 檔案。"
        case .invalidModelPath(let path):
            "MLX 模型目錄或 GGUF 檔案不存在：\(path)"
        case .invalidMMProjFile(let path):
            "mmproj 必須是可讀取的 GGUF 檔案，且只能搭配 GGUF 主模型：\(path)"
        case .invalidDraftDirectory(let path):
            "DFlash draft 模型目錄不存在：\(path)"
        case .missingValue(let option):
            "啟動參數 \(option) 缺少數值。"
        case .invalidValue(let option, let value):
            "啟動參數 \(option) 的數值無效：\(value)"
        case .mmapReserveRequiresMMap:
            "--mmap-reserve-gb 必須搭配 --mmap。"
        case .unknownOption(let option):
            "不支援的啟動參數：\(option)"
        }
    }
}
