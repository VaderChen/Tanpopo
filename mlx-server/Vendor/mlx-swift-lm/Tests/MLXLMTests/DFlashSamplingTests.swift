// Copyright © 2026 Tanpopo contributors.

import MLX
import Testing

@testable import MLXLMCommon

@Suite("DFlash sampling distribution")
struct DFlashSamplingTests {
    @Test("Top-k keeps only the requested candidates")
    func topKKeepsOnlyRequestedCandidates() {
        let logits = MLXArray([0.0 as Float, 3.0, 2.0, 1.0])[.newAxis, .ellipsis]
        let probabilities = dflashSamplingProbabilities(
            logits: logits,
            parameters: .init(temperature: 1, topP: 1, topK: 2, minP: 0)
        )
        eval(probabilities)

        let values = probabilities.asArray(Float.self)
        #expect(values[0] == 0)
        #expect(values[1] > 0)
        #expect(values[2] > 0)
        #expect(values[3] == 0)
        #expect(abs(values.reduce(0, +) - 1) < 1e-6)
    }

    @Test("Min-p removes tokens below the relative threshold")
    func minPUsesMaximumProbabilityAsThreshold() {
        let source = MLXArray([0.6 as Float, 0.25, 0.1, 0.05])[.newAxis, .ellipsis]
        let probabilities = dflashSamplingProbabilities(
            logits: log(source),
            parameters: .init(temperature: 1, topP: 1, topK: 0, minP: 0.5)
        )
        eval(probabilities)

        let values = probabilities.asArray(Float.self)
        #expect(abs(values[0] - 1) < 1e-6)
        #expect(values[1] == 0)
        #expect(values[2] == 0)
        #expect(values[3] == 0)
    }

    @Test("Temperature matches the ordinary categorical distribution")
    func temperatureMatchesCategoricalDistribution() {
        let logits = MLXArray([0.0 as Float, 0.5, 1.0])[.newAxis, .ellipsis]
        let actual = dflashSamplingProbabilities(
            logits: logits,
            parameters: .init(temperature: 2, topP: 1, topK: 0, minP: 0)
        )
        let expected = softmax(logits * 0.5, axis: -1)
        eval(actual, expected)

        for (actual, expected) in zip(
            actual.asArray(Float.self), expected.asArray(Float.self))
        {
            #expect(abs(actual - expected) < 1e-6)
        }
    }

    @Test("BFloat16 logits use a stable Float32 probability path")
    func bfloat16UsesFloat32ProbabilityPath() {
        let logits = MLXArray([0.0 as Float, 1.0, 2.0])[
            .newAxis, .ellipsis
        ].asType(.bfloat16)
        let probabilities = dflashSamplingProbabilities(
            logits: logits,
            parameters: .init(temperature: 1, topP: 1, topK: 0, minP: 0)
        )

        #expect(probabilities.dtype == .float32)
        #expect(probabilities.shape == [1, 3])
    }
}
