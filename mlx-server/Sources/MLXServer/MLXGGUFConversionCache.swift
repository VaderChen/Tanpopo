import CryptoKit
import Foundation
import MLX
import MLXLMCommon

struct FastGGUFRuntimeAssets: Sendable {
    let configurationData: Data
    let tokenizerData: Data
    let tokenizerConfigurationData: Data
    let processorConfigurationData: Data?
    let generationConfigurationData: Data?
}

/// GGUF 轉換後權重的永久快取。快取鍵只依來源內容特徵與轉換策略建立，
/// 不依模型名稱判斷；相同模型在不同 INT／Group／recurrent 策略下不會共用。
enum MLXGGUFConversionCache {
    /// 快取鍵版本刻意維持 2，讓既有 safetensors 快取可繼續沿用；新寫入格式
    /// 使用 manifest 3 與 `.fgguf`，讀取端同時接受兩種格式。
    static let keySchemaVersion = 2
    static let currentManifestSchemaVersion = 4
    static let legacyFastGGUFManifestSchemaVersion = 3
    static let legacyManifestSchemaVersion = 2
    private static let sampleByteCount = 1_048_576
    private static let maximumShardBytes = 2 * 1_024 * 1_024 * 1_024

    struct Plan: Sendable {
        let rootURL: URL
        let entryURL: URL
        let manifestURL: URL
        let shardPrefix: String
        let key: String
        let sourceNames: [String]
        let sourcePaths: [String]
        let profile: String
        let groupSize: Int
        let recurrentPromotion: String
    }

    private struct SourceIdentity: Codable, Sendable {
        let role: String
        let path: String
        let size: Int64
        let modificationNanoseconds: Int64
        let sampleSHA256: String
    }

    private struct KeyMaterial: Codable, Sendable {
        let schemaVersion: Int
        let runtimeVersion: String
        let sources: [SourceIdentity]
        let profile: String
        let groupSize: Int
        let recurrentPromotion: String
        /// 轉換後各儲存型別的張量數量；策略改變時快取自動失效。
        let storageSignature: String
    }

    private struct Manifest: Codable, Sendable {
        let schemaVersion: Int
        let key: String
        let runtimeVersion: String
        let createdAt: String
        let sourceNames: [String]
        let sourcePaths: [String]?
        let profile: String
        let groupSize: Int
        let recurrentPromotion: String
        let weightCount: Int
        let totalBytes: Int64
        let storedBytes: Int64?
        let shards: [String]
        let configuration: String?
        let tokenizer: String?
        let tokenizerConfiguration: String?
        let processorConfiguration: String?
        let generationConfiguration: String?
    }

    struct StandalonePackage {
        let weights: [String: MLXArray]
        let groupSize: Int
        let profile: String
        let sourceName: String
        let configurationData: Data
        let tokenizerData: Data
        let tokenizerConfigurationData: Data
        let processorConfigurationData: Data?
        let generationConfigurationData: Data?
    }

    enum CacheError: LocalizedError {
        case invalidManifest
        case duplicateWeight(String)
        case weightCountMismatch(expected: Int, actual: Int)

        var errorDescription: String? {
            switch self {
            case .invalidManifest:
                "Fast GGUF 資訊無效。"
            case .duplicateWeight(let name):
                "Fast GGUF 包含重複權重：\(name)。"
            case .weightCountMismatch(let expected, let actual):
                "Fast GGUF 權重數量不符：預期 \(expected)，實際 \(actual)。"
            }
        }
    }

    /// 診斷開關：轉換後不重新載入剛寫出的 FGGUF，直接沿用記憶體中的權重。
    /// 可省下一次完整讀取，代價是首次啟動與快取命中走不同的材料化路徑。
    static let skipReloadAfterStore =
        ProcessInfo.processInfo.environment["TANPOPO_GGUF_SKIP_RELOAD"] == "1"

