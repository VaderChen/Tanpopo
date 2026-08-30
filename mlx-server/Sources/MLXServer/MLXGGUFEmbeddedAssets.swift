import Foundation
import Hub
import MLXHuggingFace
import MLXLMCommon
import Tokenizers

enum MLXGGUFEmbeddedAssets {
    /// 專屬處理的架構；與 `configurationData` 的 switch 保持一致。
    private static let dedicatedArchitectures = ["qwen35", "qwen3", "qwen2", "llama"]

    /// 這個 Runtime 能從 GGUF metadata 直接組出設定的架構（正規化後的名稱）。
    static var supportedGGUFArchitectures: [String] {
        (dedicatedArchitectures + genericArchitectures.keys).sorted()
    }

    static func configurationData(
        weightURL: URL,
        mmprojURL: URL?
    ) throws -> Data {
        let metadata = try MLXGGUFLoader.metadata(from: weightURL)
        guard let architecture = metadata["general.architecture"]?.stringValue else {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(weightURL)
        }

        let configuration: [String: Any]
        switch normalizedArchitecture(architecture) {
        case "qwen35":
            let hasOutputWeight = try MLXGGUFLoader.inspect(from: weightURL)
                .tensors
                .contains { $0.name == "output.weight" }
            // 優先沿用模型自己的 tie_word_embeddings 設定；只有 GGUF
            // 沒有可用設定時，才以缺少 output.weight 作為保守 fallback。
            let configuredTieWordEmbeddings = configuredTieWordEmbeddings(
                in: weightURL.deletingLastPathComponent()
            )
            configuration = try qwen35Configuration(
                metadata: metadata,
                projectorMetadata: try mmprojURL.map {
                    try MLXGGUFLoader.metadata(from: $0)
                },
                isVision: mmprojURL != nil,
                configuredTieWordEmbeddings: configuredTieWordEmbeddings,
                hasOutputWeight: hasOutputWeight
            )
        case "qwen3":
            configuration = try standardTextConfiguration(
                metadata: metadata,
                prefix: "qwen3",
                modelType: "qwen3"
            )
        case "qwen2":
            configuration = try standardTextConfiguration(
                metadata: metadata,
                prefix: "qwen2",
                modelType: "qwen2"
            )
        case "llama":
            configuration = try standardTextConfiguration(
                metadata: metadata,
                prefix: "llama",
                modelType: "llama"
            )
        default:
            configuration = try genericTextConfiguration(
                metadata: metadata,
                architecture: architecture,
                weightURL: weightURL
            )
        }

        guard JSONSerialization.isValidJSONObject(configuration) else {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(weightURL)
        }
        do {
            return try JSONSerialization.data(withJSONObject: configuration, options: [.sortedKeys])
        } catch {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(weightURL)
        }
    }

    static func processorConfigurationData(
        mmprojURL: URL
    ) throws -> Data {
        let projectorMetadata = try MLXGGUFLoader.metadata(from: mmprojURL)
        guard projectorMetadata["clip.has_vision_encoder"]?.booleanValue == true else {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(mmprojURL)
        }

        let patchSize = integer("clip.vision.patch_size", in: projectorMetadata) ?? 16
        let mergeSize = integer("clip.vision.spatial_merge_size", in: projectorMetadata) ?? 2
        let processor: [String: Any] = [
            "processor_class": "Qwen3VLProcessor",
            "image_processor_type": "Qwen2VLImageProcessorFast",
            "patch_size": patchSize,
            "temporal_patch_size": 2,
            "merge_size": mergeSize,
            "image_mean": arrayOfFloats("clip.vision.image_mean", in: projectorMetadata)
                ?? [0.5, 0.5, 0.5],
            "image_std": arrayOfFloats("clip.vision.image_std", in: projectorMetadata)
                ?? [0.5, 0.5, 0.5],
            "min_pixels": 4 * 28 * 28,
            "max_pixels": 16_384 * 28 * 28
        ]
        guard JSONSerialization.isValidJSONObject(processor) else {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(mmprojURL)
        }
        do {
            return try JSONSerialization.data(withJSONObject: processor, options: [.sortedKeys])
        } catch {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(mmprojURL)
        }
    }

