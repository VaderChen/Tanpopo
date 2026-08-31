import Foundation
import MLX

enum MLXGGUFMetalQuantizer {
    private static let mxfp4Kernel = MLXFast.metalKernel(
        name: "tanpopo_gguf_pack_mxfp4",
        inputNames: ["raw"],
        outputNames: ["wq", "scales"],
        source: """
            uint block = thread_position_in_grid.x;
            uint raw_offset = block * 17;
            scales[block] = raw[raw_offset];

            for (uint word = 0; word < 4; ++word) {
                uint result = 0;
                for (uint index = 0; index < 8; ++index) {
                    uint source_index = word * 8 + index;
                    uint packed_index = source_index < 16
                        ? source_index
                        : source_index - 16;
                    uint shift = source_index < 16 ? 0 : 4;
                    uint value = (uint(raw[raw_offset + 1 + packed_index]) >> shift) & 15;
                    result |= value << (index * 4);
                }
                wq[block * 4 + word] = result;
            }
        """
    )

    private static let preservedQuantizedKernel = MLXFast.metalKernel(
        name: "tanpopo_gguf_pack_quantized",
        inputNames: ["raw"],
        outputNames: ["wq", "scales", "biases"],
        source: """
            uint block = thread_position_in_grid.x;
            if (sourceType == 12) {
                // Q4_K：256 元素 super-block（144 bytes）。每個 super-block 由 8 個
                // 32 元素 sub-block 組成，各自帶 6-bit 的 scale 與 min，正好對應
                // MLX affine group 32 的一組 (scale, bias)：
                //   w = (d * sc) * q - (dmin * m)  ≡  w = q * scale + bias
                // 量化值原樣保留，只改寫 scale 的階層表示，不做第二次量化。
                uint raw_offset = block * 144;
                uint scale_base = raw_offset + 4;
                uint quant_base = raw_offset + 16;
                float d = load_f16(raw, raw_offset);
                float dmin = load_f16(raw, raw_offset + 2);
                for (uint j = 0; j < 8; ++j) {
                    uint sc;
                    uint m;
                    if (j < 4) {
                        sc = uint(raw[scale_base + j]) & 63u;
                        m = uint(raw[scale_base + j + 4]) & 63u;
                    } else {
                        sc = (uint(raw[scale_base + j + 4]) & 15u)
                            | ((uint(raw[scale_base + j - 4]) >> 6) << 4);
                        m = (uint(raw[scale_base + j + 4]) >> 4)
                            | ((uint(raw[scale_base + j]) >> 6) << 4);
                    }
                    scales[block * 8 + j] = bfloat(d * float(sc));
                    biases[block * 8 + j] = bfloat(-dmin * float(m));
                }
                for (uint k = 0; k < 4; ++k) {
                    for (uint nibble = 0; nibble < 2; ++nibble) {
                        uint sub = k * 2 + nibble;
                        for (uint word = 0; word < 4; ++word) {
                            uint result = 0;
                            for (uint idx = 0; idx < 8; ++idx) {
                                uchar byte = raw[quant_base + k * 32 + word * 8 + idx];
                                uint value = nibble == 0
                                    ? (uint(byte) & 15u)
                                    : (uint(byte) >> 4);
                                result |= value << (idx * 4);
                            }
                            wq[block * 32 + sub * 4 + word] = result;
                        }
                    }
                }
                return;
            }
            uint source_bytes = sourceType == 8 ? 34 : sourceType == 3 ? 20 : 18;
            uint raw_offset = block * source_bytes;
            float scale = load_f16(raw, raw_offset);
            scales[block] = bfloat(scale);
            if (sourceType == 3) {
                biases[block] = bfloat(load_f16(raw, raw_offset + 2));
                for (uint word = 0; word < 4; ++word) {
                    uint result = 0;
                    for (uint byte_index = 0; byte_index < 4; ++byte_index) {
                        uint packed_index = word * 4 + byte_index;
                        uint pair = packed_index % 8;
                        uchar first = raw[raw_offset + 4 + pair * 2];
                        uchar second = raw[raw_offset + 4 + pair * 2 + 1];
                        uint packed = packed_index < 8
                            ? uint(first & 15) | (uint(second & 15) << 4)
                            : uint(first >> 4) | (uint(second >> 4) << 4);
                        result |= packed << (byte_index * 8);
                    }
                    wq[block * 4 + word] = result;
                }
            } else if (sourceType == 2) {
                biases[block] = bfloat(-8.0f * scale);
                for (uint word = 0; word < 4; ++word) {
                    uint result = 0;
                    for (uint byte_index = 0; byte_index < 4; ++byte_index) {
                        uint packed_index = word * 4 + byte_index;
                        uint pair = packed_index % 8;
                        uchar first = raw[raw_offset + 2 + pair * 2];
                        uchar second = raw[raw_offset + 2 + pair * 2 + 1];
                        uint packed = packed_index < 8
                            ? uint(first & 15) | (uint(second & 15) << 4)
                            : uint(first >> 4) | (uint(second >> 4) << 4);
                        result |= packed << (byte_index * 8);
                    }
                    wq[block * 4 + word] = result;
                }
            } else {
                biases[block] = bfloat(-128.0f * scale);
                for (uint word = 0; word < 8; ++word) {
                    uint result = 0;
                    for (uint byte_index = 0; byte_index < 4; ++byte_index) {
                        uint value = uint(raw[raw_offset + 2 + word * 4 + byte_index]) ^ 128u;
                        result |= value << (byte_index * 8);
                    }
                    wq[block * 8 + word] = result;
                }
            }
        """,
        header: """
            uint load_u16(device const uchar *raw, uint offset) {
                return uint(raw[offset]) | (uint(raw[offset + 1]) << 8);
            }

            float load_f16(device const uchar *raw, uint offset) {
                return float(as_type<half>((ushort) load_u16(raw, offset)));
            }

        """
    )

