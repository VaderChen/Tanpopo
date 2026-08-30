import XCTest
import MLX
import MLXHuggingFace
import MLXLLM
import MLXLMCommon
import Tokenizers
@testable import MLXServer

final class MLXGGUFCompatibilityTests: XCTestCase {
    private struct LayerTraceSnapshot: Sendable {
        let hiddenValues: [Float]
        let lastLogits: [Float]
        let sequenceLength: Int
        let hiddenSize: Int
        let layerCount: Int
    }

    func testMLXCoreGGUFBridgeMatchesCurrentPackedFormats() throws {
        let q4Bytes = (0..<16).map { index in
            UInt8(index | ((15 - index) << 4))
        }
        let q4Expected = (0..<16).map { Float($0 - 8) }
            + (0..<16).map { Float(7 - $0) }
        let q41Low = (0..<16).map { Float($0) * 0.5 - 1 }
        let q41High = (0..<16).map { Float(15 - $0) * 0.5 - 1 }
        let q41Expected = q41Low + q41High
        let q8Values = (-16..<16).map(Int8.init)

        let cases: [(name: String, type: UInt32, bits: Int, bytes: [UInt8], expected: [Float])] = [
            ("Q4_0", 2, 4, [0x00, 0x3c] + q4Bytes, q4Expected),
            ("Q4_1", 3, 4, [0x00, 0x38, 0x00, 0xbc] + q4Bytes, q41Expected),
            (
                "Q8_0",
                8,
                8,
                [0x00, 0x34] + q8Values.map { UInt8(bitPattern: $0) },
                q8Values.map { Float($0) * 0.25 }
            )
        ]

        for testCase in cases {
            let core = try MLXNativeGGUFBackend().packCoreReference(
                raw: Data(testCase.bytes),
                sourceType: testCase.type,
                rows: 1,
                columns: 32
            )
            let coreArray = dequantized(
                core.wq,
                scales: core.scales,
                biases: core.biases,
                groupSize: 32,
                bits: testCase.bits
            )
            eval(coreArray)
            let coreValues = coreArray.asArray(Float.self)
            let current = try MLXGGUFMetalQuantizer.packPreserved(
                raw: Data(testCase.bytes),
                sourceType: testCase.type,
                sourceShape: [1, 32],
                targetWeightShape: [1, testCase.bits == 4 ? 4 : 8],
                targetScaleShape: [1, 1]
            )
            let currentArray = dequantized(
                current.wq,
                scales: current.scales,
                biases: current.biases,
                groupSize: 32,
                bits: testCase.bits
            )
            eval(currentArray)
            let currentValues = currentArray.asArray(Float.self)

            XCTAssertEqual(coreValues.count, testCase.expected.count, testCase.name)
            XCTAssertEqual(currentValues.count, testCase.expected.count, testCase.name)
            for index in testCase.expected.indices {
                XCTAssertEqual(
                    coreValues[index], testCase.expected[index], accuracy: 0.01,
                    "MLX Core \(testCase.name) index \(index)"
                )
                XCTAssertEqual(
                    currentValues[index], coreValues[index], accuracy: 0.01,
                    "Tanpopo \(testCase.name) index \(index)"
                )
            }
        }
    }

    func testPreservedQ40BlockReconstructsTheOriginalValues() throws {
        var bytes: [UInt8] = [0x00, 0x3c] // Float16(1.0)，little endian
        bytes.append(contentsOf: (0..<16).map { index in
            UInt8(index | ((15 - index) << 4))
        })
        let packed = try MLXGGUFMetalQuantizer.packPreserved(
            raw: Data(bytes),
            sourceType: 2,
            sourceShape: [1, 32],
            targetWeightShape: [1, 4],
            targetScaleShape: [1, 1]
        )
        let reconstructed = dequantized(
            packed.wq,
            scales: packed.scales,
            biases: packed.biases,
            groupSize: 32,
            bits: 4
        )
        eval(reconstructed)

        let expected = (0..<16).map { Float($0 - 8) }
            + (0..<16).map { Float(7 - $0) }
        let actual = reconstructed.asArray(Float.self)
        XCTAssertEqual(actual.count, expected.count)
        for (actualValue, expectedValue) in zip(actual, expected) {
            XCTAssertEqual(actualValue, expectedValue, accuracy: 0.01)
        }
    }

