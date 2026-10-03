#include "MLXDistributedBridge.h"
#include <string>
#include "mlx/c/private/mlx.h"
#include "mlx/c/error.h"
#include "mlx/distributed/distributed.h"
#include "mlx/distributed/ops.h"
#include "mlx/ops.h"
#include "mlx/primitives.h"

namespace dist = mlx::core::distributed;
namespace {
thread_local std::string last_error;
template <typename F> int checked(F&& operation) {
    try { operation(); last_error.clear(); return 0; }
    catch (const std::exception& error) { last_error = error.what(); return -1; }
}
}
extern "C" bool tanpopo_distributed_available(const char* backend) {
    bool available = false;
    checked([&] { available = dist::is_available(backend); });
    return available;
}
extern "C" void* tanpopo_distributed_init(const char* backend) {
    dist::Group* group = nullptr;
    checked([&] { group = new dist::Group(dist::init(true, backend)); });
    return group;
}
extern "C" void tanpopo_distributed_free(void* group) {
    delete static_cast<dist::Group*>(group);
}
extern "C" int tanpopo_distributed_rank(void* group) {
    return static_cast<dist::Group*>(group)->rank();
}
extern "C" int tanpopo_distributed_size(void* group) {
    return static_cast<dist::Group*>(group)->size();
}
extern "C" const char* tanpopo_distributed_error() { return last_error.c_str(); }
extern "C" void tanpopo_distributed_raise(const char* message) { mlx_error("%s", message); }
extern "C" int tanpopo_distributed_sum(void* group, mlx_array* result, mlx_array input) {
    return checked([&] {
        mlx_array_set_(*result, dist::all_sum(mlx_array_get_(input),
            *static_cast<dist::Group*>(group), mlx::core::Device::cpu));
    });
}
extern "C" int tanpopo_distributed_gather(void* group, mlx_array* result, mlx_array input) {
    return checked([&] {
        mlx_array_set_(*result, dist::all_gather(mlx_array_get_(input),
            *static_cast<dist::Group*>(group), mlx::core::Device::cpu));
    });
}
extern "C" bool tanpopo_distributed_can_copy_rows(mlx_array input) {
    bool supported = false;
    checked([&] {
        const auto& source = mlx_array_get_(input);
        if (source.ndim() < 1) return;
        supported = source.is_available();
#ifdef TANPOPO_MLX_LOAD_ROW_SLICE
        if (source.has_primitive() && dynamic_cast<mlx::core::Load*>(&source.primitive())) {
            supported = true;
        }
#endif
    });
    return supported;
}
extern "C" int tanpopo_distributed_copy_rows(mlx_array* result, mlx_array input, int start, int end) {
    return checked([&] {
        const auto& source = mlx_array_get_(input);
        if (source.ndim() < 1 || start < 0 || end <= start || end > source.shape(0)) {
            throw std::runtime_error("Invalid distributed weight row range");
        }
        auto shape = source.shape();
        shape[0] = end - start;
#ifdef TANPOPO_MLX_LOAD_ROW_SLICE
        if (source.has_primitive()) {
            if (auto* loader = dynamic_cast<mlx::core::Load*>(&source.primitive())) {
                const auto offset = static_cast<size_t>(start) * (source.nbytes() / source.shape(0));
                mlx_array_set_(*result, mlx::core::array(shape, source.dtype(),
                    loader->with_byte_offset(offset), std::vector<mlx::core::array>{}));
                return;
            }
        }
#endif
        if (!source.is_available()) {
            throw std::runtime_error("Weight is not a supported lazy file tensor; rebuild with the distributed load patch");
        }
        // 已求值的小型測試／自訂權重仍可安全複製，不保留完整來源緩衝區。
        mlx::core::Shape begins(source.ndim(), 0), ends = source.shape();
        begins[0] = start;
        ends[0] = end;
        mlx_array_set_(*result, mlx::core::copy(mlx::core::slice(source, begins, ends)));
    });
}