    static func makePlan(
        cacheDirectory: String?,
        weightURL: URL,
        mmprojURL: URL?,
        configurationURL: URL?,
        profile: GGUFQuantizationProfile,
        groupSize: Int,
        recurrentPromotion: GGUFRecurrentPromotionPolicy,
        storageSignature: String
    ) throws -> Plan {
        var sources = [try sourceIdentity(role: "model", url: weightURL)]
        if let mmprojURL {
            sources.append(try sourceIdentity(role: "mmproj", url: mmprojURL))
        }
        if let configurationURL,
           FileManager.default.fileExists(atPath: configurationURL.path) {
            sources.append(try sourceIdentity(role: "configuration", url: configurationURL))
        }
        let material = KeyMaterial(
            schemaVersion: keySchemaVersion,
            runtimeVersion: ServerConfiguration.version,
            sources: sources,
            profile: profile.rawValue,
            groupSize: groupSize,
            recurrentPromotion: recurrentPromotion.rawValue,
            storageSignature: storageSignature
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        let key = sha256Hex(try encoder.encode(material))
        let rootURL = resolvedCacheDirectory(
            cacheDirectory,
            sourceURL: weightURL
        )
        let shardPrefix = adjacentShardPrefix(
            sourceURL: weightURL,
            key: key
        )
        return Plan(
            rootURL: rootURL,
            entryURL: rootURL,
            manifestURL: rootURL.appendingPathComponent(
                "\(shardPrefix).fgguf.json"
            ),
            shardPrefix: shardPrefix,
            key: key,
            sourceNames: sources.map { URL(fileURLWithPath: $0.path).lastPathComponent },
            sourcePaths: sources.map(\.path),
            profile: profile.rawValue,
            groupSize: groupSize,
            recurrentPromotion: recurrentPromotion.rawValue
        )
    }

    static func load(
        plan: Plan,
        memoryMapped: Bool,
        progress: ((Int64, Int64) -> Void)? = nil
    ) throws -> [String: MLXArray]? {
        guard FileManager.default.fileExists(atPath: plan.manifestURL.path) else { return nil }
        let manifest = try JSONDecoder().decode(
            Manifest.self,
            from: Data(contentsOf: plan.manifestURL)
        )
        guard supportedManifestVersions.contains(manifest.schemaVersion),
              manifest.key == plan.key,
              manifest.runtimeVersion == ServerConfiguration.version,
              !manifest.shards.isEmpty else {
            throw CacheError.invalidManifest
        }

        var weights = [String: MLXArray]()
        weights.reserveCapacity(manifest.weightCount)
        var completedBytes: Int64 = 0
        if manifest.totalBytes > 0 {
            progress?(0, manifest.totalBytes)
        }
        for filename in manifest.shards {
            guard filename == URL(fileURLWithPath: filename).lastPathComponent,
                  validShardSuffix(filename, schemaVersion: manifest.schemaVersion) else {
                throw CacheError.invalidManifest
            }
            let shardURL = plan.rootURL.appendingPathComponent(filename)
            let shardWeights: [String: MLXArray]
            if fastGGUFManifestVersions.contains(manifest.schemaVersion) {
                (shardWeights, _) = try FastGGUFContainer.load(
                    from: shardURL,
                    expectedCacheKey: plan.key,
                    memoryMapped: memoryMapped
                )
            } else if memoryMapped {
                (shardWeights, _) = try MemoryMappedSafetensors.loadArraysAndMetadata(from: shardURL)
            } else {
                shardWeights = try loadArrays(url: shardURL)
            }
            for (name, value) in shardWeights {
                guard weights[name] == nil else { throw CacheError.duplicateWeight(name) }
                weights[name] = value
            }
            completedBytes += shardWeights.values.reduce(Int64(0)) { partial, value in
                partial + Int64(value.nbytes)
            }
            progress?(min(completedBytes, manifest.totalBytes), manifest.totalBytes)
        }
        guard weights.count == manifest.weightCount else {
            throw CacheError.weightCountMismatch(
                expected: manifest.weightCount,
                actual: weights.count
            )
        }
        return weights
    }

    static func contains(plan: Plan) -> Bool {
        guard let data = try? Data(contentsOf: plan.manifestURL),
              let manifest = try? JSONDecoder().decode(Manifest.self, from: data),
              supportedManifestVersions.contains(manifest.schemaVersion),
              manifest.key == plan.key,
              manifest.runtimeVersion == ServerConfiguration.version,
              !manifest.shards.isEmpty else {
            return false
        }
        return manifest.shards.allSatisfy { filename in
            guard filename == URL(fileURLWithPath: filename).lastPathComponent,
                  validShardSuffix(filename, schemaVersion: manifest.schemaVersion) else {
                return false
            }
            let shardURL = plan.rootURL.appendingPathComponent(filename)
            guard let attributes = try? FileManager.default.attributesOfItem(
                atPath: shardURL.path
            ), let size = (attributes[.size] as? NSNumber)?.int64Value else {
                return false
            }
            return size > 0
        }
    }

    static func store(
        weights: [String: MLXArray],
        plan: Plan,
        runtimeAssets: FastGGUFRuntimeAssets,
        progress: ((Int64, Int64) -> Void)? = nil
    ) throws {
        let fileManager = FileManager.default
        try fileManager.createDirectory(
            at: plan.rootURL,
            withIntermediateDirectories: true
        )
        if fileManager.fileExists(atPath: plan.manifestURL.path) {
            return
        }

        let temporaryURL = plan.rootURL.appendingPathComponent(
            ".\(plan.shardPrefix).\(UUID().uuidString).tmp",
            isDirectory: true
        )
        try fileManager.createDirectory(at: temporaryURL, withIntermediateDirectories: false)
        defer { try? fileManager.removeItem(at: temporaryURL) }

        let shards = makeShards(weights)
        let totalBytes = weights.values.reduce(Int64(0)) { partial, value in
            partial + Int64(value.nbytes)
        }
        var completedBytes: Int64 = 0
        if totalBytes > 0 {
            progress?(0, totalBytes)
        }
        let shardNames = shards.indices.map {
            String(
                format: "%@-%05d-of-%05d.fgguf",
                plan.shardPrefix,
                $0 + 1,
                shards.count
            )
        }
        var storedBytes: Int64 = 0
        var compressedTensorCount = 0
        var rawTensorCount = 0
        for (index, shard) in shards.enumerated() {
            eval(Array(shard.values))
            let baseCompletedBytes = completedBytes
            let statistics = try FastGGUFContainer.store(
                arrays: shard,
                metadata: [
                    "format": "mlx",
                    "tanpopo.gguf_cache_key": plan.key
                ],
                cacheKey: plan.key,
                url: temporaryURL.appendingPathComponent(shardNames[index])
            ) { shardCompletedBytes in
                progress?(min(baseCompletedBytes + shardCompletedBytes, totalBytes), totalBytes)
            }
            completedBytes += statistics.rawBytes
            storedBytes += statistics.storedBytes
            compressedTensorCount += statistics.compressedTensorCount
            rawTensorCount += statistics.rawTensorCount
            progress?(min(completedBytes, totalBytes), totalBytes)
        }

        let rawMiB = String(format: "%.1f", Double(totalBytes) / (1_024 * 1_024))
        let storedMiB = String(format: "%.1f", Double(storedBytes) / (1_024 * 1_024))
        fputs(
            "Fast GGUF raw_mib=\(rawMiB) stored_mib=\(storedMiB) compressed=\(compressedTensorCount) raw=\(rawTensorCount)\n",
            stderr
        )

        let assetNames = standaloneAssetNames(for: plan)
        let manifest = Manifest(
            schemaVersion: currentManifestSchemaVersion,
            key: plan.key,
            runtimeVersion: ServerConfiguration.version,
            createdAt: ISO8601DateFormatter().string(from: Date()),
            sourceNames: plan.sourceNames,
            sourcePaths: plan.sourcePaths,
            profile: plan.profile,
            groupSize: plan.groupSize,
            recurrentPromotion: plan.recurrentPromotion,
            weightCount: weights.count,
            totalBytes: totalBytes,
            storedBytes: storedBytes,
            shards: shardNames,
            configuration: assetNames.configuration,
            tokenizer: assetNames.tokenizer,
            tokenizerConfiguration: assetNames.tokenizerConfiguration,
            processorConfiguration: runtimeAssets.processorConfigurationData == nil
                ? nil : assetNames.processorConfiguration,
            generationConfiguration: runtimeAssets.generationConfigurationData == nil
                ? nil : assetNames.generationConfiguration
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        let temporaryManifestURL = temporaryURL.appendingPathComponent(
            plan.manifestURL.lastPathComponent
        )
        try encoder.encode(manifest).write(
            to: temporaryManifestURL,
            options: .atomic
        )
        try writeRuntimeAssets(
            runtimeAssets,
            names: assetNames,
            directory: temporaryURL
        )

        if fileManager.fileExists(atPath: plan.manifestURL.path) {
            return
        }
        do {
            // Shard 先入位、manifest 最後入位；讀取端只承認已完成的
            // manifest，因此中斷寫入不會被當成可用快取。
            for shardName in shardNames {
                let source = temporaryURL.appendingPathComponent(shardName)
                let destination = plan.rootURL.appendingPathComponent(shardName)
                if fileManager.fileExists(atPath: destination.path) {
                    try fileManager.removeItem(at: destination)
                }
                try fileManager.moveItem(at: source, to: destination)
            }
            for assetName in manifestAssetNames(manifest) {
                let source = temporaryURL.appendingPathComponent(assetName)
                let destination = plan.rootURL.appendingPathComponent(assetName)
                if fileManager.fileExists(atPath: destination.path) {
                    try fileManager.removeItem(at: destination)
                }
                try fileManager.moveItem(at: source, to: destination)
            }
            try fileManager.moveItem(at: temporaryManifestURL, to: plan.manifestURL)
        } catch {
            // 另一個程序可能已完成相同快取；只要完整 manifest
            // 已存在即可採用。
            guard fileManager.fileExists(atPath: plan.manifestURL.path) else {
                throw error
            }
        }
    }

    /// 將既有 schema 3 Fast GGUF 補齊為可在來源 GGUF 移除後獨立啟動的套件。
    /// 資產先以原子寫入落地，最後才更新 manifest；中途失敗時原快取仍可照常使用。
    static func ensureStandaloneAssets(
        plan: Plan,
        runtimeAssets: FastGGUFRuntimeAssets
    ) throws {
        guard FileManager.default.fileExists(atPath: plan.manifestURL.path) else { return }
        let data = try Data(contentsOf: plan.manifestURL)
        let manifest = try JSONDecoder().decode(Manifest.self, from: data)
        guard fastGGUFManifestVersions.contains(manifest.schemaVersion),
              manifest.key == plan.key,
              !manifest.shards.isEmpty else {
            throw CacheError.invalidManifest
        }
        if manifest.schemaVersion == currentManifestSchemaVersion,
           manifest.configuration != nil,
           manifest.tokenizer != nil,
           manifest.tokenizerConfiguration != nil,
           let urls = try? standaloneAssetURLs(manifest: manifest, rootURL: plan.rootURL),
           standaloneAssetFilesExist(urls) {
            // 權重可沿用，但合成 config 的解析規則可能已修正；同步小型設定
            // 資產即可，不必讓使用者重建數 GiB 的 Fast GGUF 分片。
            if try Data(contentsOf: urls.configuration) != runtimeAssets.configurationData {
                try runtimeAssets.configurationData.write(
                    to: urls.configuration,
                    options: .atomic
                )
            }
            return
        }

        let names = standaloneAssetNames(for: plan)
        try writeRuntimeAssets(runtimeAssets, names: names, directory: plan.rootURL)
        let upgraded = Manifest(
            schemaVersion: currentManifestSchemaVersion,
            key: manifest.key,
            runtimeVersion: manifest.runtimeVersion,
            createdAt: manifest.createdAt,
            sourceNames: manifest.sourceNames,
            sourcePaths: manifest.sourcePaths,
            profile: manifest.profile,
            groupSize: manifest.groupSize,
            recurrentPromotion: manifest.recurrentPromotion,
            weightCount: manifest.weightCount,
            totalBytes: manifest.totalBytes,
            storedBytes: manifest.storedBytes,
            shards: manifest.shards,
            configuration: names.configuration,
            tokenizer: names.tokenizer,
            tokenizerConfiguration: names.tokenizerConfiguration,
            processorConfiguration: runtimeAssets.processorConfigurationData == nil
                ? nil : names.processorConfiguration,
            generationConfiguration: runtimeAssets.generationConfigurationData == nil
                ? nil : names.generationConfiguration
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        try encoder.encode(upgraded).write(to: plan.manifestURL, options: .atomic)
    }

    /// 來源 GGUF 已不存在時直接讀取 Fast GGUF manifest、執行資產與權重。
    /// schema 3 可使用同目錄的標準 Hugging Face 資產作為相容 fallback；
    /// schema 4 則固定讀取 manifest 指向的獨立資產。
    static func loadStandalone(
        manifestURL: URL,
        memoryMapped: Bool,
        progress: ((Int64, Int64) -> Void)? = nil
    ) throws -> StandalonePackage {
        let manifest = try JSONDecoder().decode(
            Manifest.self,
            from: Data(contentsOf: manifestURL)
        )
        guard fastGGUFManifestVersions.contains(manifest.schemaVersion),
              !manifest.key.isEmpty,
              !manifest.shards.isEmpty,
              manifest.groupSize == 32 || manifest.groupSize == 64 else {
            throw CacheError.invalidManifest
        }
        let rootURL = manifestURL.deletingLastPathComponent()
        let assetURLs = try standaloneAssetURLs(manifest: manifest, rootURL: rootURL)
        let configurationData = try Data(contentsOf: assetURLs.configuration)
        let tokenizerData = try Data(contentsOf: assetURLs.tokenizer)
        var tokenizerConfigurationData = try Data(
            contentsOf: assetURLs.tokenizerConfiguration
        )
        if manifest.schemaVersion == legacyFastGGUFManifestSchemaVersion {
            tokenizerConfigurationData = try tokenizerConfigurationByAddingAdjacentTemplate(
                tokenizerConfigurationData,
                rootURL: rootURL
            )
        }

        var weights = [String: MLXArray]()
        weights.reserveCapacity(manifest.weightCount)
        var completedBytes: Int64 = 0
        if manifest.totalBytes > 0 { progress?(0, manifest.totalBytes) }
        for filename in manifest.shards {
            guard safeAdjacentFilename(filename), filename.hasSuffix(".fgguf") else {
                throw CacheError.invalidManifest
            }
            let (shardWeights, _) = try FastGGUFContainer.load(
                from: rootURL.appendingPathComponent(filename),
                expectedCacheKey: manifest.key,
                memoryMapped: memoryMapped
            )
            for (name, value) in shardWeights {
                guard weights[name] == nil else { throw CacheError.duplicateWeight(name) }
                weights[name] = value
            }
            completedBytes += shardWeights.values.reduce(Int64(0)) {
                $0 + Int64($1.nbytes)
            }
            progress?(min(completedBytes, manifest.totalBytes), manifest.totalBytes)
        }
        guard weights.count == manifest.weightCount else {
            throw CacheError.weightCountMismatch(
                expected: manifest.weightCount,
                actual: weights.count
            )
        }
        return StandalonePackage(
            weights: weights,
            groupSize: manifest.groupSize,
            profile: manifest.profile,
            sourceName: manifest.sourceNames.first ?? manifestURL.lastPathComponent,
            configurationData: configurationData,
            tokenizerData: tokenizerData,
            tokenizerConfigurationData: tokenizerConfigurationData,
            processorConfigurationData: try assetURLs.processor.map { try Data(contentsOf: $0) },
            generationConfigurationData: try assetURLs.generation.map { try Data(contentsOf: $0) }
        )
    }

    static func invalidate(plan: Plan) {
        let fileManager = FileManager.default
        if let data = try? Data(contentsOf: plan.manifestURL),
           let manifest = try? JSONDecoder().decode(Manifest.self, from: data),
           manifest.key == plan.key {
            for filename in manifest.shards where
                filename == URL(fileURLWithPath: filename).lastPathComponent {
                try? fileManager.removeItem(
                    at: plan.rootURL.appendingPathComponent(filename)
                )
            }
            for filename in manifestAssetNames(manifest) where safeAdjacentFilename(filename) {
                try? fileManager.removeItem(
                    at: plan.rootURL.appendingPathComponent(filename)
                )
            }
        }
        try? fileManager.removeItem(at: plan.manifestURL)
    }

    private static func resolvedCacheDirectory(
        _ value: String?,
        sourceURL: URL
    ) -> URL {
        if let value, !value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return URL(fileURLWithPath: NSString(string: value).expandingTildeInPath)
                .standardizedFileURL
        }
        return sourceURL.deletingLastPathComponent().standardizedFileURL
    }

    private static func adjacentShardPrefix(
        sourceURL: URL,
        key: String
    ) -> String {
        let rawStem = sourceURL.deletingPathExtension().lastPathComponent
        let scalarPrefix = String(rawStem.unicodeScalars.prefix(80))
        let stem = scalarPrefix.isEmpty ? "model" : scalarPrefix
        return "\(stem).tanpopo-\(key.prefix(24))"
    }

    private static func sourceIdentity(role: String, url: URL) throws -> SourceIdentity {
        let canonicalURL = url.resolvingSymlinksInPath().standardizedFileURL
        let attributes = try FileManager.default.attributesOfItem(atPath: canonicalURL.path)
        let size = (attributes[.size] as? NSNumber)?.int64Value ?? 0
        let modificationDate = attributes[.modificationDate] as? Date ?? .distantPast
        let modificationNanoseconds = Int64(
            (modificationDate.timeIntervalSince1970 * 1_000_000_000).rounded()
        )

        let handle = try FileHandle(forReadingFrom: canonicalURL)
        defer { try? handle.close() }
        let first = try handle.read(upToCount: sampleByteCount) ?? Data()
        var sample = Data()
        sample.append(first)
        if size > Int64(sampleByteCount) {
            try handle.seek(toOffset: UInt64(max(0, size - Int64(sampleByteCount))))
            sample.append(try handle.read(upToCount: sampleByteCount) ?? Data())
        }
        return SourceIdentity(
            role: role,
            path: canonicalURL.path,
            size: size,
            modificationNanoseconds: modificationNanoseconds,
            sampleSHA256: sha256Hex(sample)
        )
    }

    private static func makeShards(
        _ weights: [String: MLXArray]
    ) -> [[String: MLXArray]] {
        var result = [[String: MLXArray]]()
        var current = [String: MLXArray]()
        var currentBytes = 0
        for name in weights.keys.sorted() {
            guard let value = weights[name] else { continue }
            if !current.isEmpty,
               currentBytes + value.nbytes > maximumShardBytes {
                result.append(current)
                current = [:]
                currentBytes = 0
            }
            current[name] = value
            currentBytes += value.nbytes
        }
        if !current.isEmpty { result.append(current) }
        return result
    }

    private static func sha256Hex(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    private static let supportedManifestVersions: Set<Int> = [
        legacyManifestSchemaVersion,
        legacyFastGGUFManifestSchemaVersion,
        currentManifestSchemaVersion
    ]

    private static let fastGGUFManifestVersions: Set<Int> = [
        legacyFastGGUFManifestSchemaVersion,
        currentManifestSchemaVersion
    ]

    private static func validShardSuffix(_ filename: String, schemaVersion: Int) -> Bool {
        switch schemaVersion {
        case legacyManifestSchemaVersion:
            filename.hasSuffix(".safetensors")
        case legacyFastGGUFManifestSchemaVersion, currentManifestSchemaVersion:
            filename.hasSuffix(".fgguf")
        default:
            false
        }
    }

    private struct StandaloneAssetNames {
        let configuration: String
        let tokenizer: String
        let tokenizerConfiguration: String
        let processorConfiguration: String
        let generationConfiguration: String
    }

    private struct StandaloneAssetURLs {
        let configuration: URL
        let tokenizer: URL
        let tokenizerConfiguration: URL
        let processor: URL?
        let generation: URL?
    }

    private static func standaloneAssetNames(for plan: Plan) -> StandaloneAssetNames {
        StandaloneAssetNames(
            configuration: "\(plan.shardPrefix).config.json",
            tokenizer: "\(plan.shardPrefix).tokenizer.json",
            tokenizerConfiguration: "\(plan.shardPrefix).tokenizer_config.json",
            processorConfiguration: "\(plan.shardPrefix).preprocessor_config.json",
            generationConfiguration: "\(plan.shardPrefix).generation_config.json"
        )
    }

    private static func writeRuntimeAssets(
        _ assets: FastGGUFRuntimeAssets,
        names: StandaloneAssetNames,
        directory: URL
    ) throws {
        try assets.configurationData.write(
            to: directory.appendingPathComponent(names.configuration),
            options: .atomic
        )
        try assets.tokenizerData.write(
            to: directory.appendingPathComponent(names.tokenizer),
            options: .atomic
        )
        try assets.tokenizerConfigurationData.write(
            to: directory.appendingPathComponent(names.tokenizerConfiguration),
            options: .atomic
        )
        if let data = assets.processorConfigurationData {
            try data.write(
                to: directory.appendingPathComponent(names.processorConfiguration),
                options: .atomic
            )
        }
        if let data = assets.generationConfigurationData {
            try data.write(
                to: directory.appendingPathComponent(names.generationConfiguration),
                options: .atomic
            )
        }
    }

    private static func manifestAssetNames(_ manifest: Manifest) -> [String] {
        [
            manifest.configuration,
            manifest.tokenizer,
            manifest.tokenizerConfiguration,
            manifest.processorConfiguration,
            manifest.generationConfiguration
        ].compactMap { $0 }
    }

    private static func standaloneAssetURLs(
        manifest: Manifest,
        rootURL: URL
    ) throws -> StandaloneAssetURLs {
        func required(_ filename: String?) throws -> URL {
            guard let filename, safeAdjacentFilename(filename) else {
                throw CacheError.invalidManifest
            }
            return rootURL.appendingPathComponent(filename)
        }
        func optional(_ filename: String?) throws -> URL? {
            guard let filename else { return nil }
            guard safeAdjacentFilename(filename) else { throw CacheError.invalidManifest }
            return rootURL.appendingPathComponent(filename)
        }

        if manifest.schemaVersion == currentManifestSchemaVersion {
            return StandaloneAssetURLs(
                configuration: try required(manifest.configuration),
                tokenizer: try required(manifest.tokenizer),
                tokenizerConfiguration: try required(manifest.tokenizerConfiguration),
                processor: try optional(manifest.processorConfiguration),
                generation: try optional(manifest.generationConfiguration)
            )
        }
        return StandaloneAssetURLs(
            configuration: rootURL.appendingPathComponent("config.json"),
            tokenizer: rootURL.appendingPathComponent("tokenizer.json"),
            tokenizerConfiguration: rootURL.appendingPathComponent("tokenizer_config.json"),
            processor: firstExistingURL(
                names: ["preprocessor_config.json", "processor_config.json"],
                rootURL: rootURL
            ),
            generation: firstExistingURL(
                names: ["generation_config.json"],
                rootURL: rootURL
            )
        )
    }

    private static func firstExistingURL(names: [String], rootURL: URL) -> URL? {
        names.map(rootURL.appendingPathComponent).first {
            FileManager.default.fileExists(atPath: $0.path)
        }
    }

    private static func standaloneAssetFilesExist(_ urls: StandaloneAssetURLs) -> Bool {
        let required = [urls.configuration, urls.tokenizer, urls.tokenizerConfiguration]
        let optional = [urls.processor, urls.generation].compactMap { $0 }
        return (required + optional).allSatisfy { url in
            guard let attributes = try? FileManager.default.attributesOfItem(atPath: url.path),
                  let size = (attributes[.size] as? NSNumber)?.int64Value else {
                return false
            }
            return size > 0
        }
    }

    private static func tokenizerConfigurationByAddingAdjacentTemplate(
        _ data: Data,
        rootURL: URL
    ) throws -> Data {
        let templateURL = rootURL.appendingPathComponent("chat_template.jinja")
        guard let template = try? String(contentsOf: templateURL, encoding: .utf8),
              !template.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return data
        }
        guard var configuration = try JSONSerialization.jsonObject(with: data)
                as? [String: Any] else {
            throw CacheError.invalidManifest
        }
        configuration["chat_template"] = template
        return try JSONSerialization.data(
            withJSONObject: configuration,
            options: [.sortedKeys]
        )
    }

    private static func safeAdjacentFilename(_ filename: String) -> Bool {
        !filename.isEmpty && filename == URL(fileURLWithPath: filename).lastPathComponent
    }
}