    func testWeightContractReportsMissingUnexpectedAndShapeMismatch() {
        let expected: [(String, MLXArray)] = [
            ("model.present.weight", MLXArray.zeros([2, 4])),
            ("model.missing.weight", MLXArray.zeros([3, 4])),
            ("model.shape.weight", MLXArray.zeros([4, 8]))
        ]
        let actual = [
            "model.present.weight": MLXArray.zeros([2, 4]),
            "model.shape.weight": MLXArray.zeros([8, 4]),
            "model.unexpected.weight": MLXArray.zeros([1])
        ]

        let report = MLXGGUFWeightContract.inspect(
            expected: expected,
            weights: actual
        )

        XCTAssertFalse(report.isCompatible)
        XCTAssertEqual(report.expectedCount, 3)
        XCTAssertEqual(report.actualCount, 3)
        XCTAssertEqual(
            report.issues,
            [
                MLXGGUFWeightContractIssue(
                    kind: .missing,
                    name: "model.missing.weight",
                    expectedShape: [3, 4],
                    actualShape: nil
                ),
                MLXGGUFWeightContractIssue(
                    kind: .unexpected,
                    name: "model.unexpected.weight",
                    expectedShape: nil,
                    actualShape: [1]
                ),
                MLXGGUFWeightContractIssue(
                    kind: .shapeMismatch,
                    name: "model.shape.weight",
                    expectedShape: [4, 8],
                    actualShape: [8, 4]
                )
            ]
        )
    }

    /// 真實模型的內容排列 POC；CI 沒有模型檔時會自動略過。
    ///
    /// 這個測試不把 Qwen 當成決策規則，而是用它的 fused QKV 當作
    /// 具有重排需求的證明樣本。實際生產邏輯仍由架構佈局分派器統一處理。
    func testRealQwen35LayoutAgainstNativeMLXWhenConfigured() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let ggufPath = environment["TANPOPO_POC_GGUF"],
              let nativePath = environment["TANPOPO_POC_NATIVE_SAFETENSORS"],
              let configPath = environment["TANPOPO_POC_CONFIG"] else {
            throw XCTSkip("No real GGUF POC model configured")
        }

        let configData = try Data(contentsOf: URL(fileURLWithPath: configPath))
        let embeddedConfigData = try MLXNativeGGUFBackend().configurationData(
            fileURL: URL(fileURLWithPath: ggufPath)
        )
        let embeddedConfiguration = try XCTUnwrap(
            JSONSerialization.jsonObject(with: embeddedConfigData) as? [String: Any]
        )
        XCTAssertEqual(embeddedConfiguration["attn_output_gate"] as? Bool, true)
        XCTAssertEqual(
            (embeddedConfiguration["layer_types"] as? [String])?.count,
            embeddedConfiguration["num_hidden_layers"] as? Int
        )
        let stopTokenIDs = (embeddedConfiguration["eos_token_id"] as? [NSNumber])?
            .map(\.intValue) ?? []
        XCTAssertTrue(stopTokenIDs.contains(248044))
        XCTAssertTrue(stopTokenIDs.contains(248046))
        let sourceWeights = try MLXGGUFLoader.loadWeights(
            from: URL(fileURLWithPath: ggufPath),
            targetGroupSize: 64,
            quantizationProfile: .automatic,
            convertQwen35StateSpaceParameters: true
        )
        let reorderedWeights = MLXGGUFModelLoader.applyArchitectureWeightLayout(
            sourceWeights,
            modelType: "qwen3_5",
            configurationData: configData,
            groupSize: 64
        )
        let rawNormalized = try MLXGGUFWeightNameNormalizer.normalize(
            sourceWeights,
            modelType: "qwen3_5",
            maximumLayerIndex: 32
        )
        let reorderedNormalized = try MLXGGUFWeightNameNormalizer.normalize(
            reorderedWeights,
            modelType: "qwen3_5",
            maximumLayerIndex: 32
        )
        let nativeWeights = try loadArrays(url: URL(fileURLWithPath: nativePath))
        let sampleNames = [
            "model.layers.0.linear_attn.in_proj_qkv",
            "model.layers.0.linear_attn.in_proj_z",
            "model.layers.0.linear_attn.in_proj_a",
            "model.layers.0.linear_attn.in_proj_b",
            "model.layers.0.linear_attn.out_proj",
            "model.layers.0.mlp.gate_proj",
            "model.layers.0.mlp.up_proj",
            "model.layers.0.mlp.down_proj",
            "model.layers.3.self_attn.q_proj",
            "model.layers.3.self_attn.k_proj",
            "model.layers.3.self_attn.v_proj",
            "model.layers.3.self_attn.o_proj"
        ]

