#ifndef TANPOPO_MLX_CORE_GGUF_BRIDGE_H
#define TANPOPO_MLX_CORE_GGUF_BRIDGE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct tanpopo_mlx_array_handle {
    void *context;
} tanpopo_mlx_array_handle;

/// POC：將單一 GGUF Q4_0、Q4_1 或 Q8_0 Tensor 的原始 block，
/// 依 MLX Core 最新版邏輯轉為 quantized() 所用的三個 Array。
/// 回傳 Array 的所有權交給 Swift MLXArray。
int tanpopo_mlx_gguf_pack_reference(
    const uint8_t *raw,
    size_t raw_count,
    uint32_t source_type,
    size_t rows,
    size_t columns,
    tanpopo_mlx_array_handle *weights,
    tanpopo_mlx_array_handle *scales,
    tanpopo_mlx_array_handle *biases
);

const char *tanpopo_mlx_gguf_last_error(void);

#ifdef __cplusplus
}
#endif

#endif
