import Foundation

enum ModelKind: String, Sendable {
    case auto
    case text
    case vision
}

struct ServerConfiguration: Sendable {
    static let version = "1.3.3-mlxswiftlm-3.31.4-dflash2"

    var modelPath = ""
    var host = "0.0.0.0"
    var port = 8080
    var modelKind = ModelKind.auto
    var maximumRequestBytes = 32 * 1_024 * 1_024
    var maximumImageBytes = 25 * 1_024 * 1_024
    var maxTokens = 4096
    var maxKVSize: Int?
    var kvBits: Int?
    var kvGroupSize = 64
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
            case "--kv-scheme":
                result.kvScheme = try nextValue(for: option)
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
        if let accessControlPath = result.accessControlPath {
            result.accessControlPath = NSString(string: accessControlPath).expandingTildeInPath
        }
        if let dflashDraftPath = result.dflashDraftPath {
            result.dflashDraftPath = NSString(string: dflashDraftPath).expandingTildeInPath
        }
        guard !result.modelPath.isEmpty else {
            throw ConfigurationError.missingModel
        }
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: result.modelPath, isDirectory: &isDirectory),
              isDirectory.boolValue else {
            throw ConfigurationError.invalidModelDirectory(result.modelPath)
        }
        if let dflashDraftPath = result.dflashDraftPath {
            var isDraftDirectory: ObjCBool = false
            guard FileManager.default.fileExists(
                atPath: dflashDraftPath, isDirectory: &isDraftDirectory),
                isDraftDirectory.boolValue else {
                throw ConfigurationError.invalidDraftDirectory(dflashDraftPath)
            }
        }
        return result
    }

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
    用法：mlx-server --model <模型目錄> [選項]

      --host <IP>                 監聽位址，預設 0.0.0.0
      --port <Port>               監聽連接埠，預設 8080
      --model-type <類型>          auto、text 或 vision，預設 auto
      --max-tokens <數量>          預設最大輸出 Token，預設 4096
      --max-kv-size <數量>         KV Cache 最大 Token 數
      --kv-bits <4|8>             KV Cache 量化位元
      --kv-group-size <數量>       KV Cache 量化群組大小，預設 64
      --kv-scheme <名稱>           KV Cache 壓縮格式，例如 affine4
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
    case invalidModelDirectory(String)
    case invalidDraftDirectory(String)
    case missingValue(String)
    case invalidValue(String, String)
    case unknownOption(String)

    var errorDescription: String? {
        switch self {
        case .missingModel:
            "必須使用 --model 指定本機 MLX 模型目錄。"
        case .invalidModelDirectory(let path):
            "MLX 模型目錄不存在：\(path)"
        case .invalidDraftDirectory(let path):
            "DFlash draft 模型目錄不存在：\(path)"
        case .missingValue(let option):
            "啟動參數 \(option) 缺少數值。"
        case .invalidValue(let option, let value):
            "啟動參數 \(option) 的數值無效：\(value)"
        case .unknownOption(let option):
            "不支援的啟動參數：\(option)"
        }
    }
}
