import Foundation
import Hub
import MLXHuggingFace
import MLXLMCommon
import Tokenizers

enum MLXGGUFEmbeddedAssets {
    /// 專屬處理的架構；與 `configurationData` 的 switch 保持一致。
    private static let dedicatedArchitectures = [
        "apertus", "gemma3", "gemma4", "qwen35", "qwen3", "qwen2", "llama"
    ]

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

        var configuration: [String: Any]
        switch normalizedArchitecture(architecture) {
        case "apertus":
            let tensorNames = try MLXGGUFLoader.tensorNames(from: weightURL)
            configuration = try apertusConfiguration(
                metadata: metadata,
                prefix: architecture,
                tensorNames: tensorNames
            )
        case "gemma3":
            let tensorNames = try MLXGGUFLoader.tensorNames(from: weightURL)
            configuration = try gemma3TextConfiguration(
                metadata: metadata,
                prefix: architecture,
                tensorNames: tensorNames
            )
        case "gemma4":
            let tensorNames = try MLXGGUFLoader.tensorNames(from: weightURL)
            configuration = try gemma4TextConfiguration(
                metadata: metadata,
                prefix: architecture,
                tensorNames: tensorNames
            )
        case "qwen35":
            let hasOutputWeight = try MLXGGUFLoader.tensorNames(from: weightURL)
                .contains("output.weight")
            // GGUF metadata 不一定保留 layer_types、gating 與完整 RoPE
            // 語意；同目錄有 Hugging Face config 時以它為語意基準，
            // 再由 Tensor contract 確認形狀一致。
            let configuredTextConfiguration = configuredQwen35TextConfiguration(
                in: weightURL.deletingLastPathComponent()
            )
            let configuredTieWordEmbeddings = configuredTextConfiguration?[
                "tie_word_embeddings"
            ] as? Bool
            configuration = try qwen35Configuration(
                metadata: metadata,
                projectorMetadata: try mmprojURL.map {
                    try MLXGGUFLoader.metadata(from: $0)
                },
                isVision: mmprojURL != nil,
                configuredTextConfiguration: configuredTextConfiguration,
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

        configuration = addingTokenizerTokenIDs(
            to: configuration,
            metadata: metadata
        )

        guard JSONSerialization.isValidJSONObject(configuration) else {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(weightURL)
        }
        do {
            return try JSONSerialization.data(withJSONObject: configuration, options: [.sortedKeys])
        } catch {
            throw MLXGGUFLoaderError.embeddedConfigurationUnavailable(weightURL)
        }
    }

    /// 將 GGUF tokenizer 的特殊 token ID 補入合成設定。
    ///
    /// `BaseConfiguration` 會以 `eos_token_id` 建立生成停止條件；若省略，即使模型
    /// 已生成結束符號，Runtime 仍會把它當成一般文字繼續解碼。其餘 ID 一併保存，
    /// 讓有讀取這些欄位的模型設定能沿用原始 tokenizer 語意。
    static func addingTokenizerTokenIDs(
        to configuration: [String: Any],
        metadata: [String: MLXGGUFMetadataValue]
    ) -> [String: Any] {
        var result = configuration
        let stopTokenIDs = generationStopTokenIDs(
            configuration: configuration,
            metadata: metadata
        )
        if stopTokenIDs.count == 1 {
            result["eos_token_id"] = stopTokenIDs[0]
        } else if !stopTokenIDs.isEmpty {
            result["eos_token_id"] = stopTokenIDs
        }

        for (metadataKey, configurationKey) in [
            ("tokenizer.ggml.bos_token_id", "bos_token_id"),
            ("tokenizer.ggml.padding_token_id", "pad_token_id"),
            ("tokenizer.ggml.unknown_token_id", "unk_token_id")
        ] {
            if let tokenID = integer(metadataKey, in: metadata) {
                result[configurationKey] = tokenID
            }
        }
        return result
    }

    /// GGUF 的 `eos_token_id` 有時只保留基礎 tokenizer 的 `</s>`，但 chat
    /// template 實際以 `<|assistant_end|>`、`<|im_end|>` 或 `<end_of_turn>`
    /// 結束回答。從模板中實際出現的控制 token 推導額外停止 ID，避免模型已結束
    /// 回答卻繼續生成；普通字彙及只屬於 system/user 的結束符號不會被納入。
    static func generationStopTokenIDs(
        configuration: [String: Any],
        metadata: [String: MLXGGUFMetadataValue]
    ) -> [Int] {
        var result = [Int]()
        func append(_ tokenID: Int) {
            guard tokenID >= 0, !result.contains(tokenID) else { return }
            result.append(tokenID)
        }

        if let values = configuration["eos_token_id"] as? [Int] {
            values.forEach(append)
        } else if let value = configuration["eos_token_id"] as? Int {
            append(value)
        }
        if let value = integer("tokenizer.ggml.eos_token_id", in: metadata) {
            append(value)
        }

        guard let template = metadata["tokenizer.chat_template"]?.stringValue,
              let tokens = metadata["tokenizer.ggml.tokens"]?.arrayValue else {
            return result
        }
        let tokenTypes = metadata["tokenizer.ggml.token_type"]?.arrayValue
        let stopMarkers = [
            "assistant_end", "end_assistant", "end_of_turn", "end_of_message",
            "eot_id", "eom_id", "im_end", "end_of_text"
        ]
        for (tokenID, value) in tokens.enumerated() {
            // 一般字彙占大型 tokenizer 的絕大多數，先依 token type 排除，
            // 再比對少量結束標記；只有候選控制 token 才搜尋 chat template。
            // 避免對數十萬個普通 token 逐一執行 template.contains(token)。
            if let tokenTypes,
               tokenTypes.indices.contains(tokenID),
               tokenTypes[tokenID].integerValue == 1 {
                continue
            }
            guard let token = value.stringValue,
                  !token.isEmpty,
                  stopMarkers.contains(where: token.lowercased().contains),
                  template.contains(token) else {
                continue
            }
            append(tokenID)
        }
        return result
    }

    /// 是否認得這份 GGUF 的架構並能嘗試直接從 metadata 建立設定。
    ///
    /// 這裡只判斷架構，不提前吞掉設定合成錯誤。如此一來，已登記架構中的特殊變體
    /// 會在真正載入時回報精確原因，而不會被誤判成缺少 `config.json`。
    static func recognizesEmbeddedConfiguration(at weightURL: URL) -> Bool {
        guard let metadata = try? MLXGGUFLoader.metadata(from: weightURL),
              let architecture = metadata["general.architecture"]?.stringValue else {
            return false
        }
        return supportedGGUFArchitectures.contains(normalizedArchitecture(architecture))
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
        configuredTextConfiguration: [String: Any]?,
        configuredTieWordEmbeddings: Bool?,
        hasOutputWeight: Bool
    ) throws -> [String: Any] {
        var textConfiguration = try qwen35TextConfiguration(metadata: metadata)
        if let configuredTextConfiguration {
            // 外部 config 是模型作者定義的 Runtime 語意；GGUF
            // metadata 仍擔任沒有 config 時的完整 fallback。
            textConfiguration.merge(configuredTextConfiguration) { _, configured in
                configured
            }
        }
        let tieWordEmbeddings = configuredTieWordEmbeddings ?? !hasOutputWeight
        textConfiguration["model_type"] = "qwen3_5_text"
        textConfiguration["tie_word_embeddings"] = tieWordEmbeddings
        guard isVision, let projectorMetadata else {
            textConfiguration["architectures"] = ["Qwen3_5ForCausalLM"]
            return textConfiguration
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

    /// 讀取 Qwen3.5 的作者設定。多模態 config 取 `text_config`；
    /// 純文字 config 則直接使用根層。
    private static func configuredQwen35TextConfiguration(
        in directoryURL: URL
    ) -> [String: Any]? {
        let configurationURL = directoryURL.appendingPathComponent("config.json")
        guard let data = try? Data(contentsOf: configurationURL),
              let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return nil }

        if let textConfiguration = root["text_config"] as? [String: Any] {
            return textConfiguration
        }
        let modelType = root["model_type"] as? String
        return modelType == "qwen3_5" || modelType == "qwen3_5_text" ? root : nil
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
            "mtp_num_hidden_layers": predictedLayers,
            "mtp_use_dedicated_embeddings": false,
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

    private struct Gemma4FeedForwardLayout {
        let intermediateSize: Int
        let usesDoubleWideMLP: Bool
    }

    /// Gemma 3 的 GGUF 仍保留四組殘差正規化權重，不能走把 `ffn_norm`
    /// 視為 attention 後正規化的通用映射。設定本身可完整由 metadata 還原。
    static func gemma3TextConfiguration(
        metadata: [String: MLXGGUFMetadataValue],
        prefix: String,
        tensorNames: [String]
    ) throws -> [String: Any] {
        guard let hiddenSize = integer("\(prefix).embedding_length", in: metadata),
              let hiddenLayers = integer("\(prefix).block_count", in: metadata),
              let intermediateSize = integer("\(prefix).feed_forward_length", in: metadata),
              let attentionHeads = integer("\(prefix).attention.head_count", in: metadata),
              let vocabularySize = metadata["tokenizer.ggml.tokens"]?.arrayValue?.count,
              hiddenSize > 0,
              hiddenLayers > 0,
              intermediateSize > 0,
              attentionHeads > 0,
              vocabularySize > 0 else {
            throw MLXGGUFLoaderError.invalidSize
        }

        let unmappedNames = MLXGGUFWeightNameNormalizer.unmappedNames(
            tensorNames,
            modelType: "gemma3_text"
        )
        guard unmappedNames.isEmpty else {
            throw MLXGGUFLoaderError.unmappedWeights(
                architecture: "gemma3",
                names: Array(unmappedNames.prefix(8))
            )
        }

        let headDim = integer("\(prefix).attention.key_length", in: metadata)
            ?? hiddenSize / attentionHeads
        var configuration: [String: Any] = [
            "model_type": "gemma3_text",
            "architectures": ["Gemma3ForCausalLM"],
            "hidden_size": hiddenSize,
            "num_hidden_layers": hiddenLayers,
            "intermediate_size": intermediateSize,
            "num_attention_heads": attentionHeads,
            "num_key_value_heads": integer(
                "\(prefix).attention.head_count_kv",
                in: metadata
            ) ?? attentionHeads,
            "head_dim": headDim,
            "rms_norm_eps": float(
                "\(prefix).attention.layer_norm_rms_epsilon",
                in: metadata
            ) ?? 1e-6,
            "vocab_size": vocabularySize,
            "rope_theta": float("\(prefix).rope.freq_base", in: metadata) ?? 1_000_000,
            "rope_local_base_freq": float(
                "\(prefix).rope.freq_base_swa",
                in: metadata
            ) ?? 10_000,
            "rope_traditional": false,
            "query_pre_attn_scalar": float(
                "\(prefix).attention.query_pre_attn_scalar",
                in: metadata
            ) ?? Float(headDim),
            "sliding_window": integer(
                "\(prefix).attention.sliding_window",
                in: metadata
            ) ?? 1_024,
            "sliding_window_pattern": integer(
                "\(prefix).attention.sliding_window_pattern",
                in: metadata
            ) ?? 6,
            "max_position_embeddings": integer("\(prefix).context_length", in: metadata)
                ?? 131_072
        ]
        if let factor = float("\(prefix).rope.scaling.factor", in: metadata) {
            configuration["rope_scaling"] = [
                "factor": factor,
                "rope_type": metadata["\(prefix).rope.scaling.type"]?.stringValue ?? "linear"
            ]
        }
        return configuration
    }

    /// Apertus 的 attention／FFN norm 名稱與標準 Llama 佈局不同，MLP 也使用
    /// xIELU 而沒有 gate projection；因此以專屬映射保留原生模型的拓樸。
    static func apertusConfiguration(
        metadata: [String: MLXGGUFMetadataValue],
        prefix: String,
        tensorNames: [String]
    ) throws -> [String: Any] {
        guard let hiddenSize = integer("\(prefix).embedding_length", in: metadata),
              let hiddenLayers = integer("\(prefix).block_count", in: metadata),
              let intermediateSize = integer("\(prefix).feed_forward_length", in: metadata),
              let attentionHeads = integer("\(prefix).attention.head_count", in: metadata),
              let vocabularySize = integer("\(prefix).vocab_size", in: metadata)
                ?? metadata["tokenizer.ggml.tokens"]?.arrayValue?.count,
              hiddenSize > 0,
              hiddenLayers > 0,
              intermediateSize > 0,
              attentionHeads > 0,
              vocabularySize > 0 else {
            throw MLXGGUFLoaderError.invalidSize
        }

        let xieluKeys = ["alpha_p", "alpha_n", "beta", "eps"]
        for key in xieluKeys {
            guard let values = arrayOfFloats("xielu.\(key)", in: metadata),
                  values.count == hiddenLayers else {
                throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                    architecture: "apertus",
                    reason: "缺少完整的逐層 xIELU \(key) 參數"
                )
            }
        }

        let unmappedNames = MLXGGUFWeightNameNormalizer.unmappedNames(
            tensorNames,
            modelType: "apertus"
        )
        guard unmappedNames.isEmpty else {
            throw MLXGGUFLoaderError.unmappedWeights(
                architecture: "apertus",
                names: Array(unmappedNames.prefix(8))
            )
        }

        var configuration: [String: Any] = [
            "model_type": "apertus",
            "architectures": ["ApertusForCausalLM"],
            "hidden_size": hiddenSize,
            "num_hidden_layers": hiddenLayers,
            "intermediate_size": intermediateSize,
            "num_attention_heads": attentionHeads,
            "num_key_value_heads": integer(
                "\(prefix).attention.head_count_kv",
                in: metadata
            ) ?? attentionHeads,
            "rms_norm_eps": float(
                "\(prefix).attention.layer_norm_rms_epsilon",
                in: metadata
            ) ?? 1e-5,
            "vocab_size": vocabularySize,
            "tie_word_embeddings": !tensorNames.contains("output.weight"),
            "max_position_embeddings": integer("\(prefix).context_length", in: metadata)
                ?? 262_144,
            "rope_theta": float("\(prefix).rope.freq_base", in: metadata) ?? 1_000_000,
            "rope_traditional": false
        ]
        if let factor = float("\(prefix).rope.scaling.factor", in: metadata) {
            configuration["rope_scaling"] = [
                "factor": factor,
                "rope_type": metadata["\(prefix).rope.scaling.type"]?.stringValue ?? "linear"
            ]
        }
        return configuration
    }

    /// 從 llama.cpp 寫入的 Gemma 4 GGUF metadata 還原文字模型設定。
    ///
    /// Gemma 4 E2B/E4B 不只是尺寸不同：它們包含 PLE、尾端共享 K/V，E2B 的共享
    /// 層還會把 MLP 寬度加倍。因此不能用單一固定 config，也不能把逐層陣列硬取第一
    /// 個值。此處只接受目前 `Gemma4TextModel` 能精確表達的兩種 FFN 版型：全層同寬，
    /// 或從第一個共享 K/V 層開始恰好加倍；其他版型會留下可診斷的錯誤。
    static func gemma4TextConfiguration(
        metadata: [String: MLXGGUFMetadataValue],
        prefix: String,
        tensorNames: [String]
    ) throws -> [String: Any] {
        guard let hiddenSize = integer("\(prefix).embedding_length", in: metadata),
              let hiddenLayers = integer("\(prefix).block_count", in: metadata),
              let attentionHeads = integer("\(prefix).attention.head_count", in: metadata),
              let vocabularySize = metadata["tokenizer.ggml.tokens"]?.arrayValue?.count,
              let slidingHeadDim = integer(
                  "\(prefix).attention.key_length_swa",
                  in: metadata
              ),
              let globalHeadDim = integer("\(prefix).attention.key_length", in: metadata),
              let hiddenSizePerLayerInput = integer(
                  "\(prefix).embedding_length_per_layer_input",
                  in: metadata
              ),
              let sharedKVLayers = integer(
                  "\(prefix).attention.shared_kv_layers",
                  in: metadata
              ),
              hiddenSize > 0,
              hiddenLayers > 0,
              attentionHeads > 0,
              vocabularySize > 0,
              slidingHeadDim > 0,
              globalHeadDim > 0,
              hiddenSizePerLayerInput >= 0,
              sharedKVLayers >= 0,
              sharedKVLayers < hiddenLayers else {
            throw MLXGGUFLoaderError.invalidSize
        }

        let expertWeightNames = tensorNames.filter {
            $0.contains(".ffn_gate_inp.")
                || $0.contains(".ffn_gate_exps.")
                || $0.contains(".ffn_up_exps.")
                || $0.contains(".ffn_down_exps.")
        }
        guard expertWeightNames.isEmpty else {
            throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                architecture: "gemma4",
                reason: "目前 MLX 文字模型尚未提供 GGUF MoE expert 權重布局"
            )
        }

        let layerTypes = try gemma4LayerTypes(
            metadata: metadata,
            prefix: prefix,
            hiddenLayers: hiddenLayers
        )
        let feedForwardLayout = try gemma4FeedForwardLayout(
            metadata: metadata,
            prefix: prefix,
            hiddenLayers: hiddenLayers,
            sharedKVLayers: sharedKVLayers
        )
        let unmappedNames = MLXGGUFWeightNameNormalizer.unmappedNames(
            tensorNames,
            modelType: "gemma4_text"
        )
        guard unmappedNames.isEmpty else {
            throw MLXGGUFLoaderError.unmappedWeights(
                architecture: "gemma4",
                names: Array(unmappedNames.prefix(8))
            )
        }

        let fullRotaryDimensions = integer("\(prefix).rope.dimension_count", in: metadata)
            ?? globalHeadDim
        let fullPartialRotaryFactor = Float(fullRotaryDimensions) / Float(globalHeadDim)
        guard fullPartialRotaryFactor > 0, fullPartialRotaryFactor <= 1 else {
            throw MLXGGUFLoaderError.invalidSize
        }

        let kvHeads = integer("\(prefix).attention.head_count_kv", in: metadata)
            ?? attentionHeads
        let hasValueProjection = tensorNames.contains {
            $0.hasPrefix("blk.") && $0.contains(".attn_v.")
        }
        let hasOutputWeight = tensorNames.contains("output.weight")
        return [
            "model_type": "gemma4_text",
            "architectures": ["Gemma4ForCausalLM"],
            "hidden_size": hiddenSize,
            "num_hidden_layers": hiddenLayers,
            "intermediate_size": feedForwardLayout.intermediateSize,
            "num_attention_heads": attentionHeads,
            "num_key_value_heads": kvHeads,
            "head_dim": slidingHeadDim,
            "global_head_dim": globalHeadDim,
            "rms_norm_eps": float(
                "\(prefix).attention.layer_norm_rms_epsilon",
                in: metadata
            ) ?? 1e-6,
            "vocab_size": vocabularySize,
            "vocab_size_per_layer_input": vocabularySize,
            "num_kv_shared_layers": sharedKVLayers,
            "hidden_size_per_layer_input": hiddenSizePerLayerInput,
            "sliding_window": integer("\(prefix).attention.sliding_window", in: metadata)
                ?? 512,
            "max_position_embeddings": integer("\(prefix).context_length", in: metadata)
                ?? 131_072,
            "attention_k_eq_v": !hasValueProjection,
            "final_logit_softcapping": float(
                "\(prefix).final_logit_softcapping",
                in: metadata
            ) ?? 30,
            "use_double_wide_mlp": feedForwardLayout.usesDoubleWideMLP,
            "layer_types": layerTypes,
            "tie_word_embeddings": !hasOutputWeight,
            "rope_parameters": [
                "sliding_attention": [
                    "rope_theta": float("\(prefix).rope.freq_base_swa", in: metadata)
                        ?? 10_000,
                    "rope_type": "default"
                ],
                "full_attention": [
                    "rope_theta": float("\(prefix).rope.freq_base", in: metadata)
                        ?? 1_000_000,
                    "partial_rotary_factor": fullPartialRotaryFactor,
                    "rope_type": "proportional"
                ]
            ]
        ]
    }

    private static func gemma4LayerTypes(
        metadata: [String: MLXGGUFMetadataValue],
        prefix: String,
        hiddenLayers: Int
    ) throws -> [String] {
        guard let pattern = arrayOfBooleans(
            "\(prefix).attention.sliding_window_pattern",
            in: metadata
        ), pattern.count == hiddenLayers else {
            throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                architecture: "gemma4",
                reason: "缺少完整的逐層 sliding_window_pattern"
            )
        }
        return pattern.map { $0 ? "sliding_attention" : "full_attention" }
    }