    private static let kernel = MLXFast.metalKernel(
        name: "tanpopo_gguf_direct_quantize",
        inputNames: ["raw", "iq3sGrid"],
        outputNames: ["wq", "scales", "biases"],
        source: """
            uint group_index = thread_position_in_grid.x;
            float values[64];
            uint group_start = group_index * targetGroupSize;
            values[0] = source_value(raw, iq3sGrid, sourceType, group_start);
            float minimum = values[0];
            float maximum = values[0];
            for (uint index = 1; index < targetGroupSize; ++index) {
                values[index] = source_value(raw, iq3sGrid, sourceType, group_start + index);
                minimum = min(minimum, values[index]);
                maximum = max(maximum, values[index]);
            }

            uint bins = (1u << bits) - 1u;
            float scale = max((maximum - minimum) / float(bins), 1e-7f);
            bool use_minimum_as_edge = abs(minimum) > abs(maximum);
            scale = use_minimum_as_edge ? scale : -scale;
            float edge = use_minimum_as_edge ? minimum : maximum;
            float initial_quantized_edge = round(edge / scale);
            float bias = initial_quantized_edge == 0
                ? 0
                : edge;
            scale = initial_quantized_edge == 0
                ? scale
                : edge / initial_quantized_edge;

            scales[group_index] = bfloat(scale);
            biases[group_index] = bfloat(bias);
            for (uint word = 0; word < words_per_group(bits, targetGroupSize); ++word) {
                wq[group_index * words_per_group(bits, targetGroupSize) + word] =
                    quantized_group_word(values, scale, bias, bits, word);
            }
        """,
        header: """
            uint load_u16(device const uchar *raw, uint offset) {
                return uint(raw[offset]) | (uint(raw[offset + 1]) << 8);
            }

            uint load_u32(device const uchar *raw, uint offset) {
                return uint(raw[offset])
                    | (uint(raw[offset + 1]) << 8)
                    | (uint(raw[offset + 2]) << 16)
                    | (uint(raw[offset + 3]) << 24);
            }

            float load_f16(device const uchar *raw, uint offset) {
                return float(as_type<half>((ushort) load_u16(raw, offset)));
            }

            constant short tanpopo_iq4nl_values[16] = {
                -127, -104, -83, -65, -49, -35, -22, -10,
                1, 13, 25, 38, 53, 69, 89, 113
            };

            int signed_byte(uchar value) {
                int result = int(value);
                return result >= 128 ? result - 256 : result;
            }

            uint qk_scale(device const uchar *raw, uint offset, uint index) {
                if (index < 4) {
                    return uint(raw[offset + index] & 63);
                }
                return uint((raw[offset + index + 4] & 15)
                    | ((raw[offset + index - 4] >> 6) << 4));
            }

            uint qk_minimum(device const uchar *raw, uint offset, uint index) {
                if (index < 4) {
                    return uint(raw[offset + index + 4] & 63);
                }
                return uint((raw[offset + index + 4] >> 4)
                    | ((raw[offset + index] >> 6) << 4));
            }

            int q3_scale(
                device const uchar *raw,
                uint offset,
                uint index
            ) {
                uint first = load_u32(raw, offset);
                uint second = load_u32(raw, offset + 4);
                uint packed = load_u32(raw, offset + 8);
                uint mask1 = 0x03030303;
                uint mask2 = 0x0f0f0f0f;
                uint third = ((first >> 4) & mask2)
                    | (((packed >> 4) & mask1) << 4);
                uint fourth = ((second >> 4) & mask2)
                    | (((packed >> 6) & mask1) << 4);
                first = (first & mask2) | (((packed >> 0) & mask1) << 4);
                second = (second & mask2) | (((packed >> 2) & mask1) << 4);
                uint word;
                switch (index / 4) {
                case 0: word = first; break;
                case 1: word = second; break;
                case 2: word = third; break;
                default: word = fourth; break;
                }
                return signed_byte((uchar) (word >> ((index % 4) * 8))) - 32;
            }

            float source_value(
                device const uchar *raw,
                device const uint *iq3s_grid,
                uint source_type,
                uint element
            ) {
                uint block;
                uint source_index;
                uint block_offset;
                if (source_type == 2 || source_type == 3 || source_type == 8) {
                    uint bytes_per_block = source_type == 8 ? 34 : source_type == 3 ? 20 : 18;
                    uint block_elements = 32;
                    block = element / block_elements;
                    source_index = element % block_elements;
                    block_offset = block * bytes_per_block;
                    float scale = load_f16(raw, block_offset);
                    if (source_type == 8) {
                        int quantized = signed_byte(raw[block_offset + 2 + source_index]);
                        return scale * float(quantized);
                    }
                    uchar packed = raw[block_offset + (source_type == 3 ? 4 : 2)
                        + source_index % 16];
                    if (source_type == 2) {
                        uint quantized = source_index < 16 ? uint(packed & 15) : uint(packed >> 4);
                        return scale * float(int(quantized) - 8);
                    }
                    uint quantized = source_index < 16 ? uint(packed & 15) : uint(packed >> 4);
                    return scale * float(quantized) + load_f16(raw, block_offset + 2);
                }
                if (source_type == 41 || source_type == 42) {
                    uint block_elements = source_type == 41 ? 128 : 64;
                    block = element / block_elements;
                    source_index = element % block_elements;
                    block_offset = block * 18;
                    float scale = load_f16(raw, block_offset);
                    if (source_type == 41) {
                        uint quantized = (raw[block_offset + 2 + source_index / 8]
                            >> (source_index % 8)) & 1;
                        return quantized == 0 ? -scale : scale;
                    }
                    uint quantized = (raw[block_offset + 2 + source_index / 4]
                        >> ((source_index % 4) * 2)) & 3;
                    return float(int(quantized) - 1) * scale;
                }

                if (source_type == 20) {
                    block = element / 32;
                    source_index = element % 32;
                    block_offset = block * 18;
                    uchar packed = raw[block_offset + 2 + source_index % 16];
                    uint quantized = source_index < 16
                        ? uint(packed & 15)
                        : uint(packed >> 4);
                    return load_f16(raw, block_offset)
                        * float(tanpopo_iq4nl_values[quantized]);
                }

                if (source_type == 21) {
                    block = element / 256;
                    source_index = element % 256;
                    block_offset = block * 110;
                    uint block_of_32 = source_index / 32;
                    uint position_32 = source_index % 32;
                    uint group_half = position_32 / 16;
                    uint position_16 = position_32 % 16;
                    uint grid_slot = position_16 / 4;
                    uint position_4 = position_16 % 4;
                    uint high_nibble = (uint(raw[block_offset + 66 + block_of_32])
                        >> (4 * group_half)) & 15;
                    uint grid_index = uint(raw[
                        block_offset + 2 + block_of_32 * 8 + group_half * 4 + grid_slot
                    ]) | (((high_nibble >> grid_slot) & 1) << 8);
                    uint grid_word = iq3s_grid[grid_index];
                    uint magnitude = (grid_word >> (8 * position_4)) & 255;
                    uint sign_byte = uint(raw[
                        block_offset + 74 + block_of_32 * 4 + group_half * 2 + grid_slot / 2
                    ]);
                    uint sign_bit = 1 << (position_16 % 8);
                    float sign = (sign_byte & sign_bit) == 0 ? 1.0f : -1.0f;
                    uint packed_scale = uint(raw[block_offset + 106 + block_of_32 / 2]);
                    uint source_scale = (packed_scale >> (4 * (block_of_32 % 2))) & 15;
                    return load_f16(raw, block_offset)
                        * float(1 + 2 * source_scale)
                        * float(magnitude)
                        * sign;
                }

                if (source_type == 23) {
                    block = element / 256;
                    source_index = element % 256;
                    block_offset = block * 136;
                    uint subblock = source_index / 32;
                    uint position = source_index % 32;
                    uint scale_low = (uint(raw[block_offset + 4 + subblock / 2])
                        >> (4 * (subblock % 2))) & 15;
                    uint scale_high = (load_u16(raw, block_offset + 2)
                        >> (2 * subblock)) & 3;
                    int scale = int(scale_low | (scale_high << 4)) - 32;
                    uchar packed = raw[block_offset + 8 + subblock * 16 + position % 16];
                    uint quantized = position < 16
                        ? uint(packed & 15)
                        : uint(packed >> 4);
                    return load_f16(raw, block_offset)
                        * float(scale)
                        * float(tanpopo_iq4nl_values[quantized]);
                }

                block = element / 256;
                source_index = element % 256;
                block_offset = block * (source_type == 10 ? 84
                    : source_type == 11 ? 110
                    : source_type == 12 ? 144
                    : source_type == 13 ? 176 : 210);
                uint block_half = source_index / 128;
                uint half_index = source_index % 128;
                if (source_type == 10) {
                    uint chunk = half_index / 32;
                    uint position = half_index % 32;
                    uint subblock = position / 16;
                    uint scale_index = block_half * 8 + chunk * 2 + subblock;
                    uchar scale_byte = raw[block_offset + 4 + scale_index];
                    uint quantized = (raw[block_offset + 20 + block_half * 32 + position]
                        >> (chunk * 2)) & 3;
                    return load_f16(raw, block_offset) * float(scale_byte & 15)
                        * float(quantized)
                        - load_f16(raw, block_offset + 2) * float(scale_byte >> 4);
                }
                if (source_type == 11) {
                    uint chunk = half_index / 32;
                    uint position = half_index % 32;
                    uint subblock = position / 16;
                    uint scale_index = block_half * 8 + chunk * 2 + subblock;
                    uint high_mask = 1 << (block_half * 4 + chunk);
                    uint quantized = (raw[block_offset + 32 + block_half * 32 + position]
                        >> (chunk * 2)) & 3;
                    uint high = (raw[block_offset + position] & high_mask) == 0 ? 4 : 0;
                    int source_scale = q3_scale(raw, block_offset + 96, scale_index);
                    return load_f16(raw, block_offset + 108) * float(source_scale)
                        * float(int(quantized) - int(high));
                }
                if (source_type == 12 || source_type == 13) {
                    uint segment = source_index / 64;
                    bool upper = (source_index % 64) >= 32;
                    uint position = source_index % 32;
                    uint scale_index = segment * 2 + (upper ? 1 : 0);
                    uint quantized;
                    if (source_type == 12) {
                        quantized = upper
                            ? uint(raw[block_offset + 16 + segment * 32 + position] >> 4)
                            : uint(raw[block_offset + 16 + segment * 32 + position] & 15);
                    } else {
                        uchar high = raw[block_offset + 16 + position];
                        uint high_bit = upper ? 2 << (segment * 2) : 1 << (segment * 2);
                        quantized = upper
                            ? uint(raw[block_offset + 48 + segment * 32 + position] >> 4)
                            : uint(raw[block_offset + 48 + segment * 32 + position] & 15);
                        quantized += (high & high_bit) == 0 ? 0 : 16;
                    }
                    uint source_scale = qk_scale(raw, block_offset + 4, scale_index);
                    uint source_minimum = qk_minimum(raw, block_offset + 4, scale_index);
                    return load_f16(raw, block_offset) * float(source_scale)
                        * float(quantized)
                        - load_f16(raw, block_offset + 2) * float(source_minimum);
                }

                uint plane = half_index / 32;
                uint position = half_index % 32;
                uint low_offset = block_offset + block_half * 64;
                uchar high = raw[block_offset + 128 + block_half * 32 + position];
                uint quantized;
                switch (plane) {
                case 0:
                    quantized = uint(raw[low_offset + position] & 15)
                        | (uint(high & 3) << 4);
                    break;
                case 1:
                    quantized = uint(raw[low_offset + position + 32] & 15)
                        | (uint((high >> 2) & 3) << 4);
                    break;
                case 2:
                    quantized = uint(raw[low_offset + position] >> 4)
                        | (uint((high >> 4) & 3) << 4);
                    break;
                default:
                    quantized = uint(raw[low_offset + position + 32] >> 4)
                        | (uint((high >> 6) & 3) << 4);
                    break;
                }
                int source_scale = signed_byte(
                    raw[block_offset + 192 + block_half * 8 + plane * 2 + position / 16]
                );
                return load_f16(raw, block_offset + 208) * float(source_scale)
                    * float(int(quantized) - 32);
            }

            uint quantized_value(float value, float scale, float bias, uint bins) {
                float quantized = round((value - bias) / scale);
                quantized = min(max(quantized, 0.0f), float(bins));
                return uint(quantized);
            }

            uint pack_factor(uint bits) {
                return bits == 4 ? 8 : 4;
            }

            uint words_per_group(uint bits, uint group_size) {
                return group_size * bits / 32;
            }

            uint quantized_bits(uint value, uint bits, uint index) {
                return value << (bits * (index % pack_factor(bits)));
            }

            uint quantized_group_word(
                float values[64],
                float scale,
                float bias,
                uint bits,
                uint word_index
            ) {
                uint result = 0;
                uint values_per_word = 32 / bits;
                uint start = word_index * values_per_word;
                uint bins = (1u << bits) - 1u;
                for (uint index = 0; index < values_per_word; ++index) {
                    uint quantized = quantized_value(
                        values[start + index],
                        scale,
                        bias,
                        bins
                    );
                    result |= quantized_bits(quantized, bits, index);
                }
                return result;
            }

        """
    )

