import Foundation

/// 從模型目錄讀取建議的取樣參數。
///
/// 支援 Hugging Face `generation_config.json`，以及下載包可選的
/// `mangakitchen-model.json`／`generation` 區段。這是一套資料驅動的通用
/// 規則：API 請求與命令列明確參數仍會覆寫模型建議值。
struct ModelGenerationDefaults: Equatable, Sendable {
    var temperature: Float?
    var topP: Float?
    var topK: Int?
    var minP: Float?
    var repetitionPenalty: Float?
    var sources: [String] = []

    var isEmpty: Bool {
        temperature == nil && topP == nil && topK == nil && minP == nil
            && repetitionPenalty == nil
    }

    static func load(from directory: URL) -> Self {
        var result = Self()

        // 相容 Tanpopo／MangaKitchen 模型包；標準 Hugging Face 設定在後面
        // 合併，因此同時存在時以標準 generation_config.json 為準。
        let manifestURL = directory.appendingPathComponent("mangakitchen-model.json")
        if let object = jsonObject(at: manifestURL),
           let generation = object["generation"] as? [String: Any],
           result.merge(generation, overwrite: false) {
            result.sources.append(manifestURL.lastPathComponent)
        }

        let standardURL = directory.appendingPathComponent("generation_config.json")
        if let object = jsonObject(at: standardURL),
           result.merge(object, overwrite: true) {
            result.sources.append(standardURL.lastPathComponent)
        }
        return result
    }

    var logDescription: String {
        var fields = ["model_generation_defaults"]
        if !sources.isEmpty { fields.append("sources=\(sources.joined(separator: ","))") }
        if let temperature { fields.append("temperature=\(temperature)") }
        if let topP { fields.append("top_p=\(topP)") }
        if let topK { fields.append("top_k=\(topK)") }
        if let minP { fields.append("min_p=\(minP)") }
        if let repetitionPenalty { fields.append("repetition_penalty=\(repetitionPenalty)") }
        return fields.joined(separator: " ")
    }

    @discardableResult
    private mutating func merge(_ object: [String: Any], overwrite: Bool) -> Bool {
        var changed = false

        func assignFloat(
            _ value: Float?,
            current: inout Float?,
            range: ClosedRange<Float>
        ) {
            guard let value, value.isFinite, range.contains(value), overwrite || current == nil
            else { return }
            current = value
            changed = true
        }

        func assignInt(
            _ value: Int?,
            current: inout Int?,
            range: ClosedRange<Int>
        ) {
            guard let value, range.contains(value), overwrite || current == nil else { return }
            current = value
            changed = true
        }

        assignFloat(
            Self.float(in: object, keys: ["temperature"]),
            current: &temperature,
            range: 0...2
        )
        assignFloat(
            Self.float(in: object, keys: ["top_p", "topP"]),
            current: &topP,
            range: 0...1
        )
        assignInt(
            Self.integer(in: object, keys: ["top_k", "topK"]),
            current: &topK,
            range: 0...100_000
        )
        assignFloat(
            Self.float(in: object, keys: ["min_p", "minP"]),
            current: &minP,
            range: 0...1
        )
        assignFloat(
            Self.float(in: object, keys: ["repetition_penalty", "repetitionPenalty"]),
            current: &repetitionPenalty,
            range: 0...10
        )
        return changed
    }

    private static func jsonObject(at url: URL) -> [String: Any]? {
        guard let data = try? Data(contentsOf: url),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return nil }
        return object
    }

    private static func float(in object: [String: Any], keys: [String]) -> Float? {
        for key in keys {
            if let number = object[key] as? NSNumber {
                return number.floatValue
            }
        }
        return nil
    }

    private static func integer(in object: [String: Any], keys: [String]) -> Int? {
        for key in keys {
            guard let number = object[key] as? NSNumber else { continue }
            let value = number.doubleValue
            guard value.isFinite, value.rounded() == value else { continue }
            return number.intValue
        }
        return nil
    }
}
