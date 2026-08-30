#include "MLXCoreGGUFBridge.h"

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <exception>
#include <sstream>
#include <string>

#include <mlx/allocator.h>
#include <mlx/array.h>
#include <mlx/dtype.h>

namespace {

thread_local std::string last_error;

void unpack_32_4(const uint8_t *data, int8_t *destination) {
    std::fill_n(destination, 16, 0);
    for (int index = 0; index < 16; ++index) {
        uint8_t value = data[index] & 0x0f;
        if (index % 2 != 0) {
            value <<= 4;
        }
        destination[index / 2] += value;
    }
    for (int index = 0; index < 16; ++index) {
        uint8_t value = data[index] >> 4;
        if (index % 2 != 0) {
            value <<= 4;
        }
        destination[8 + index / 2] += value;
    }
}

}  // namespace

extern "C" int tanpopo_mlx_gguf_pack_reference(
    const uint8_t *raw,
    size_t raw_count,
    uint32_t source_type,
    size_t rows,
    size_t columns,
    tanpopo_mlx_array_handle *weights_handle,
    tanpopo_mlx_array_handle *scales_handle,
    tanpopo_mlx_array_handle *biases_handle
) {
    if (raw == nullptr || weights_handle == nullptr || scales_handle == nullptr ||
        biases_handle == nullptr || rows == 0 || columns == 0) {
        last_error = "GGUF Reference Packer 收到空白輸入。";
        return 1;
    }
    if (columns % 32 != 0) {
        last_error = "GGUF Reference Packer 要求最後一維必須為 32 的倍數。";
        return 1;
    }

    try {
        size_t bytes_per_block;
        size_t weights_per_byte;
        if (source_type == 2) {  // Q4_0
            bytes_per_block = 18;
            weights_per_byte = 2;
        } else if (source_type == 3) {  // Q4_1
            bytes_per_block = 20;
            weights_per_byte = 2;
        } else if (source_type == 8) {  // Q8_0
            bytes_per_block = 34;
            weights_per_byte = 1;
        } else {
            std::ostringstream message;
            message << "GGUF Reference Packer 不支援 type " << source_type << "。";
            last_error = message.str();
            return 1;
        }

        const size_t block_count = rows * columns / 32;
        const size_t required_bytes = block_count * bytes_per_block;
        if (raw_count != required_bytes) {
            std::ostringstream message;
            message << "GGUF Reference Packer 預期 " << required_bytes
                    << " bytes，實際收到 " << raw_count << " bytes。";
            last_error = message.str();
            return 1;
        }

        mlx::core::Shape weights_shape{
            static_cast<int>(rows),
            static_cast<int>(columns / (weights_per_byte * 4))
        };
        mlx::core::Shape scale_shape{
            static_cast<int>(rows),
            static_cast<int>(columns / 32)
        };
        const size_t weights_bytes = rows * columns / weights_per_byte;
        const size_t scale_bytes = block_count * mlx::core::float16.size();

        mlx::core::array weights(
            mlx::core::allocator::malloc(weights_bytes),
            weights_shape,
            mlx::core::uint32
        );
        mlx::core::array scales(
            mlx::core::allocator::malloc(scale_bytes),
            scale_shape,
            mlx::core::float16
        );
        mlx::core::array biases(
            mlx::core::allocator::malloc(scale_bytes),
            scale_shape,
            mlx::core::float16
        );

        auto packed_weights = weights.data<int8_t>();
        auto packed_scales = scales.data<mlx::core::float16_t>();
        auto packed_biases = biases.data<mlx::core::float16_t>();
        for (size_t block = 0; block < block_count; ++block) {
            const uint8_t *block_data = raw + block * bytes_per_block;
            std::memcpy(&packed_scales[block], block_data, sizeof(uint16_t));
            if (source_type == 2) {
                packed_biases[block] = -8 * packed_scales[block];
                unpack_32_4(block_data + 2, packed_weights + block * 16);
            } else if (source_type == 3) {
                std::memcpy(&packed_biases[block], block_data + 2, sizeof(uint16_t));
                unpack_32_4(block_data + 4, packed_weights + block * 16);
            } else {
                packed_biases[block] = -128 * packed_scales[block];
                for (size_t index = 0; index < 32; ++index) {
                    packed_weights[block * 32 + index] =
                        static_cast<int8_t>(block_data[index + 2] ^ (1 << 7));
                }
            }
        }

        weights_handle->context = new mlx::core::array(std::move(weights));
        scales_handle->context = new mlx::core::array(std::move(scales));
        biases_handle->context = new mlx::core::array(std::move(biases));
        last_error.clear();
        return 0;
    } catch (const std::exception &error) {
        last_error = error.what();
        return 1;
    }
}

extern "C" const char *tanpopo_mlx_gguf_last_error(void) {
    return last_error.c_str();
}