    private static func gemma4FeedForwardLayout(
        metadata: [String: MLXGGUFMetadataValue],
        prefix: String,
        hiddenLayers: Int,
        sharedKVLayers: Int
    ) throws -> Gemma4FeedForwardLayout {
        let key = "\(prefix).feed_forward_length"
        let values: [Int]
        if let scalar = metadata[key]?.integerValue {
            values = Array(repeating: scalar, count: hiddenLayers)
        } else if let array = arrayOfIntegers(key, in: metadata), array.count == hiddenLayers {
            values = array
        } else {
            throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                architecture: "gemma4",
                reason: "feed_forward_length 不是純量或完整的逐層陣列"
            )
        }
        guard let baseSize = values.first, baseSize > 0, values.allSatisfy({ $0 > 0 }) else {
            throw MLXGGUFLoaderError.invalidSize
        }
        if values.allSatisfy({ $0 == baseSize }) {
            return Gemma4FeedForwardLayout(
                intermediateSize: baseSize,
                usesDoubleWideMLP: false
            )
        }

        let firstSharedLayer = hiddenLayers - sharedKVLayers
        guard sharedKVLayers > 0,
              values[..<firstSharedLayer].allSatisfy({ $0 == baseSize }),
              values[firstSharedLayer...].allSatisfy({ $0 == baseSize * 2 }) else {
            throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                architecture: "gemma4",
                reason: "逐層 FFN 尺寸無法由目前的共享 K/V 雙寬 MLP 規則表示"
            )
        }
        return Gemma4FeedForwardLayout(
            intermediateSize: baseSize,
            usesDoubleWideMLP: true
        )
    }

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
              !tokens.isEmpty else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let merges = metadata["tokenizer.ggml.merges"]?.arrayValue?.compactMap(\.stringValue)
            ?? []
        let scores = metadata["tokenizer.ggml.scores"]?.arrayValue?.compactMap(\.floatValue)
            ?? []
        let tokenTypes = metadata["tokenizer.ggml.token_type"]?.arrayValue ?? []
        let tokenizerModel = normalizedArchitecture(
            metadata["tokenizer.ggml.model"]?.stringValue ?? ""
        )
        let isGemma4Tokenizer = tokenizerModel == "gemma4"
        let isSentencePieceTokenizer = tokenizerModel == "llama"
            && merges.isEmpty
            && scores.count == tokens.count
        let specialIDs = Set([
            integer("tokenizer.ggml.bos_token_id", in: metadata),
            integer("tokenizer.ggml.eos_token_id", in: metadata),
            integer("tokenizer.ggml.padding_token_id", in: metadata),
            integer("tokenizer.ggml.unknown_token_id", in: metadata),
            integer("tokenizer.ggml.mask_token_id", in: metadata)
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
                "special": Config(
                    isGemma4Tokenizer
                        || specialIDs.contains(index)
                        || isEmbeddedSpecialToken(tokens[index])
                )
            ])
        }
        if isGemma4Tokenizer {
            return gemma4TokenizerData(
                vocabulary: vocabulary,
                merges: merges,
                addedTokens: addedTokens
            )
        }
        if isSentencePieceTokenizer {
            return sentencePieceTokenizerData(
                tokens: tokens,
                scores: scores,
                addedTokens: addedTokens,
                unknownTokenID: integer("tokenizer.ggml.unknown_token_id", in: metadata) ?? 0
            )
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

    /// Gemma 4 使用 SentencePiece 風格的空白標記與 byte fallback。若套用 Qwen 的
    /// ByteLevel decoder，遇到 `▁` 或一般 Unicode token 時，上游 decoder 會因查不到
    /// byte 對照而崩潰；因此依 GGUF 的 `tokenizer.ggml.model` 選擇正確管線。
    private static func gemma4TokenizerData(
        vocabulary: [String: Config],
        merges: [String],
        addedTokens: [Config]
    ) -> Config {
        let replaceSpace = Config([
            "type": Config("Replace"),
            "pattern": Config(["String": Config(" ")]),
            "content": Config("▁")
        ])
        let splitSpace = Config([
            "type": Config("Split"),
            "pattern": Config(["String": Config(" ")]),
            "behavior": Config("MergedWithPrevious"),
            "invert": Config(false)
        ])
        let templateProcessing = Config([
            "type": Config("TemplateProcessing"),
            "single": Config([
                Config(["Sequence": Config(["id": Config("A"), "type_id": Config(0)])])
            ]),
            "pair": Config([
                Config(["Sequence": Config(["id": Config("A"), "type_id": Config(0)])]),
                Config(["Sequence": Config(["id": Config("B"), "type_id": Config(1)])])
            ]),
            "special_tokens": Config([String: Config]())
        ])
        let decoder = Config([
            "type": Config("Sequence"),
            "decoders": Config([
                Config([
                    "type": Config("Replace"),
                    "pattern": Config(["String": Config("▁")]),
                    "content": Config(" ")
                ]),
                Config(["type": Config("ByteFallback")]),
                Config(["type": Config("Fuse")])
            ])
        ])
        return Config([
            "version": Config("1.0"),
            "added_tokens": Config(addedTokens),
            "normalizer": replaceSpace,
            "pre_tokenizer": splitSpace,
            "post_processor": templateProcessing,
            "decoder": decoder,
            "model": Config([
                "type": Config("BPE"),
                "dropout": Config("nil"),
                "unk_token": Config("<unk>"),
                "continuing_subword_prefix": Config("nil"),
                "end_of_word_suffix": Config("nil"),
                "fuse_unk": Config(true),
                "byte_fallback": Config(true),
                "ignore_merges": Config(false),
                "vocab": Config(vocabulary),
                "merges": Config(merges.map { Config($0) })
            ])
        ])
    }

    /// llama.cpp 的 `tokenizer.ggml.model = llama` 會以 SentencePiece token 與分數
    /// 保存詞表，不一定包含 BPE merges。此時改走 Unigram，避免把合法 GGUF 誤判成
    /// 缺少 tokenizer；空白與 byte fallback 解碼則沿用 SentencePiece 管線。
    private static func sentencePieceTokenizerData(
        tokens: [String],
        scores: [Float],
        addedTokens: [Config],
        unknownTokenID: Int
    ) -> Config {
        // Swift 的 String／NSString 會把 Unicode 正規等價字串視為同一個 key；大型
        // SentencePiece 詞表可能同時保留 composed 與 decomposed token，直接交給
        // 上游 UnigramTokenizer 會在建 Dictionary 時觸發 duplicate-key crash。
        // 只替後續等價項目加上不會出現在輸入裡的 private-use marker，解碼時再移除，
        // 因而保留原始 token ID、權重 vocab 對齊與可讀輸出。
        let duplicateMarker = "\u{F0000}"
        var uniqueTokens = [String]()
        uniqueTokens.reserveCapacity(tokens.count)
        var occupied = Set<String>()
        for token in tokens {
            var candidate = token
            while !occupied.insert(candidate).inserted {
                candidate += duplicateMarker
            }
            uniqueTokens.append(candidate)
        }
        let vocabulary = zip(uniqueTokens, scores).map { token, score in
            Config([Config(token), Config(score)])
        }
        let replaceSpace = Config([
            "type": Config("Replace"),
            "pattern": Config(["String": Config(" ")]),
            "content": Config("▁")
        ])
        let splitSpace = Config([
            "type": Config("Split"),
            "pattern": Config(["String": Config(" ")]),
            "behavior": Config("MergedWithPrevious"),
            "invert": Config(false)
        ])
        let templateProcessing = Config([
            "type": Config("TemplateProcessing"),
            "single": Config([
                Config(["Sequence": Config(["id": Config("A"), "type_id": Config(0)])])
            ]),
            "pair": Config([
                Config(["Sequence": Config(["id": Config("A"), "type_id": Config(0)])]),
                Config(["Sequence": Config(["id": Config("B"), "type_id": Config(1)])])
            ]),
            "special_tokens": Config([String: Config]())
        ])
        let decoder = Config([
            "type": Config("Sequence"),
            "decoders": Config([
                Config([
                    "type": Config("Replace"),
                    "pattern": Config(["String": Config("▁")]),
                    "content": Config(" ")
                ]),
                Config([
                    "type": Config("Replace"),
                    "pattern": Config(["String": Config(duplicateMarker)]),
                    "content": Config("")
                ]),
                Config(["type": Config("ByteFallback")]),
                Config(["type": Config("Fuse")])
            ])
        ])
        return Config([
            "version": Config("1.0"),
            "added_tokens": Config(addedTokens),
            "normalizer": replaceSpace,
            "pre_tokenizer": splitSpace,
            "post_processor": templateProcessing,
            "decoder": decoder,
            "model": Config([
                "type": Config("Unigram"),
                "vocab": Config(vocabulary),
                "unk_id": Config(unknownTokenID),
                "byte_fallback": Config(true)
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
        let tokenizerModel = normalizedArchitecture(
            metadata["tokenizer.ggml.model"]?.stringValue ?? ""
        )
        let isGemma4 = tokenizerModel == "gemma4"
        let isSentencePiece = tokenizerModel == "llama"
            && metadata["tokenizer.ggml.merges"] == nil
            && metadata["tokenizer.ggml.scores"]?.arrayValue != nil
        var values: [String: Config] = [
            "tokenizer_class": Config(
                isSentencePiece
                    ? "XLMRobertaTokenizer"
                    : isGemma4 ? "GemmaTokenizer" : "Qwen2Tokenizer"
            ),
            "add_bos_token": Config(
                isGemma4 || isSentencePiece
                    ? metadata["tokenizer.ggml.add_bos_token"]?.booleanValue ?? true
                    : false
            ),
            "add_eos_token": Config(false),
            "add_prefix_space": Config(false),
            "clean_up_tokenization_spaces": Config(false),
            "eos_token": tokenString(integer("tokenizer.ggml.eos_token_id", in: metadata)),
            "pad_token": tokenString(integer("tokenizer.ggml.padding_token_id", in: metadata)),
            "model_max_length": Config(262_144),
            "additional_special_tokens": Config(specialTokens)
        ]
        if isGemma4 || isSentencePiece {
            values["bos_token"] = tokenString(integer("tokenizer.ggml.bos_token_id", in: metadata))
            values["unk_token"] = tokenString(
                integer("tokenizer.ggml.unknown_token_id", in: metadata)
            )
            values["mask_token"] = tokenString(
                integer("tokenizer.ggml.mask_token_id", in: metadata)
            )
            values["padding_side"] = Config("left")
        }
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
        guard let values = metadata[key]?.arrayValue else { return nil }
        let integers = values.compactMap(\.integerValue)
        return integers.count == values.count ? integers : nil
    }

    private static func arrayOfBooleans(
        _ key: String,
        in metadata: [String: MLXGGUFMetadataValue]
    ) -> [Bool]? {
        guard let values = metadata[key]?.arrayValue else { return nil }
        let booleans = values.compactMap(\.booleanValue)
        return booleans.count == values.count ? booleans : nil
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
