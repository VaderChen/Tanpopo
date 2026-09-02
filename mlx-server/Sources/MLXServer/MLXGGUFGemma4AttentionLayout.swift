import Foundation

/// Gemma 4 的注意力配置按層型別還原，不以模型名稱或參數量推測。
/// 來源 GGUF metadata 與既有 Fast GGUF 權重共用同一份可表示性檢查。
struct MLXGGUFGemma4AttentionLayout {
    let slidingKVHeads: Int
    let globalKVHeads: Int
    let globalKeyEqualsValue: Bool

    static func resolve(
        layerTypes: [String],
        headCounts: [Int],
        globalValueProjectionPresence: [Bool]
    ) throws -> Self {
        guard !layerTypes.isEmpty,
              layerTypes.count == headCounts.count,
              headCounts.allSatisfy({ $0 > 0 }),
              layerTypes.allSatisfy({
                  $0 == "sliding_attention" || $0 == "full_attention"
              }) else {
            throw MLXGGUFLoaderError.invalidSize
        }

        let slidingCounts = Set(layerTypes.indices.compactMap { index in
            layerTypes[index] == "sliding_attention" ? headCounts[index] : nil
        })
        let globalCounts = Set(layerTypes.indices.compactMap { index in
            layerTypes[index] == "full_attention" ? headCounts[index] : nil
        })
        guard slidingCounts.count <= 1, globalCounts.count <= 1,
              Set(globalValueProjectionPresence).count <= 1 else {
            throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                architecture: "gemma4",
                reason: "相同注意力型別的逐層 KV 頭數或 K/V 共用方式不一致"
            )
        }
        let slidingHeads = slidingCounts.first ?? headCounts[0]
        let globalHeads = globalCounts.first ?? slidingHeads
        let keyEqualsValue = globalValueProjectionPresence.first == false

        // 目前 Runtime 只在全域 K=V 層使用 num_global_key_value_heads。
        // 無法精確表示的架構應明確拒絕，不可退回 Query 頭數後載入錯誤形狀。
        guard keyEqualsValue || globalHeads == slidingHeads else {
            throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                architecture: "gemma4",
                reason: "不同全域 KV 頭數搭配獨立 V 投影的布局尚未受 Runtime 支援"
            )
        }
        return Self(
            slidingKVHeads: slidingHeads,
            globalKVHeads: globalHeads,
            globalKeyEqualsValue: keyEqualsValue
        )
    }

    func applying(to configuration: [String: Any]) -> [String: Any] {
        var result = configuration
        result["num_key_value_heads"] = slidingKVHeads
        result["num_global_key_value_heads"] = globalKVHeads
        result["attention_k_eq_v"] = globalKeyEqualsValue
        return result
    }

    /// 舊 Fast GGUF 可能已保存錯誤的合成 config；原 GGUF 移除後，仍能由
    /// 投影權重的輸出列數還原 KV 頭數。量化不會改變矩陣的輸出列數。
    static func restoring(
        configuration: [String: Any],
        weightShapes: [String: [Int]]
    ) throws -> [String: Any] {
        guard let modelType = configuration["model_type"] as? String,
              ["gemma4", "gemma4_text", "gemma4_unified"].contains(modelType) else {
            return configuration
        }
        guard let layerTypes = configuration["layer_types"] as? [String],
              let layerCount = configuration["num_hidden_layers"] as? Int,
              let slidingHeadDim = configuration["head_dim"] as? Int,
              let globalHeadDim = configuration["global_head_dim"] as? Int,
              layerCount > 0, layerTypes.count == layerCount,
              slidingHeadDim > 0, globalHeadDim > 0 else {
            throw MLXGGUFLoaderError.invalidSize
        }
        let sharedLayers = configuration["num_kv_shared_layers"] as? Int ?? 0
        guard sharedLayers >= 0, sharedLayers < layerCount else {
            throw MLXGGUFLoaderError.invalidSize
        }
        var observedHeads = [Int: Int]()
        var typeHeads = [String: Int]()
        var fullValueProjections = [Bool]()
        for index in 0..<(layerCount - sharedLayers) {
            let prefix = "model.layers.\(index).self_attn."
            guard let keyShape = weightShapes[prefix + "k_proj.weight"] else {
                throw MLXGGUFLoaderError.invalidTensor(prefix + "k_proj.weight")
            }
            let isSliding = layerTypes[index] == "sliding_attention"
            let headDim = isSliding ? slidingHeadDim : globalHeadDim
            guard keyShape.count == 2, keyShape[0] > 0,
                  keyShape[0].isMultiple(of: headDim) else {
                throw MLXGGUFLoaderError.invalidTensor(prefix + "k_proj.weight")
            }
            let valueShape = weightShapes[prefix + "v_proj.weight"]
            // K/V 可能採用不同量化位元，打包後的輸入欄數可以不同；只比較
            // 不受量化影響的輸出列數，避免把混合量化誤判為架構不符。
            if let valueShape, valueShape.count != 2 || valueShape[0] != keyShape[0] {
                throw MLXGGUFLoaderError.invalidTensor(prefix + "v_proj.weight")
            }
            if isSliding && valueShape == nil {
                throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                    architecture: "gemma4",
                    reason: "局部注意力層缺少獨立 V 投影"
                )
            }
            if !isSliding {
                fullValueProjections.append(valueShape != nil)
            }
            let heads = keyShape[0] / headDim
            observedHeads[index] = heads
            typeHeads[layerTypes[index]] = heads
        }
        let headCounts = try layerTypes.indices.map { index -> Int in
            guard let heads = observedHeads[index] ?? typeHeads[layerTypes[index]] else {
                throw MLXGGUFLoaderError.unsupportedArchitectureVariant(
                    architecture: "gemma4",
                    reason: "Fast GGUF 缺少可還原 KV 頭數的注意力權重"
                )
            }
            return heads
        }
        return try resolve(
            layerTypes: layerTypes,
            headCounts: headCounts,
            globalValueProjectionPresence: fullValueProjections
        ).applying(to: configuration)
    }
}
