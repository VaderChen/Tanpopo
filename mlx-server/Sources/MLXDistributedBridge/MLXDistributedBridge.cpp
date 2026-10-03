#include "MLXDistributedBridge.h"
#include <string>
#include "mlx/c/private/mlx.h"
#include "mlx/c/error.h"
#include "mlx/distributed/distributed.h"
#include "mlx/distributed/ops.h"
#include "mlx/ops.h"
#include "mlx/primitives.h"
#include "mlx/io/load.h"

namespace mlx::core { extern thread_local int tanpopo_matmul_output_size; }

namespace {
struct MatmulOutputScope {
    int previous;
    explicit MatmulOutputScope(int output) : previous(mlx::core::tanpopo_matmul_output_size) {
        mlx::core::tanpopo_matmul_output_size = output;
    }
    ~MatmulOutputScope() { mlx::core::tanpopo_matmul_output_size = previous; }
};
template<typename Parent> class ShardedMatmul : public Parent {
    const int output_size_;
public:
    template<typename... Args> ShardedMatmul(int output_size, Args&&... args)
        : Parent(std::forward<Args>(args)...), output_size_(output_size) {}
    void eval_gpu(const std::vector<mlx::core::array>& inputs, mlx::core::array& output) override {
        MatmulOutputScope scope(output_size_);
        Parent::eval_gpu(inputs, output);
    }
    bool is_equivalent(const mlx::core::Primitive& other) const override {
        auto p = dynamic_cast<const ShardedMatmul*>(&other);
        return p && p->output_size_ == output_size_ && Parent::is_equivalent(other);
    }
};
}

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
// 多個張量共用一個 Reader；Load 持有檔案描述元，暫存檔 unlink 後仍可按列讀取。
using WeightReader = std::shared_ptr<mlx::core::io::Reader>;
extern "C" void* tanpopo_distributed_open_weights(const char* path) {
    WeightReader* result = nullptr;
    checked([&] {
        auto reader = std::make_shared<mlx::core::io::ParallelFileReader>(path);
        if (!reader->is_open()) throw std::runtime_error("Cannot open distributed weight file");
        result = new WeightReader(reader);
    });
    return result;
}
extern "C" void tanpopo_distributed_close_weights(void* reader) {
    delete static_cast<WeightReader*>(reader);
}
extern "C" int tanpopo_distributed_load_weights(void* reader, mlx_array* result, mlx_array descriptor, size_t offset) {
    return checked([&] {
        const auto& source = mlx_array_get_(descriptor);
        mlx_array_set_(*result, mlx::core::array(source.shape(), source.dtype(),
            std::make_shared<mlx::core::Load>(mlx::core::default_stream(mlx::core::Device::cpu),
                *static_cast<WeightReader*>(reader), offset), std::vector<mlx::core::array>{}));
    });
}
extern "C" int tanpopo_distributed_linear(mlx_array* result, mlx_array input, mlx_array weight, const mlx_array* bias, int original_output_size) {
    return checked([&] {
        using namespace mlx::core;
        const auto& x = mlx_array_get_(input);
        const auto& w = mlx_array_get_(weight);
        auto flat = reshape(x, {-1, x.shape(-1)});
        auto base = bias ? addmm(mlx_array_get_(*bias), flat, transpose(w)) : matmul(flat, transpose(w));
        std::shared_ptr<Primitive> primitive;
        if (bias) {
            primitive = std::make_shared<ShardedMatmul<AddMM>>(original_output_size, base.primitive().stream(), 1.0f, 1.0f);
        } else {
            primitive = std::make_shared<ShardedMatmul<Matmul>>(original_output_size, base.primitive().stream());
        }
        auto local = array(base.shape(), base.dtype(), primitive, base.inputs());
        auto shape = x.shape();
        shape.back() = w.shape(0);
        mlx_array_set_(*result, reshape(local, shape));
    });
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