    static func quantize(
        raw: Data,
        sourceType: UInt32,
        targetBits: Int,
        sourceShape: [Int],
        targetGroupSize: Int,
        targetWeightShape: [Int],
        targetScaleShape: [Int]
    ) throws -> (wq: MLXArray, scales: MLXArray, biases: MLXArray) {
        guard targetGroupSize == 32 || targetGroupSize == 64,
              targetBits == 4 || targetBits == 8 else {
            throw MLXGGUFLoaderError.invalidTensor("量化群組設定")
        }
        let elementCount = sourceShape.reduce(1, *)
        guard elementCount > 0, elementCount % targetGroupSize == 0 else {
            throw MLXGGUFLoaderError.invalidTensor("量化群組設定")
        }
        let groupCount = elementCount / targetGroupSize
        let rawArray = MLXArray(raw, [raw.count], dtype: .uint8)
        let iq3SGridArray = MLXArray(
            MLXGGUFIQ3SGrid.values,
            [MLXGGUFIQ3SGrid.values.count]
        )
        let output = kernel(
            [rawArray, iq3SGridArray],
            template: [
                ("sourceType", Int(sourceType)),
                ("bits", targetBits),
                ("targetGroupSize", targetGroupSize)
            ],
            grid: (groupCount, 1, 1),
            threadGroup: (min(groupCount, 64), 1, 1),
            outputShapes: [targetWeightShape, targetScaleShape, targetScaleShape],
            outputDTypes: [.uint32, .bfloat16, .bfloat16]
        )
        try checkedEval(output)
        return (output[0], output[1], output[2])
    }