    static func tokenizer(
        directoryURL: URL,
        weightURL: URL
    ) async throws -> any MLXLMCommon.Tokenizer {
        let metadata = try MLXGGUFLoader.metadata(from: weightURL)
        let tokenizerDataURL = directoryURL.appendingPathComponent("tokenizer.json")
        let tokenizerConfigURL = directoryURL.appendingPathComponent("tokenizer_config.json")

        let embeddedData = try? embeddedTokenizerData(from: metadata)
        let chatTemplate = chatTemplate(in: directoryURL)
        let embeddedConfiguration = (try? embeddedTokenizerConfiguration(from: metadata)).map {
            configurationByAddingChatTemplate($0, chatTemplate: chatTemplate)
        }
        let externalData = try? config(from: Data(contentsOf: tokenizerDataURL))
        let externalConfiguration = (try? config(from: Data(contentsOf: tokenizerConfigURL))).map {
            configurationByAddingChatTemplate($0, chatTemplate: chatTemplate)
        }
        var candidates: [(data: Config, configuration: Config)] = []
        if let externalData, let externalConfiguration {
            candidates.append((externalData, externalConfiguration))
        }
        if let externalData, let embeddedConfiguration {
            candidates.append((externalData, embeddedConfiguration))
        }
        if let embeddedData, let externalConfiguration {
            candidates.append((embeddedData, externalConfiguration))
        }
        if let embeddedData, let embeddedConfiguration {
            candidates.append((embeddedData, embeddedConfiguration))
        }

        for candidate in candidates {
            let tokenizerConfiguration = configurationWithUnknownTokenFallback(
                candidate.configuration,
                tokenizerData: candidate.data
            )
            if let upstream = try? AutoTokenizer.from(
                tokenizerConfig: tokenizerConfiguration,
                tokenizerData: candidate.data,
                strict: false
            ) {
                return #adaptHuggingFaceTokenizer(upstream)
            }
        }

        if FileManager.default.fileExists(atPath: tokenizerDataURL.path), externalData == nil {
            throw MLXGGUFLoaderError.missingTokenizer(tokenizerDataURL)
        }
        if FileManager.default.fileExists(atPath: tokenizerConfigURL.path),
           externalConfiguration == nil {
            throw MLXGGUFLoaderError.missingTokenizer(tokenizerConfigURL)
        }
        throw MLXGGUFLoaderError.embeddedTokenizerUnavailable(weightURL)
    }

