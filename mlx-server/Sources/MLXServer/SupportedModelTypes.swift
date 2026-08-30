import Foundation
import MLXLLM
import MLXVLM

/// 回報這個 Runtime 實際能載入的模型型別，供 Tanpopo 過濾模型列表。
///
/// 直接讀 MLXLLM／MLXVLM 的型別註冊表與 GGUF 內建設定支援表，Tanpopo 端就不需要
/// 另外維護一份會過期的靜態清單。
enum SupportedModelTypes {
    static func emit() async {
        let payload: [String: Any] = [
            "version": ServerConfiguration.version,
            "llm": await LLMTypeRegistry.shared.registeredModelTypes,
            "vlm": await VLMTypeRegistry.shared.registeredModelTypes,
            "gguf": MLXGGUFEmbeddedAssets.supportedGGUFArchitectures
        ]
        guard JSONSerialization.isValidJSONObject(payload),
              let data = try? JSONSerialization.data(
                withJSONObject: payload,
                options: [.sortedKeys]
              ),
              let text = String(data: data, encoding: .utf8) else {
            fputs("mlx-server 無法輸出支援的模型型別。\n", stderr)
            Foundation.exit(EXIT_FAILURE)
        }
        print(text)
    }
}
