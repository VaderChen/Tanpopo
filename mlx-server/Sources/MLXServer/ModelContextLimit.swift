import CoreFoundation
import Foundation

/// 啟動時解析一次；對外能力資訊與請求驗證共用同一個有效上下文上限。
enum ModelContextLimit {
    static func resolve(
        directory: URL,
        ggufWeightURL: URL?,
        fastGGUFManifestURL: URL?,
        configuredLimit: Int?
    ) -> Int? {
        let modelLimit: Int?
        if let fastGGUFManifestURL {
            // 直接使用該 manifest 的設定資產，不讀同目錄其他模型的 config.json。
            modelLimit = configurationLimit(
                try? MLXGGUFConversionCache.standaloneConfigurationData(
                    manifestURL: fastGGUFManifestURL))
        } else if let ggufWeightURL {
            let metadata = try? MLXGGUFLoader.metadata(from: ggufWeightURL)
            if let architecture = metadata?["general.architecture"]?.stringValue {
                modelLimit = positive(metadata?["\(architecture).context_length"]?.integerValue)
            } else {
                modelLimit = nil
            }
        } else {
            modelLimit = configurationLimit(
                try? Data(contentsOf: directory.appendingPathComponent("config.json")))
        }
        // 不把 max_tokens、滑動視窗或 tokenizer 的無限長度哨兵當作上下文上限。
        // 資料不足時保持未知，不憑模型名稱或固定預設值宣告能力。
        return [positive(configuredLimit), modelLimit].compactMap { $0 }.min()
    }

    private static func configurationLimit(_ data: Data?) -> Int? {
        guard let data,
            let root = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
        else { return nil }
        let text = root["text_config"] as? [String: Any] ?? root
        return positiveInteger(text["max_position_embeddings"])
            ?? positiveInteger(root["max_position_embeddings"])
    }

    private static func positiveInteger(_ value: Any?) -> Int? {
        guard let number = value as? NSNumber,
            CFGetTypeID(number) != CFBooleanGetTypeID(),
            let integer = Int(exactly: number.doubleValue)
        else { return nil }
        return positive(integer)
    }

    private static func positive(_ value: Int?) -> Int? {
        guard let value, value > 0 else { return nil }
        return value
    }
}
