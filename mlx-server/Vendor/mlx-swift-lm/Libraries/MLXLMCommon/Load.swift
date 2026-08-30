// Copyright © 2024 Apple Inc.

import Foundation
import MLX
import MLXNN

/// Load model weights.
///
/// This is typically called via ``GenericModelFactory/load(from:using:configuration:useLatest:progressHandler:)``.
/// This function loads all `safetensor` files in the given `modelDirectory`,
/// calls ``BaseLanguageModel/sanitize(weights:metadata:)`` to allow per-model preprocessing,
/// applies optional quantization, and
/// updates the model with the weights.
public func loadWeights(
    modelDirectory: URL, model: BaseLanguageModel,
    quantization: BaseConfiguration.Quantization? = nil,
    perLayerQuantization: BaseConfiguration.PerLayerQuantization? = nil
) throws {
    // load the weights and collect metadata from the first safetensor file
    var weights = [String: MLXArray]()
    var metadata = [String: String]()
    let enumerator = FileManager.default.enumerator(
        at: modelDirectory, includingPropertiesForKeys: nil)!
    var weightURLs = [URL]()
    for case let url as URL in enumerator where url.pathExtension == "safetensors" {
        weightURLs.append(url)
    }
    weightURLs.sort { $0.path < $1.path }
    let fileByteCounts = weightURLs.map { url in
        Int64((try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0)
    }
    let totalFileBytes = fileByteCounts.reduce(Int64(0), +)
    var completedFileBytes: Int64 = 0
    ModelWeightLoadingContext.progressHandler?(0, 100)
    for (index, url) in weightURLs.enumerated() {
            let (w, m): ([String: MLXArray], [String: String])
            switch ModelWeightLoadingContext.mode {
            case .eager:
                (w, m) = try loadArraysAndMetadata(url: url)
            case .memoryMapped:
                (w, m) = try MemoryMappedSafetensors.loadArraysAndMetadata(from: url)
            }
            for (key, value) in w {
                weights[key] = value
            }
            if metadata.isEmpty {
                metadata = m
            }
            completedFileBytes += fileByteCounts[index]
            let fileProgress: Int64
            if totalFileBytes > 0 {
                fileProgress = min(55, completedFileBytes * 55 / totalFileBytes)
            } else {
                fileProgress = Int64((index + 1) * 55 / max(1, weightURLs.count))
            }
            ModelWeightLoadingContext.progressHandler?(fileProgress, 100)
    }

    // per-model cleanup (models can inspect metadata to customize behavior)
    weights = model.sanitize(weights: weights, metadata: metadata)
    ModelWeightLoadingContext.progressHandler?(65, 100)

    // quantize if needed
    if quantization != nil || perLayerQuantization != nil {
        quantize(model: model) { path, module in
            if weights["\(path).scales"] != nil {
                if let perLayerQuantization {
                    return perLayerQuantization.quantization(layer: path)?.asTuple
                } else {
                    return quantization?.asTuple
                }
            } else {
                return nil
            }
        }
    }
    ModelWeightLoadingContext.progressHandler?(75, 100)

    // apply the loaded weights
    let parameters = ModuleParameters.unflattened(weights)
    try model.update(parameters: parameters, verify: [.all])
    ModelWeightLoadingContext.progressHandler?(90, 100)

    eval(model)
    ModelWeightLoadingContext.progressHandler?(100, 100)
}