    static func packPreserved(
        raw: Data,
        sourceType: UInt32,
        sourceShape: [Int],
        targetWeightShape: [Int],
        targetScaleShape: [Int]
    ) throws -> (wq: MLXArray, scales: MLXArray, biases: MLXArray) {
        guard sourceType == 2 || sourceType == 3 || sourceType == 8 || sourceType == 12 else {
            throw MLXGGUFLoaderError.invalidTensor("GGUF type (sourceType)")
        }
        // Q4_K 的 super-block 是 256 元素；其餘保留型別是 32 元素 block。
        let elementsPerBlock = sourceType == 12 ? 256 : 32
        let elementCount = sourceShape.reduce(1, *)
        guard elementCount > 0, elementCount % elementsPerBlock == 0 else {
            throw MLXGGUFLoaderError.invalidTensor("GGUF type (sourceType)")
        }
        let blockCount = elementCount / elementsPerBlock
        let bytesPerBlock = sourceType == 12
            ? 144
            : sourceType == 8 ? 34 : sourceType == 3 ? 20 : 18
        guard raw.count == blockCount * bytesPerBlock else {
            throw MLXGGUFLoaderError.invalidTensor("GGUF type (sourceType)")
        }

        let rawArray = MLXArray(raw, [raw.count], dtype: .uint8)
        let output = preservedQuantizedKernel(
            [rawArray],
            template: [("sourceType", Int(sourceType))],
            grid: (blockCount, 1, 1),
            threadGroup: (min(blockCount, 64), 1, 1),
            outputShapes: [targetWeightShape, targetScaleShape, targetScaleShape],
            // scale／bias 維持 bfloat16。改用 fp16 雖能把 scale 的相對誤差由
            // 約 0.54% 降到 0.068%，但實測 beta1／beta2 的速度掉 33～44%——
            // 模型權重是 bfloat16，scales 同型別才走 MLX 的原生 kernel，
            // 換成 fp16 會多一次型別轉換。端到端精度改善又幾乎為零。
            outputDTypes: [.uint32, .bfloat16, .bfloat16]
        )
        try checkedEval(output)
        return (output[0], output[1], output[2])
    }

    static func packMXFP4(
        raw: Data,
        sourceShape: [Int],
        targetWeightShape: [Int],
        targetScaleShape: [Int]
    ) throws -> (wq: MLXArray, scales: MLXArray) {
        let elementCount = sourceShape.reduce(1, *)
        guard elementCount > 0, elementCount % 32 == 0 else {
            throw MLXGGUFLoaderError.invalidTensor("MXFP4 量化群組設定")
        }
        let blockCount = elementCount / 32
        guard raw.count == blockCount * 17 else {
            throw MLXGGUFLoaderError.invalidTensor("MXFP4 權重資料大小")
        }

        let rawArray = MLXArray(raw, [raw.count], dtype: .uint8)
        let output = mxfp4Kernel(
            [rawArray],
            grid: (blockCount, 1, 1),
            threadGroup: (min(blockCount, 64), 1, 1),
            outputShapes: [targetWeightShape, targetScaleShape],
            outputDTypes: [.uint32, .uint8]
        )
        try checkedEval(output)
        return (output[0], output[1])
    }
}
