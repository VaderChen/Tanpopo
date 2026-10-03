import Foundation
import Cmlx
import MLX
import MLXDistributedBridge

/// 與模型架構無關的檔案 backing；Fast GGUF 的 raw／壓縮張量共用此路徑。
enum DistributedWeightLoading {
    @TaskLocal static var enabled = false
}

final class DistributedWeightFile {
    private let reader: UnsafeMutableRawPointer

    init(url: URL) throws {
        guard let reader = tanpopo_distributed_open_weights(url.path) else {
            throw DistributedError.invalid(String(cString: tanpopo_distributed_error()))
        }
        self.reader = reader
    }

    deinit { tanpopo_distributed_close_weights(reader) }

    func array(offset: Int, shape: [Int], dtype: DType) throws -> MLXArray {
        let descriptor = MLXArray.zeros(shape, dtype: dtype)
        var context = mlx_array_new()
        let code = tanpopo_distributed_load_weights(reader, &context, descriptor.ctx, offset)
        let result = MLXArray(context)
        guard code == 0 else {
            throw DistributedError.invalid(String(cString: tanpopo_distributed_error()))
        }
        return result
    }
}