    private static func chatTemplate(in directoryURL: URL) -> String? {
        let templateURL = directoryURL.appendingPathComponent("chat_template.jinja")
        guard let template = try? String(contentsOf: templateURL, encoding: .utf8),
              !template.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return nil
        }
        return template
    }

    private static func configurationByAddingChatTemplate(
        _ configuration: Config,
        chatTemplate: String?
    ) -> Config {
        guard let chatTemplate else { return configuration }
        var values = configuration.dictionary(or: [:])
        values[BinaryDistinctString("chat_template")] = Config(chatTemplate)
        return Config(values)
    }

    private static func configurationWithUnknownTokenFallback(
        _ configuration: Config,
        tokenizerData: Config
    ) -> Config {
        let knownTokens = knownTokens(in: tokenizerData)
        guard !knownTokens.isEmpty else { return configuration }

        let configuredUnknownToken = configuration.unkToken.content.string()
            ?? configuration.unkToken.string()
        if let configuredUnknownToken, knownTokens.contains(configuredUnknownToken) {
            return configuration
        }

        let configuredFallbacks = [
            configuration.padToken.content.string() ?? configuration.padToken.string(),
            configuration.eosToken.content.string() ?? configuration.eosToken.string(),
            "<|endoftext|>"
        ].compactMap { $0 }
        let fallbackToken = configuredFallbacks.first(where: knownTokens.contains)
            ?? knownTokens[0]
        var values = configuration.dictionary(or: [:])
        values[BinaryDistinctString("unk_token")] = Config(fallbackToken)
        return Config(values)
    }

    private static func knownTokens(in tokenizerData: Config) -> [String] {
        var tokens = [String]()
        if let vocabulary = tokenizerData.model.vocab.dictionary() {
            tokens.append(contentsOf: vocabulary
                .sorted { ($0.value.integer() ?? .max) < ($1.value.integer() ?? .max) }
                .map { $0.key.string })
        }
        for addedToken in tokenizerData.addedTokens.array(or: []) {
            if let content = addedToken.content.string() {
                tokens.append(content)
            } else if let token = addedToken.string() {
                tokens.append(token)
            }
        }
        var uniqueTokens = [String]()
        var seenTokens = Set<String>()
        for token in tokens where seenTokens.insert(token).inserted {
            uniqueTokens.append(token)
        }
        return uniqueTokens
    }

    private static func qwen35Configuration(
        metadata: [String: MLXGGUFMetadataValue],
        projectorMetadata: [String: MLXGGUFMetadataValue]?,
        isVision: Bool,
        configuredTieWordEmbeddings: Bool?,
        hasOutputWeight: Bool
    ) throws -> [String: Any] {
        var textConfiguration = try qwen35TextConfiguration(metadata: metadata)
        let tieWordEmbeddings = configuredTieWordEmbeddings ?? !hasOutputWeight
        textConfiguration["tie_word_embeddings"] = tieWordEmbeddings
        guard isVision, let projectorMetadata else {
            return [
                "model_type": "qwen3_5_text",
                "architectures": ["Qwen3_5ForCausalLM"],
                "tie_word_embeddings": tieWordEmbeddings,
                "hidden_size": textConfiguration["hidden_size"]!,
                "num_hidden_layers": textConfiguration["num_hidden_layers"]!,
                "intermediate_size": textConfiguration["intermediate_size"]!,
                "num_attention_heads": textConfiguration["num_attention_heads"]!,
                "num_key_value_heads": textConfiguration["num_key_value_heads"]!,
                "head_dim": textConfiguration["head_dim"]!,
                "rms_norm_eps": textConfiguration["rms_norm_eps"]!,
                "vocab_size": textConfiguration["vocab_size"]!,
                "rope_parameters": textConfiguration["rope_parameters"]!,
                "full_attention_interval": textConfiguration["full_attention_interval"]!,
                "linear_num_value_heads": textConfiguration["linear_num_value_heads"]!,
                "linear_num_key_heads": textConfiguration["linear_num_key_heads"]!,
                "linear_key_head_dim": textConfiguration["linear_key_head_dim"]!,
                "linear_value_head_dim": textConfiguration["linear_value_head_dim"]!,
                "linear_conv_kernel_dim": textConfiguration["linear_conv_kernel_dim"]!
            ]
        }

        let visionConfiguration = try qwen35VisionConfiguration(from: projectorMetadata)
        var result: [String: Any] = [
            "model_type": "qwen3_5",
            "architectures": ["Qwen3_5ForConditionalGeneration"],
            "text_config": textConfiguration,
            "vision_config": visionConfiguration,
            "tie_word_embeddings": tieWordEmbeddings
        ]
        for (metadataKey, configurationKey) in [
            ("<|image_pad|>", "image_token_id"),
            ("<|video_pad|>", "video_token_id"),
            ("<|vision_start|>", "vision_start_token_id"),
            ("<|vision_end|>", "vision_end_token_id")
        ] {
            if let tokenID = tokenID(metadataKey, in: metadata) {
                result[configurationKey] = tokenID
            }
        }
        if let eosTokenID = integer("tokenizer.ggml.eos_token_id", in: metadata) {
            result["eos_token_id"] = eosTokenID
        }
        return result
    }

    /// GGUF metadata 通常沒有 tie_word_embeddings；若同目錄提供 Hugging
    /// Face config.json，則以它作為常規設定來源。找不到設定時由呼叫端
    /// 以 GGUF 是否含 output.weight 執行 fallback 判斷。
    private static func configuredTieWordEmbeddings(in directoryURL: URL) -> Bool? {
        let configurationURL = directoryURL.appendingPathComponent("config.json")
        guard let data = try? Data(contentsOf: configurationURL),
              let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return nil }

        if let textConfiguration = root["text_config"] as? [String: Any],
           let value = textConfiguration["tie_word_embeddings"] as? Bool {
            return value
        }
        return root["tie_word_embeddings"] as? Bool
    }

    private static func qwen35TextConfiguration(
        metadata: [String: MLXGGUFMetadataValue]
    ) throws -> [String: Any] {
        guard let hiddenSize = integer("qwen35.embedding_length", in: metadata),
              let blockCount = integer("qwen35.block_count", in: metadata),
              let intermediateSize = integer("qwen35.feed_forward_length", in: metadata),
              let attentionHeads = integer("qwen35.attention.head_count", in: metadata),
              let kvHeads = integer("qwen35.attention.head_count_kv", in: metadata),
              let vocabularySize = metadata["tokenizer.ggml.tokens"]?.arrayValue?.count,
              hiddenSize > 0, blockCount > 0, intermediateSize > 0,
              attentionHeads > 0, kvHeads > 0, vocabularySize > 0 else {
            throw MLXGGUFLoaderError.invalidSize
        }

        let predictedLayers = integer("qwen35.nextn_predict_layers", in: metadata) ?? 0
        guard predictedLayers >= 0, predictedLayers < blockCount else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let hiddenLayers = blockCount - predictedLayers
        guard hiddenLayers > 0 else { throw MLXGGUFLoaderError.invalidSize }
        let headDim = integer("qwen35.attention.key_length", in: metadata)
            ?? hiddenSize / attentionHeads
        let valueHeadDim = integer("qwen35.ssm.state_size", in: metadata) ?? 128
        let valueHeads = max(
            1,
            (integer("qwen35.ssm.inner_size", in: metadata) ?? valueHeadDim) / valueHeadDim
        )
        let keyHeads = integer("qwen35.ssm.group_count", in: metadata) ?? 16
        let ropeSections = arrayOfIntegers("qwen35.rope.dimension_sections", in: metadata)
            ?? [11, 11, 10]
        let ropeTheta = float("qwen35.rope.freq_base", in: metadata) ?? 10_000_000

        return [
            "model_type": "qwen3_5_text",
            "hidden_size": hiddenSize,
            "num_hidden_layers": hiddenLayers,
            "intermediate_size": intermediateSize,
            "num_attention_heads": attentionHeads,
            "num_key_value_heads": kvHeads,
            "linear_num_value_heads": valueHeads,
            "linear_num_key_heads": keyHeads,
            "linear_key_head_dim": valueHeadDim,
            "linear_value_head_dim": valueHeadDim,
            "linear_conv_kernel_dim": integer("qwen35.ssm.conv_kernel", in: metadata) ?? 4,
            "rms_norm_eps": float("qwen35.attention.layer_norm_rms_epsilon", in: metadata)
                ?? 1e-6,
            "vocab_size": vocabularySize,
            "head_dim": headDim,
            "max_position_embeddings": integer("qwen35.context_length", in: metadata) ?? 131_072,
            "partial_rotary_factor": 0.25,
            "full_attention_interval": integer("qwen35.full_attention_interval", in: metadata) ?? 4,
            "tie_word_embeddings": false,
            "attention_bias": false,
            "rope_parameters": [
                "type": "default",
                "mrope_section": ropeSections,
                "rope_theta": ropeTheta,
                "partial_rotary_factor": 0.25
            ]
        ]
    }

    private static func qwen35VisionConfiguration(
        from metadata: [String: MLXGGUFMetadataValue]
    ) throws -> [String: Any] {
        guard let depth = integer("clip.vision.block_count", in: metadata),
              let hiddenSize = integer("clip.vision.embedding_length", in: metadata),
              let intermediateSize = integer("clip.vision.feed_forward_length", in: metadata),
              let numHeads = integer("clip.vision.attention.head_count", in: metadata),
              let outHiddenSize = integer("clip.vision.projection_dim", in: metadata),
              depth > 0, hiddenSize > 0, intermediateSize > 0, numHeads > 0,
              outHiddenSize > 0 else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let imageSize = integer("clip.vision.image_size", in: metadata) ?? 768
        let patchSize = integer("clip.vision.patch_size", in: metadata) ?? 16
        let numPositionEmbeddings = max(1, (imageSize / patchSize) * (imageSize / patchSize))
        return [
            "model_type": "qwen3_5",
            "depth": depth,
            "hidden_size": hiddenSize,
            "intermediate_size": intermediateSize,
            "out_hidden_size": outHiddenSize,
            "num_heads": numHeads,
            "patch_size": patchSize,
            "spatial_merge_size": integer("clip.vision.spatial_merge_size", in: metadata) ?? 2,
            "temporal_patch_size": 2,
            "num_position_embeddings": numPositionEmbeddings,
            "in_channels": 3,
            "hidden_act": metadata["clip.use_gelu"]?.booleanValue == true
                ? "gelu_pytorch_tanh" : "gelu",
            "deepstack_visual_indexes": []
        ]
    }

    /// GGUF `general.architecture` → Hugging Face `model_type`。
    ///
    /// 只收錄同時滿足兩個條件的架構：`LLMTypeRegistry.shared` 有對應實作，且該
    /// 實作的 `@ModuleInfo` 參數名稱就是標準的 llama 命名（`self_attn.{q,k,v,o}_proj`、
    /// `mlp.{gate,up,down}_proj`、`input_layernorm`、`post_attention_layernorm`）。
    ///
    /// 每一筆都逐一比對過模型實作。命名不同的一律不列入，例如 InternLM2 用
    /// `wqkv`／`w1`、Phi3 與 GLM4 用融合的 `qkv_proj`／`gate_up_proj`、Olmo2 與
    /// Exaone4 是 norm-after 結構、Apertus 用 `attention_layernorm` 且 xIELU 參數
    /// 存在 metadata 而非權重裡——這些都需要各自的對應處理。
    ///
    /// 每一筆都以最小合成 GGUF 實測過：設定能被對應的 `Configuration` 解碼，
    /// 權重也能通過 `verify: [.all]`。設定需要額外欄位的架構不列入——例如 Cohere
    /// 的 `logit_scale`、Granite 的各種 multiplier、Ernie 4.5 的專屬欄位，標準欄位
    /// 不足以組出可解碼的設定。
    ///
    /// `ModelTypeRegistry` 是 actor，無法在同步的設定產生路徑上查詢，所以這裡以
    /// 明確表列取代動態查詢；新增架構時要同時確認註冊表裡有對應的實作。
    private static let genericArchitectures: [String: String] = [
        "gemma": "gemma",
        "mimo": "mimo",
        "minicpm": "minicpm",
        "mistral": "mistral",
        "smollm3": "smollm3"
    ]

    /// 為沒有專屬處理的架構產生標準稠密 Transformer 設定。
    ///
    /// 四道門檻缺一不可：目錄裡沒有使用者自備的 `config.json`（有的話一律以它為
    /// 準）、架構在 `genericArchitectures` 表內、設定欄位都是純量（Gemma 4 的逐層
    /// `feed_forward_length` 陣列就是在這裡被擋下），以及所有 GGUF 權重都能對應
    /// 到標準的 Hugging Face 參數名稱。
    ///
    /// 就算前四關都過，權重名稱若與模型實際期待的不同，載入時的
    /// `model.update(parameters:verify:)` 仍會直接報錯，不會靜默載出錯誤的模型。
    private static func genericTextConfiguration(
        metadata: [String: MLXGGUFMetadataValue],
        architecture: String,
        weightURL: URL
    ) throws -> [String: Any] {
        let directoryURL = weightURL.deletingLastPathComponent()
        let configurationURL = directoryURL.appendingPathComponent("config.json")
        guard !FileManager.default.fileExists(atPath: configurationURL.path),
              let modelType = genericArchitectures[normalizedArchitecture(architecture)],
              metadata["\(architecture).feed_forward_length"]?.integerValue != nil else {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(weightURL)
        }
        let tensorNames = try MLXGGUFLoader.inspect(from: weightURL).tensors.map(\.name)
        guard MLXGGUFWeightNameNormalizer.unmappedNames(tensorNames).isEmpty else {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(weightURL)
        }
        var configuration = try standardTextConfiguration(
            metadata: metadata,
            prefix: architecture,
            modelType: modelType
        )
        configuration["tie_word_embeddings"] = !tensorNames.contains("output.weight")
        return configuration
    }

    private static func standardTextConfiguration(
        metadata: [String: MLXGGUFMetadataValue],
        prefix: String,
        modelType: String
    ) throws -> [String: Any] {
        guard let hiddenSize = integer("\(prefix).embedding_length", in: metadata),
              let hiddenLayers = integer("\(prefix).block_count", in: metadata),
              let intermediateSize = integer("\(prefix).feed_forward_length", in: metadata),
              let attentionHeads = integer("\(prefix).attention.head_count", in: metadata),
              let vocabularySize = metadata["tokenizer.ggml.tokens"]?.arrayValue?.count,
              hiddenSize > 0, hiddenLayers > 0, intermediateSize > 0, attentionHeads > 0,
              vocabularySize > 0 else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let kvHeads = integer("\(prefix).attention.head_count_kv", in: metadata) ?? attentionHeads
        let headDim = integer("\(prefix).attention.key_length", in: metadata)
            ?? hiddenSize / attentionHeads
        return [
            "model_type": modelType,
            "hidden_size": hiddenSize,
            "num_hidden_layers": hiddenLayers,
            "intermediate_size": intermediateSize,
            "num_attention_heads": attentionHeads,
            "num_key_value_heads": kvHeads,
            "head_dim": headDim,
            "rms_norm_eps": float("\(prefix).attention.layer_norm_rms_epsilon", in: metadata)
                ?? 1e-6,
            "vocab_size": vocabularySize,
            "max_position_embeddings": integer("\(prefix).context_length", in: metadata) ?? 32_768,
            "rope_theta": float("\(prefix).rope.freq_base", in: metadata) ?? 1_000_000,
            "tie_word_embeddings": false
        ]
    }

    private static func embeddedTokenizerData(
        from metadata: [String: MLXGGUFMetadataValue]
    ) throws -> Config {
        guard let tokens = metadata["tokenizer.ggml.tokens"]?.arrayValue?.compactMap(\.stringValue),
              let merges = metadata["tokenizer.ggml.merges"]?.arrayValue?.compactMap(\.stringValue),
              !tokens.isEmpty else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let tokenTypes = metadata["tokenizer.ggml.token_type"]?.arrayValue ?? []
        let specialIDs = Set([
            integer("tokenizer.ggml.bos_token_id", in: metadata),
            integer("tokenizer.ggml.eos_token_id", in: metadata),
            integer("tokenizer.ggml.padding_token_id", in: metadata)
        ].compactMap { $0 })
        let vocabulary = tokens.enumerated().reduce(into: [String: Config]()) { result, item in
            guard tokenTypes[safe: item.offset]?.integerValue == 1 else { return }
            result[item.element] = Config(item.offset)
        }
        let addedTokenIDs = tokens.indices.filter { index in
            guard let tokenType = tokenTypes[safe: index]?.integerValue else {
                return specialIDs.contains(index)
            }
            return tokenType == 3 || tokenType == 4 || specialIDs.contains(index)
        }
        let addedTokens = addedTokenIDs.map { index in
            Config([
                "id": Config(index),
                "content": Config(tokens[index]),
                "single_word": Config(false),
                "lstrip": Config(false),
                "rstrip": Config(false),
                "normalized": Config(false),
                "special": Config(isEmbeddedSpecialToken(tokens[index]))
            ])
        }
        let preTokenizer = Config([
            "type": Config("Sequence"),
            "pretokenizers": Config([
                Config([
                    "type": Config("Split"),
                    "pattern": Config([
                        "Regex": Config(
                            "(?i:'s|'t|'re|'ve|'m|'ll|'d)|[^\\r\\n\\p{L}\\p{N}]?[\\p{L}\\p{M}]+|\\p{N}| ?[^\\s\\p{L}\\p{M}\\p{N}]+[\\r\\n]*|\\s*[\\r\\n]+|\\s+(?!\\S)|\\s+"
                        )
                    ]),
                    "behavior": Config("Isolated"),
                    "invert": Config(false)
                ]),
                Config([
                    "type": Config("ByteLevel"),
                    "add_prefix_space": Config(false),
                    "trim_offsets": Config(false),
                    "use_regex": Config(false)
                ])
            ])
        ])
        let byteLevel = Config([
            "type": Config("ByteLevel"),
            "add_prefix_space": Config(false),
            "trim_offsets": Config(false),
            "use_regex": Config(false)
        ])
        return Config([
            "version": Config("1.0"),
            "added_tokens": Config(addedTokens),
            "normalizer": Config(["type": Config("NFC")]),
            "pre_tokenizer": preTokenizer,
            "post_processor": byteLevel,
            "decoder": byteLevel,
            "model": Config([
                "type": Config("BPE"),
                "dropout": Config("nil"),
                "unk_token": Config("nil"),
                "continuing_subword_prefix": Config(""),
                "end_of_word_suffix": Config(""),
                "fuse_unk": Config(false),
                "byte_fallback": Config(false),
                "ignore_merges": Config(false),
                "vocab": Config(vocabulary),
                "merges": Config(merges.map { Config($0) })
            ])
        ])
    }

    private static func isEmbeddedSpecialToken(_ token: String) -> Bool {
        token == "<|endoftext|>"
            || token.hasPrefix("<|im_")
            || token.hasPrefix("<|object_ref_")
            || token.hasPrefix("<|box_")
            || token.hasPrefix("<|quad_")
            || token.hasPrefix("<|vision_")
    }

    private static func embeddedTokenizerConfiguration(
        from metadata: [String: MLXGGUFMetadataValue]
    ) throws -> Config {
        guard let tokens = metadata["tokenizer.ggml.tokens"]?.arrayValue?.compactMap(\.stringValue),
              !tokens.isEmpty else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let tokenString: (Int?) -> Config = { id in
            guard let id, tokens.indices.contains(id) else { return Config("") }
            return Config(tokens[id])
        }
        let specialTokens: [Config] = tokens.enumerated().compactMap { item in
            let index = item.offset
            let token = item.element
            guard index != integer("tokenizer.ggml.eos_token_id", in: metadata),
                  index != integer("tokenizer.ggml.padding_token_id", in: metadata),
                  token.hasPrefix("<|") || token.hasPrefix("<tool") || token.hasPrefix("<think")
            else { return nil }
            return Config(token)
        }
        var values: [String: Config] = [
            "tokenizer_class": Config("Qwen2Tokenizer"),
            "add_bos_token": Config(false),
            "add_eos_token": Config(false),
            "add_prefix_space": Config(false),
            "clean_up_tokenization_spaces": Config(false),
            "eos_token": tokenString(integer("tokenizer.ggml.eos_token_id", in: metadata)),
            "pad_token": tokenString(integer("tokenizer.ggml.padding_token_id", in: metadata)),
            "model_max_length": Config(262_144),
            "additional_special_tokens": Config(specialTokens)
        ]
        if let chatTemplate = metadata["tokenizer.chat_template"]?.stringValue {
            values["chat_template"] = Config(chatTemplate)
        }
        return Config(values)
    }

    private static func config(from data: Data) throws -> Config {
        let object = try JSONSerialization.jsonObject(with: data)
        guard let value = configValue(from: object) else {
            throw MLXGGUFLoaderError.invalidText
        }
        return value
    }

    private static func configValue(from object: Any) -> Config? {
        if object is NSNull { return nil }
        if let value = object as? String { return Config(value) }
        if let value = object as? Bool { return Config(value) }
        if let value = object as? Int { return Config(value) }
        if let value = object as? NSNumber {
            return CFNumberIsFloatType(value) ? Config(value.floatValue) : Config(value.intValue)
        }
        if let values = object as? [Any] {
            return Config(values.compactMap(configValue(from:)))
        }
        if let values = object as? [String: Any] {
            var converted = [String: Config]()
            for (key, value) in values {
                if let convertedValue = configValue(from: value) {
                    converted[key] = convertedValue
                }
            }
            return Config(converted)
        }
        return nil
    }

    private static func normalizedArchitecture(_ architecture: String) -> String {
        architecture.lowercased().replacingOccurrences(of: "_", with: "")
    }

    private static func integer(
        _ key: String,
        in metadata: [String: MLXGGUFMetadataValue]
    ) -> Int? {
        return metadata[key]?.integerValue
    }

    private static func float(
        _ key: String,
        in metadata: [String: MLXGGUFMetadataValue]
    ) -> Float? {
        metadata[key]?.floatValue
    }

    private static func arrayOfIntegers(
        _ key: String,
        in metadata: [String: MLXGGUFMetadataValue]
    ) -> [Int]? {
        metadata[key]?.arrayValue?.compactMap(\.integerValue)
    }

    private static func arrayOfFloats(
        _ key: String,
        in metadata: [String: MLXGGUFMetadataValue]
    ) -> [Float]? {
        metadata[key]?.arrayValue?.compactMap(\.floatValue)
    }

    private static func tokenID(
        _ token: String,
        in metadata: [String: MLXGGUFMetadataValue]
    ) -> Int? {
        guard let tokens = metadata["tokenizer.ggml.tokens"]?.arrayValue else { return nil }
        return tokens.firstIndex { $0.stringValue == token }
    }
}

private extension Array {
    subscript(safe index: Index) -> Element? {
        indices.contains(index) ? self[index] : nil
    }
}