        var improvements = 0
        for name in sampleNames {
            let nativeName = "language_model." + name
            guard let reference = dequantizedWeight(
                named: nativeName,
                in: nativeWeights,
                groupSize: 64
            ), let raw = dequantizedWeight(
                named: name,
                in: rawNormalized,
                groupSize: 64
            ), let reordered = dequantizedWeight(
                named: name,
                in: reorderedNormalized,
                groupSize: 64
            ), reference.shape == raw.shape,
               reference.shape == reordered.shape else {
                XCTFail("Missing comparable tensor: \(name)")
                continue
            }

            let rawError = normalizedMeanAbsoluteError(reference, raw)
            let reorderedError = normalizedMeanAbsoluteError(reference, reordered)
            if reorderedError + 0.0001 < rawError {
                improvements += 1
            }
            print(
                "GGUF_LAYOUT_POC name=\(name) "
                    + "raw_nmae=\(rawError) reordered_nmae=\(reorderedError)"
            )
        }
        let directNames = [
            "model.layers.0.linear_attn.A_log",
            "model.layers.0.linear_attn.dt_bias",
            "model.layers.0.linear_attn.conv1d.weight",
            "model.layers.0.linear_attn.norm.weight",
            "model.layers.0.input_layernorm.weight"
        ]
        for name in directNames {
            let nativeName = "language_model." + name
            guard let reference = nativeWeights[nativeName],
                  let raw = rawNormalized[name],
                  let reordered = reorderedNormalized[name],
                  reference.shape == raw.shape,
                  reference.shape == reordered.shape else {
                XCTFail("Missing comparable direct tensor: \(name)")
                continue
            }
            let rawError = normalizedMeanAbsoluteError(reference, raw)
            let reorderedError = normalizedMeanAbsoluteError(reference, reordered)
            print(
                "GGUF_LAYOUT_POC_DIRECT name=\(name) "
                    + "raw_nmae=\(rawError) reordered_nmae=\(reorderedError)"
            )
        }
        if environment["TANPOPO_POC_EXHAUSTIVE"] == "1" {
            var errors = [(name: String, value: Float)]()
            for weightName in reorderedNormalized.keys.sorted()
            where weightName.hasSuffix(".weight") {
                let baseName = String(weightName.dropLast(".weight".count))
                let nativeName = "language_model." + baseName
                let candidate: MLXArray?
                let reference: MLXArray?
                if reorderedNormalized[baseName + ".scales"] != nil {
                    candidate = dequantizedWeight(
                        named: baseName,
                        in: reorderedNormalized,
                        groupSize: 64
                    )
                    reference = dequantizedWeight(
                        named: nativeName,
                        in: nativeWeights,
                        groupSize: 64
                    )
                } else {
                    candidate = reorderedNormalized[weightName]
                    reference = nativeWeights[nativeName + ".weight"]
                }
                guard let candidate, let reference,
                      candidate.shape == reference.shape else { continue }
                errors.append((
                    name: baseName,
                    value: normalizedMeanAbsoluteError(reference, candidate)
                ))
            }
            for name in reorderedNormalized.keys.sorted()
            where !name.hasSuffix(".scales") && !name.hasSuffix(".biases") {
                let baseName = name.hasSuffix(".weight")
                    ? String(name.dropLast(".weight".count)) : name
                guard reorderedNormalized[baseName + ".scales"] == nil,
                      let candidate = reorderedNormalized[name],
                      let reference = nativeWeights["language_model." + name],
                      candidate.shape == reference.shape else { continue }
                errors.append((
                    name: name,
                    value: normalizedMeanAbsoluteError(reference, candidate)
                ))
            }
            for error in errors.sorted(by: { $0.value > $1.value }).prefix(24) {
                print("GGUF_LAYOUT_POC_WORST name=\(error.name) nmae=\(error.value)")
            }
        }
        XCTAssertGreaterThan(
            improvements,
            0,
            "Architecture layout transform did not improve any sampled tensor"
        )
    }

    /// 使用同一份作者 tokenizer 資產時，GGUF 與純 MLX 必須形成完全相同的
    /// prompt token；否則精度差異其實來自前處理，而不是模型權重或運算核心。
    func testRealQwen35TokenizerMatchesNativeWhenConfigured() async throws {
        let environment = ProcessInfo.processInfo.environment
        guard let ggufPath = environment["TANPOPO_POC_GGUF"],
              let nativePath = environment["TANPOPO_POC_NATIVE_SAFETENSORS"] else {
            throw XCTSkip("No real GGUF POC model configured")
        }
        let ggufURL = URL(fileURLWithPath: ggufPath)
        let ggufTokenizer = try await MLXGGUFEmbeddedAssets.tokenizer(
            directoryURL: ggufURL.deletingLastPathComponent(),
            weightURL: ggufURL
        )
        let loader: any MLXLMCommon.TokenizerLoader = #huggingFaceTokenizerLoader()
        let nativeTokenizer = try await loader.load(
            from: URL(fileURLWithPath: nativePath).deletingLastPathComponent()
        )
        let messages: [[String: any Sendable]] = [[
            "role": "user",
            "content": "Which answer is correct? A. Alpha B. Beta C. Gamma D. Delta"
        ]]
        let context: [String: any Sendable] = ["enable_thinking": false]
        let ggufTokens = try ggufTokenizer.applyChatTemplate(
            messages: messages,
            tools: nil,
            additionalContext: context
        )
        let nativeTokens = try nativeTokenizer.applyChatTemplate(
            messages: messages,
            tools: nil,
            additionalContext: context
        )
        XCTAssertEqual(ggufTokens, nativeTokens)
    }

    /// 通用逐層數值追蹤：透過 DFlash target protocol 擷取每層輸出，定位
    /// GGUF 與原生 MLX 從哪一層開始明顯分歧。樣本模型只負責證明診斷策略。
    func testRealQwen35LayerDriftWhenConfigured() async throws {
        let environment = ProcessInfo.processInfo.environment
        guard environment["TANPOPO_POC_LAYER_TRACE"] == "1",
              let ggufPath = environment["TANPOPO_POC_GGUF"],
              let nativePath = environment["TANPOPO_POC_NATIVE_SAFETENSORS"] else {
            throw XCTSkip("No real GGUF layer trace configured")
        }
        let ggufURL = URL(fileURLWithPath: ggufPath)
        let nativeDirectory = URL(fileURLWithPath: nativePath).deletingLastPathComponent()
        let tokenizerLoader: any MLXLMCommon.TokenizerLoader = #huggingFaceTokenizerLoader()
        let nativeContainer = try await LLMModelFactory.shared.loadContainer(
            from: nativeDirectory,
            using: tokenizerLoader
        )
        let ggufContainer = try await MLXGGUFModelLoader.loadContainer(
            from: ggufURL.deletingLastPathComponent(),
            weightURL: ggufURL,
            quantizationProfile: .automatic
        )
        let messages: [[String: any Sendable]] = [[
            "role": "user", "content": "Answer with only one letter: A, B, C, or D."
        ]]
        let tokens = try (await nativeContainer.tokenizer).applyChatTemplate(
            messages: messages,
            tools: nil,
            additionalContext: ["enable_thinking": false]
        )
        let native = try await layerTrace(container: nativeContainer, tokens: tokens)
        let gguf = try await layerTrace(container: ggufContainer, tokens: tokens)
        XCTAssertEqual(native.sequenceLength, gguf.sequenceLength)
        XCTAssertEqual(native.hiddenSize, gguf.hiddenSize)
        XCTAssertEqual(native.layerCount, gguf.layerCount)

        for layer in 0..<native.layerCount {
            var error: Float = 0
            var magnitude: Float = 0
            for token in 0..<native.sequenceLength {
                let start = token * native.hiddenSize * native.layerCount
                    + layer * native.hiddenSize
                for index in 0..<native.hiddenSize {
                    let reference = native.hiddenValues[start + index]
                    let candidate = gguf.hiddenValues[start + index]
                    error += abs(reference - candidate)
                    magnitude += abs(reference)
                }
            }
            let nmae = error / max(magnitude, 1e-8)
            print("GGUF_LAYER_DRIFT layer=\(layer) nmae=\(nmae)")
        }
        let nativeTop = native.lastLogits.enumerated().max(by: { $0.element < $1.element })?.offset
        let ggufTop = gguf.lastLogits.enumerated().max(by: { $0.element < $1.element })?.offset
        print("GGUF_LAYER_DRIFT_TOP native=\(nativeTop ?? -1) gguf=\(ggufTop ?? -1)")
    }

    func testGGUFAndKVCacheDefaultsUseTheGenericPerformancePolicy() {
        let configuration = ServerConfiguration()

        XCTAssertNil(configuration.ggufGroupSize)
        XCTAssertEqual(configuration.ggufProfile, .automatic)
        XCTAssertEqual(configuration.ggufRecurrentPromotion, .disabled)
        XCTAssertNil(configuration.kvBits)
        XCTAssertEqual(configuration.quantizedKVStart, 2_048)
    }

    func testConfigurationParsesGGUFRecurrentPromotion() throws {
        let modelURL = FileManager.default.temporaryDirectory
            .appendingPathComponent("tanpopo-fastgguf-\(UUID().uuidString).gguf")
        XCTAssertTrue(FileManager.default.createFile(atPath: modelURL.path, contents: Data()))
        defer { try? FileManager.default.removeItem(at: modelURL) }

        let configuration = try ServerConfiguration.parse([
            "--model", modelURL.path,
            "--gguf-recurrent-promotion", "controls"
        ])

        XCTAssertEqual(configuration.ggufRecurrentPromotion, .controls)
    }

    func testAutomaticGGUFStrategyUsesTensorMetadataInsteadOfModelNames() throws {
        let tensors = [
            MLXGGUFTensorInfo(
                name: "arbitrary.float.weight", dimensions: [4_096, 4_096], type: 0, offset: 0),
            MLXGGUFTensorInfo(
                name: "arbitrary.q4.weight", dimensions: [4_096, 4_096], type: 12, offset: 0),
            MLXGGUFTensorInfo(
                name: "arbitrary.q5.weight", dimensions: [4_096, 4_096], type: 13, offset: 0),
            MLXGGUFTensorInfo(
                name: "arbitrary.q8.weight", dimensions: [4_096, 4_096], type: 8, offset: 0)
        ]

        let strategy = try MLXGGUFLoader.quantizationStrategy(
            for: tensors,
            requestedGroupSize: nil,
            profile: .automatic
        )

        XCTAssertEqual(strategy.groupSize, 32)
        XCTAssertTrue(strategy.usedResolvedGroup32)
        XCTAssertEqual(strategy.targetStorageCounts[.bf16], 1)
        XCTAssertNil(strategy.targetStorageCounts[.int4])
        XCTAssertEqual(strategy.targetStorageCounts[.int8], 3)
    }

    func testAutomaticGGUFStrategyFallsBackToGroup32ForIncompatibleDimensions() throws {
        let tensors = [
            MLXGGUFTensorInfo(
                name: "generic.96-wide.weight", dimensions: [96, 128], type: 2, offset: 0)
        ]

        let strategy = try MLXGGUFLoader.quantizationStrategy(
            for: tensors,
            requestedGroupSize: nil,
            profile: .automatic
        )

        XCTAssertEqual(strategy.groupSize, 32)
        XCTAssertTrue(strategy.usedResolvedGroup32)
    }

    func testSpeedGGUFStrategyKeepsPreservedSourceBlocksAtGroup32() throws {
        let tensors = [
            MLXGGUFTensorInfo(
                name: "generic.q4.weight", dimensions: [4_096, 4_096], type: 2, offset: 0)
        ]

        let strategy = try MLXGGUFLoader.quantizationStrategy(
            for: tensors,
            requestedGroupSize: nil,
            profile: .speed
        )

        XCTAssertEqual(strategy.groupSize, 32)
        XCTAssertTrue(strategy.usedResolvedGroup32)
        XCTAssertEqual(strategy.targetStorageCounts[.int4], 1)
    }

    func testGGUFQualityProfileUsesFP32ReferenceStorage() throws {
        let tensors = [
            MLXGGUFTensorInfo(
                name: "generic.q5.weight", dimensions: [4_096, 4_096], type: 13, offset: 0)
        ]

        let strategy = try MLXGGUFLoader.quantizationStrategy(
            for: tensors,
            requestedGroupSize: 64,
            profile: .quality
        )

        XCTAssertEqual(strategy.targetStorageCounts[.fp32], 1)
        XCTAssertNil(strategy.targetStorageCounts[.int4])
        XCTAssertNil(strategy.targetStorageCounts[.int8])
    }

    func testGGUFAutomaticProfileRequantizesQ4AsInt8() throws {
        let tensors = [
            MLXGGUFTensorInfo(
                name: "generic.q4.weight", dimensions: [4_096, 4_096], type: 2, offset: 0)
        ]

        let strategy = try MLXGGUFLoader.quantizationStrategy(
            for: tensors,
            requestedGroupSize: 32,
            profile: .automatic
        )

        XCTAssertEqual(strategy.targetStorageCounts[.int8], 1)
        XCTAssertNil(strategy.targetStorageCounts[.int4])
    }

    func testEmbeddedConfigurationPreservesTokenizerTokenIDs() {
        let configuration = MLXGGUFEmbeddedAssets.addingTokenizerTokenIDs(
            to: ["model_type": "apertus"],
            metadata: [
                "tokenizer.ggml.eos_token_id": .uint32(2),
                "tokenizer.ggml.bos_token_id": .uint32(1),
                "tokenizer.ggml.padding_token_id": .uint32(3),
                "tokenizer.ggml.unknown_token_id": .uint32(0)
            ]
        )

        XCTAssertEqual(configuration["eos_token_id"] as? Int, 2)
        XCTAssertEqual(configuration["bos_token_id"] as? Int, 1)
        XCTAssertEqual(configuration["pad_token_id"] as? Int, 3)
        XCTAssertEqual(configuration["unk_token_id"] as? Int, 0)
    }

    func testChatTemplateAddsItsAssistantEndTokenToEOSSet() {
        let metadata: [String: MLXGGUFMetadataValue] = [
            "tokenizer.ggml.eos_token_id": .uint32(0),
            "tokenizer.ggml.tokens": .array([
                .string("</s>"), .string("<|assistant_end|>"),
                .string("<|user_end|>"), .string("ordinary")
            ]),
            "tokenizer.ggml.token_type": .array([
                .int32(3), .int32(3), .int32(3), .int32(1)
            ]),
            "tokenizer.chat_template": .string(
                "{% set end_assistant_token = '<|assistant_end|>' %} "
                    + "{% set end_user_token = '<|user_end|>' %}"
            )
        ]

        XCTAssertEqual(
            MLXGGUFEmbeddedAssets.generationStopTokenIDs(
                configuration: [:],
                metadata: metadata
            ),
            [0, 1]
        )
    }

    func testGemma4E2BStyleConfigurationUsesDoubleWideSharedLayers() throws {
        var metadata = gemma4Metadata(hiddenLayers: 4, sharedKVLayers: 2)
        metadata["gemma4.feed_forward_length"] = .array([
            .uint32(64), .uint32(64), .uint32(128), .uint32(128)
        ])

        let configuration = try MLXGGUFEmbeddedAssets.gemma4TextConfiguration(
            metadata: metadata,
            prefix: "gemma4",
            tensorNames: gemma4TensorNames
        )

        XCTAssertEqual(configuration["model_type"] as? String, "gemma4_text")
        XCTAssertEqual(configuration["intermediate_size"] as? Int, 64)
        XCTAssertEqual(configuration["use_double_wide_mlp"] as? Bool, true)
        XCTAssertEqual(configuration["num_kv_shared_layers"] as? Int, 2)
        XCTAssertEqual(
            configuration["layer_types"] as? [String],
            ["sliding_attention", "full_attention", "sliding_attention", "full_attention"]
        )
    }

    func testGemma4E4BStyleConfigurationKeepsUniformMLPWidth() throws {
        var metadata = gemma4Metadata(hiddenLayers: 4, sharedKVLayers: 1)
        metadata["gemma4.feed_forward_length"] = .uint32(96)

        let configuration = try MLXGGUFEmbeddedAssets.gemma4TextConfiguration(
            metadata: metadata,
            prefix: "gemma4",
            tensorNames: gemma4TensorNames
        )

        XCTAssertEqual(configuration["intermediate_size"] as? Int, 96)
        XCTAssertEqual(configuration["use_double_wide_mlp"] as? Bool, false)
        XCTAssertEqual(configuration["tie_word_embeddings"] as? Bool, true)
    }

    func testGemma4WeightNamesUseDedicatedLayout() {
        let cases = [
            "per_layer_token_embd.weight": "model.embed_tokens_per_layer.weight",
            "per_layer_model_proj.weight": "model.per_layer_model_projection.weight",
            "per_layer_proj_norm.weight": "model.per_layer_projection_norm.weight",
            "blk.3.ffn_norm.weight": "model.layers.3.pre_feedforward_layernorm.weight",
            "blk.3.post_ffw_norm.weight": "model.layers.3.post_feedforward_layernorm.weight",
            "blk.3.inp_gate.weight": "model.layers.3.per_layer_input_gate.weight",
            "blk.3.proj.weight": "model.layers.3.per_layer_projection.weight",
            "blk.3.post_norm.weight": "model.layers.3.post_per_layer_input_norm.weight",
            "blk.3.layer_output_scale.weight": "model.layers.3.layer_scalar"
        ]

        for (source, expected) in cases {
            XCTAssertEqual(
                MLXGGUFWeightNameNormalizer.normalizedName(
                    source,
                    modelType: "gemma4_text"
                ),
                expected,
                source
            )
        }
    }

    func testGemma4RejectsAnUnrepresentablePerLayerFFNLayout() throws {
        var metadata = gemma4Metadata(hiddenLayers: 4, sharedKVLayers: 2)
        metadata["gemma4.feed_forward_length"] = .array([
            .uint32(64), .uint32(80), .uint32(128), .uint32(128)
        ])

        XCTAssertThrowsError(
            try MLXGGUFEmbeddedAssets.gemma4TextConfiguration(
                metadata: metadata,
                prefix: "gemma4",
                tensorNames: gemma4TensorNames
            )
        ) { error in
            guard case MLXGGUFLoaderError.unsupportedArchitectureVariant = error else {
                return XCTFail("預期回報不支援的 Gemma 4 變體，實際為：\(error)")
            }
        }
    }

    func testGemma3ConfigurationRestoresSlidingAttentionAndRopeScaling() throws {
        let metadata: [String: MLXGGUFMetadataValue] = [
            "gemma3.embedding_length": .uint32(3_840),
            "gemma3.block_count": .uint32(48),
            "gemma3.feed_forward_length": .uint32(15_360),
            "gemma3.attention.head_count": .uint32(16),
            "gemma3.attention.head_count_kv": .uint32(8),
            "gemma3.attention.key_length": .uint32(256),
            "gemma3.attention.sliding_window": .uint32(1_024),
            "gemma3.attention.layer_norm_rms_epsilon": .float32(1e-6),
            "gemma3.context_length": .uint32(131_072),
            "gemma3.rope.freq_base": .float32(1_000_000),
            "gemma3.rope.scaling.type": .string("linear"),
            "gemma3.rope.scaling.factor": .float32(8),
            "tokenizer.ggml.tokens": .array([
                .string("<pad>"), .string("<eos>"), .string("<bos>")
            ])
        ]
        let configuration = try MLXGGUFEmbeddedAssets.gemma3TextConfiguration(
            metadata: metadata,
            prefix: "gemma3",
            tensorNames: gemma3TensorNames
        )

        XCTAssertEqual(configuration["model_type"] as? String, "gemma3_text")
        XCTAssertEqual(configuration["head_dim"] as? Int, 256)
        XCTAssertEqual(configuration["sliding_window_pattern"] as? Int, 6)
        let ropeScaling = configuration["rope_scaling"] as? [String: Any]
        XCTAssertEqual(ropeScaling?["rope_type"] as? String, "linear")
        XCTAssertEqual(ropeScaling?["factor"] as? Float, 8)
    }

    func testGemma3WeightNamesPreserveAllFourResidualNorms() {
        let cases = [
            "blk.2.attn_norm.weight": "model.layers.2.input_layernorm.weight",
            "blk.2.post_attention_norm.weight":
                "model.layers.2.post_attention_layernorm.weight",
            "blk.2.ffn_norm.weight": "model.layers.2.pre_feedforward_layernorm.weight",
            "blk.2.post_ffw_norm.weight": "model.layers.2.post_feedforward_layernorm.weight"
        ]
        for (source, expected) in cases {
            XCTAssertEqual(
                MLXGGUFWeightNameNormalizer.normalizedName(
                    source,
                    modelType: "gemma3_text"
                ),
                expected,
                source
            )
        }
    }

    func testApertusConfigurationAndDedicatedNormMappings() throws {
        let metadata: [String: MLXGGUFMetadataValue] = [
            "apertus.embedding_length": .uint32(8_192),
            "apertus.block_count": .uint32(80),
            "apertus.feed_forward_length": .uint32(43_008),
            "apertus.attention.head_count": .uint32(64),
            "apertus.attention.head_count_kv": .uint32(8),
            "apertus.attention.layer_norm_rms_epsilon": .float32(1e-5),
            "apertus.context_length": .uint32(262_144),
            "apertus.rope.freq_base": .float32(4_000_000),
            "apertus.vocab_size": .uint32(131_072),
            "xielu.alpha_p": .array(Array(repeating: .float32(0.55), count: 80)),
            "xielu.alpha_n": .array(Array(repeating: .float32(0.55), count: 80)),
            "xielu.beta": .array(Array(repeating: .float32(0.5), count: 80)),
            "xielu.eps": .array(Array(repeating: .float32(-1e-6), count: 80)),
            "tokenizer.ggml.tokens": .array([.string("<unk>")])
        ]
        let tensorNames = [
            "token_embd.weight", "output_norm.weight", "output.weight",
            "blk.0.attn_norm.weight", "blk.0.attn_q.weight", "blk.0.attn_k.weight",
            "blk.0.attn_v.weight", "blk.0.attn_output.weight",
            "blk.0.attn_q_norm.weight", "blk.0.attn_k_norm.weight",
            "blk.0.ffn_norm.weight", "blk.0.ffn_up.weight", "blk.0.ffn_down.weight"
        ]
        let configuration = try MLXGGUFEmbeddedAssets.apertusConfiguration(
            metadata: metadata,
            prefix: "apertus",
            tensorNames: tensorNames
        )

        XCTAssertEqual(configuration["model_type"] as? String, "apertus")
        XCTAssertEqual(configuration["rope_theta"] as? Float, 4_000_000)
        XCTAssertEqual(configuration["tie_word_embeddings"] as? Bool, false)
        XCTAssertEqual(
            MLXGGUFWeightNameNormalizer.normalizedName(
                "blk.7.attn_norm.weight",
                modelType: "apertus"
            ),
            "model.layers.7.attention_layernorm.weight"
        )
        XCTAssertEqual(
            MLXGGUFWeightNameNormalizer.normalizedName(
                "blk.7.ffn_norm.weight",
                modelType: "apertus"
            ),
            "model.layers.7.feedforward_layernorm.weight"
        )
    }

    func testConverterNormOffsetIncludesGemmaStyleNormsOnly() {
        XCTAssertTrue(
            MLXGGUFModelLoader.isConverterShiftedNorm("blk.0.attn_norm.weight")
        )
        XCTAssertTrue(
            MLXGGUFModelLoader.isConverterShiftedNorm("blk.0.attn_q_norm.weight")
        )
        XCTAssertFalse(
            MLXGGUFModelLoader.isConverterShiftedNorm("blk.0.ssm_norm.weight")
        )
        XCTAssertFalse(
            MLXGGUFModelLoader.isConverterShiftedNorm("blk.0.attn_q.weight")
        )
    }

    private func gemma4Metadata(
        hiddenLayers: Int,
        sharedKVLayers: Int
    ) -> [String: MLXGGUFMetadataValue] {
        [
            "gemma4.embedding_length": .uint32(32),
            "gemma4.block_count": .uint32(UInt32(hiddenLayers)),
            "gemma4.attention.head_count": .uint32(4),
            "gemma4.attention.head_count_kv": .uint32(1),
            "gemma4.attention.key_length_swa": .uint32(8),
            "gemma4.attention.key_length": .uint32(16),
            "gemma4.attention.layer_norm_rms_epsilon": .float32(1e-6),
            "gemma4.attention.shared_kv_layers": .uint32(UInt32(sharedKVLayers)),
            "gemma4.attention.sliding_window": .uint32(512),
            "gemma4.attention.sliding_window_pattern": .array(
                (0..<hiddenLayers).map { .boolean($0.isMultiple(of: 2)) }
            ),
            "gemma4.embedding_length_per_layer_input": .uint32(4),
            "gemma4.context_length": .uint32(4096),
            "gemma4.final_logit_softcapping": .float32(30),
            "gemma4.rope.dimension_count": .uint32(4),
            "gemma4.rope.freq_base": .float32(1_000_000),
            "gemma4.rope.freq_base_swa": .float32(10_000),
            "tokenizer.ggml.tokens": .array([
                .string("<pad>"), .string("<eos>"), .string("<bos>")
            ])
        ]
    }

    private var gemma4TensorNames: [String] {
        [
            "token_embd.weight",
            "output_norm.weight",
            "per_layer_token_embd.weight",
            "per_layer_model_proj.weight",
            "per_layer_proj_norm.weight",
            "blk.0.attn_norm.weight",
            "blk.0.attn_q.weight",
            "blk.0.attn_k.weight",
            "blk.0.attn_v.weight",
            "blk.0.attn_output.weight",
            "blk.0.attn_q_norm.weight",
            "blk.0.attn_k_norm.weight",
            "blk.0.post_attention_norm.weight",
            "blk.0.ffn_norm.weight",
            "blk.0.ffn_gate.weight",
            "blk.0.ffn_down.weight",
            "blk.0.ffn_up.weight",
            "blk.0.post_ffw_norm.weight",
            "blk.0.inp_gate.weight",
            "blk.0.proj.weight",
            "blk.0.post_norm.weight",
            "blk.0.layer_output_scale.weight"
        ]
    }

    private var gemma3TensorNames: [String] {
        [
            "token_embd.weight",
            "output_norm.weight",
            "blk.0.attn_norm.weight",
            "blk.0.attn_q.weight",
            "blk.0.attn_k.weight",
            "blk.0.attn_v.weight",
            "blk.0.attn_output.weight",
            "blk.0.attn_q_norm.weight",
            "blk.0.attn_k_norm.weight",
            "blk.0.post_attention_norm.weight",
            "blk.0.ffn_norm.weight",
            "blk.0.ffn_gate.weight",
            "blk.0.ffn_down.weight",
            "blk.0.ffn_up.weight",
            "blk.0.post_ffw_norm.weight"
        ]
    }

    private func dequantizedWeight(
        named name: String,
        in weights: [String: MLXArray],
        groupSize: Int
    ) -> MLXArray? {
        guard let weight = weights[name + ".weight"],
              let scales = weights[name + ".scales"],
              let biases = weights[name + ".biases"],
              scales.dim(-1) > 0,
              groupSize >= 32,
              groupSize.isMultiple(of: 32) else { return nil }
        let packedWidthPerGroup = groupSize / 32
        guard weight.dim(-1).isMultiple(of: scales.dim(-1) * packedWidthPerGroup)
        else { return nil }
        let bits = weight.dim(-1) / scales.dim(-1) / packedWidthPerGroup
        guard bits == 4 || bits == 8 else { return nil }
        return dequantized(
            weight,
            scales: scales,
            biases: biases,
            groupSize: groupSize,
            bits: bits
        )
    }

    private func layerTrace(
        container: ModelContainer,
        tokens: [Int]
    ) async throws -> LayerTraceSnapshot {
        try await container.perform(values: tokens) { context, tokens in
            guard let target = context.model as? any DFlashTargetModel else {
                throw MLXGGUFLoaderError.invalidTensor("DFlash layer trace target")
            }
            let layerIDs = Array(0..<target.dflashLayerCount)
            let output = target.dflashForward(
                MLXArray(tokens).reshaped([1, -1]),
                cache: nil,
                captureLayerIDs: layerIDs,
                captureRollback: false
            )
            let lastLogits = output.logits[0, -1].asType(.float32)
            let hidden = output.hiddenStates.asType(.float32)
            eval(lastLogits, hidden)
            return LayerTraceSnapshot(
                hiddenValues: hidden.asArray(Float.self),
                lastLogits: lastLogits.asArray(Float.self),
                sequenceLength: hidden.dim(1),
                hiddenSize: target.dflashHiddenSize,
                layerCount: target.dflashLayerCount
            )
        }
    }

    private func normalizedMeanAbsoluteError(
        _ reference: MLXArray,
        _ candidate: MLXArray
    ) -> Float {
        let reference = reference.asType(.float32)
        let candidate = candidate.asType(.float32)
        let absoluteError = (reference - candidate).abs().mean()
        let referenceMagnitude = reference.abs().mean()
        eval(absoluteError, referenceMagnitude)
        return absoluteError.item(Float.self)
            / max(referenceMagnitude.item(Float.self), 1e-8)
    }

}
