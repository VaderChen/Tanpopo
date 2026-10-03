#pragma once
#include <stdbool.h>
#include <mlx/c/array.h>

#ifdef __cplusplus
extern "C" {
#endif
// 與 Runtime 共用同一份 MLX Core；不得另外載入另一份 MLX 動態函式庫。
bool tanpopo_distributed_available(const char* backend);
void* tanpopo_distributed_init(const char* backend);
void tanpopo_distributed_free(void* group);
int tanpopo_distributed_rank(void* group);
int tanpopo_distributed_size(void* group);
const char* tanpopo_distributed_error(void);
void tanpopo_distributed_raise(const char* message);
int tanpopo_distributed_sum(void* group, mlx_array* result, mlx_array input);
int tanpopo_distributed_gather(void* group, mlx_array* result, mlx_array input);
bool tanpopo_distributed_can_copy_rows(mlx_array input);
int tanpopo_distributed_copy_rows(mlx_array* result, mlx_array input, int start, int end);
void* tanpopo_distributed_open_weights(const char* path);
void tanpopo_distributed_close_weights(void* reader);
int tanpopo_distributed_load_weights(void* reader, mlx_array* result, mlx_array descriptor, size_t offset);
int tanpopo_distributed_linear(mlx_array* result, mlx_array input, mlx_array weight, const mlx_array* bias, int original_output_size);
#ifdef __cplusplus
}
#endif
