(() => {
  "use strict";

  const STORAGE_KEY = "tanpopo.reportLanguage";
  const COMMON = {
    "模型相容性": "Model compatibility",
    "效能測試": "Performance",
    "模型": "Model",
    "模式": "Mode",
    "格式": "Format",
    "規模": "Scale",
    "狀態": "Status",
    "能力": "Capability",
    "架構": "Architecture",
    "支援": "Supported",
    "不適用": "Not applicable",
    "載入時驗證": "Validated at load time",
    "未宣告": "Not declared",
    "純 MLX": "Native MLX",
    "fastGGUF 關": "fastGGUF off",
    "fastGGUF 開": "fastGGUF on",
    "fastGGUF 關閉": "fastGGUF off",
    "fastGGUF 開啟": "fastGGUF on",
    "越高越好": "Higher is better",
    "硬體": "Hardware",
    "記憶體": "Memory",
    "速度": "Speed",
    "精度": "Accuracy",
    "觀察": "Observation",
    "手動": "Manual",
    "診斷用途": "Diagnostic use",
    "Tanpopo 專案首頁": "Tanpopo project home",
    "報告導覽": "Report navigation",
    "報告語言": "Report language"
  };

  const COMPATIBILITY = {
    "Tanpopo 模型相容性報告": "Tanpopo Model Compatibility Report",
    "Tanpopo 本機模型 Runtime 的 MLX、GGUF 與多模態相容性報告。": "Compatibility report for MLX, GGUF, and multimodal models in the Tanpopo local-model runtime.",
    "模型相容性報告": "Model Compatibility Report",
    "整理 Tanpopo 目前在 Apple Silicon 上對原生 MLX、MLX 直讀 GGUF、 llama.cpp GGUF、多模態投影、KV Cache 量化與推測解碼的支援邊界。": "Documents Tanpopo's current support boundaries on Apple Silicon for native MLX, direct GGUF loading through MLX, llama.cpp GGUF, multimodal projectors, KV-cache quantization, and speculative decoding.",
    "公開技術預覽": "Public technical preview",
    "報告版本資訊": "Report version information",
    "目前支援範圍": "Current support scope",
    "數量來自 Runtime 的能力登錄，不代表每個衍生 checkpoint 均已逐一實測。": "Counts come from the runtime capability registry and do not mean every derived checkpoint has been tested individually.",
    "原生 MLX LLM 類型": "Native MLX LLM types",
    "由 mlx-swift-lm 模型登錄表提供。": "Reported by the mlx-swift-lm model registry.",
    "原生 MLX VLM 類型": "Native MLX VLM types",
    "仍需 checkpoint 具備相容 processor 與視覺權重。": "Each checkpoint still needs a compatible processor and vision weights.",
    "可合成設定的 GGUF 架構": "GGUF architectures with synthesized configs",
    "不依賴同目錄 Hugging Face": "Does not require a colocated Hugging Face",
    "本輪重新驗證：21 項程式回歸、3 組實體模型": "Revalidated: 21 regression checks and 3 physical model pairs",
    "2026-08-30 重新執行": "Re-ran on 2026-08-30:",
    "，21 項測試通過、0 失敗， 其中 3 項需額外實體模型環境的測試在一般回歸中略過；另以 4B、9B、27B 配對模型逐一完成 原生 MLX、MLX+GGUF 與 llama+GGUF 的載入、生成及固定 100 題品質驗證。": ". All 21 checks passed with no failures. Three checks requiring additional physical models were skipped by the regular regression run. Paired 4B, 9B, and 27B models were also tested for loading, generation, and fixed-set 100-question quality across native MLX, MLX+GGUF, and llama+GGUF.",
    "格式與 Runtime 比較": "Format and runtime comparison",
    "「MLX + GGUF」代表 Swift Runtime 解析 GGUF，並轉成 MLX 可執行的權重佈局。": "“MLX + GGUF” means the Swift runtime parses GGUF and converts it into an MLX-executable weight layout.",
    "原生 MLX": "Native MLX",
    "主要權重格式": "Primary weight format",
    "Safetensors／MLX 量化": "Safetensors / MLX quantization",
    "GGUF，載入時映射或轉換": "GGUF, mapped or converted while loading",
    "GGUF 原生執行": "Native GGUF execution",
    "模型架構範圍": "Model architecture coverage",
    "最廣 MLX 範圍": "Broadest MLX coverage",
    "12 種已登錄 GGUF 架構": "12 registered GGUF architectures",
    "依內附 llama.cpp 版本": "Depends on the bundled llama.cpp version",
    "多模態": "Multimodal",
    "VLM checkpoint 與 processor": "VLM checkpoint and processor",
    "限已實作架構與配對 mmproj": "Limited to implemented architectures and paired mmproj",
    "限 llama.cpp 支援的 mmproj": "Limited to mmproj supported by llama.cpp",
    "可用；降低載入期間壓力": "Available; reduces loading pressure",
    "可用；需視 tensor 是否必須轉換": "Available; depends on whether tensors require conversion",
    "可用；由 llama.cpp 管理": "Available; managed by llama.cpp",
    "實驗性；速度通過、品質未通過": "Experimental; speed passed, quality failed",
    "Qwen3／Qwen3.5，需配對 MLX Draft": "Qwen3 / Qwen3.5 with a paired MLX draft",
    "GGUF Target 不啟用": "Disabled for GGUF targets",
    "需配對 DFlash Draft GGUF": "Requires a paired DFlash draft GGUF",
    "失敗診斷": "Failure diagnostics",
    "設定／processor／權重驗證": "Config / processor / weight validation",
    "架構、量化、映射、形狀逐層驗證": "Layer-by-layer architecture, quantization, mapping, and shape validation",
    "llama.cpp 啟動與模型日誌": "llama.cpp startup and model logs",
    "GGUF 架構相容性": "GGUF architecture compatibility",
    "專屬路徑會處理該架構特有的設定、norm、tokenizer 或逐層參數。": "Dedicated paths handle architecture-specific configuration, norms, tokenizers, and per-layer parameters.",
    "合成策略": "Synthesis strategy",
    "文字模型": "Text model",
    "多模態備註": "Multimodal notes",
    "驗證狀態": "Validation status",
    "專屬；逐層 xIELU": "Dedicated; per-layer xIELU",
    "本報告只驗證文字輸出": "This report validates text output only",
    "70B 實機驗證": "70B validated on hardware",
    "通用文字設定": "Generic text configuration",
    "依模型變體": "Depends on model variant",
    "專屬；四組 norm、SentencePiece": "Dedicated; four norm sets and SentencePiece",
    "文字 encoder 已驗證": "Text encoder validated",
    "12B 實機驗證": "12B validated on hardware",
    "專屬；PLE、共享 K/V、逐層 FFN": "Dedicated; PLE, shared K/V, and per-layer FFN",
    "需配對 mmproj 才能處理影像": "Requires a paired mmproj for image input",
    "E2B／E4B 實機驗證": "E2B / E4B validated on hardware",
    "標準文字設定": "Standard text configuration",
    "依配對 projector": "Depends on the paired projector",
    "視實際變體": "Depends on the actual variant",
    "文字路徑": "Text path",
    "專屬；混合層與 tokenizer": "Dedicated; hybrid layers and tokenizer",
    "可搭配相容 mmproj": "Can use a compatible mmproj",
    "4B／9B／27B 實機驗證": "4B / 9B / 27B validated on hardware",
    "GGUF tensor 支援": "GGUF tensor support",
    "來源量化能否原樣保留，會直接影響載入時間、記憶體與數值差異。": "Whether source quantization can be preserved directly affects loading time, memory use, and numerical drift.",
    "浮點來源統一 BF16": "Normalize floating-point sources to BF16",
    "F32、F16、BF16 tensor 會建立 BF16 運算權重；I8、I16、I32 則維持對應整數 tensor。": "F32, F16, and BF16 tensors create BF16 compute weights; I8, I16, and I32 retain their corresponding integer tensor types.",
    "保留小型參數精度": "Preserves small-parameter precision",
    "形狀仍須驗證": "Shapes still require validation",
    "Q8 保留 INT8 角色": "Q8 retains an INT8 role",
    "Q8_0 會轉成 MLX affine INT8；避免將 GGUF 中刻意保留較高精度的 tensor 無條件壓成 INT4。": "Q8_0 converts to MLX affine INT8, avoiding unconditional INT4 compression of tensors intentionally stored at higher precision in GGUF.",
    "metadata 驅動": "Metadata-driven",
    "fastGGUF 關閉：Auto／INT8": "fastGGUF off: Auto / INT8",
    "產品目前以": "The product currently uses",
    "載入低位元 GGUF，將 Q4 等來源重新量化為 MLX INT8；這是較保守的數值路徑，但不等於保真模式。": "to load low-bit GGUF sources such as Q4 and requantize them to MLX INT8. This is a more conservative numerical path, but it is not a fidelity mode.",
    "轉換成本最高": "Highest conversion cost",
    "固定 group 32": "Fixed group 32",
    "fastGGUF 開啟：Speed／INT4": "fastGGUF on: Speed / INT4",
    "讓低位元矩陣走 MLX INT4，全部相容時優先 group 64，並將量化的 recurrent 控制投影保留為 BF16。這是通用 tensor 角色策略，不依模型名稱；本輪表格數據為整合前基線，仍需重新量測。": "routes low-bit matrices through MLX INT4, prefers group 64 when fully compatible, and retains quantized recurrent control projections as BF16. This generic tensor-role strategy does not depend on model names. The table contains the pre-integration baseline and must be measured again.",
    "品質閘門未通過": "Quality gate failed",
    "Quality 診斷路徑": "Quality diagnostic path",
    "會以 FP32 參考權重執行，適合定位轉換誤差，不是 fastGGUF 開關的一部分，也不適合作為一般速度基準。": "runs FP32 reference weights to isolate conversion errors. It is not part of the fastGGUF toggle and is not suitable as a normal speed baseline.",
    "本機實測樣本": "Locally tested samples",
    "本輪三組抽樣同時驗證「可載入」與「輸出品質」；兩者不可互相替代。": "The three samples validate both loadability and output quality; neither result substitutes for the other.",
    "驗證項目": "Validation",
    "結果": "Result",
    "四模式載入、生成、MMLU 100 題": "Four-mode load, generation, and 100-question MMLU",
    "載入通過；fast 品質失敗": "Load passed; fast quality failed",
    "SentencePiece、norm、固定輸出": "SentencePiece, norms, and deterministic output",
    "先前實機通過": "Previously validated on hardware",
    "PLE、共享 K/V、FFN 版型": "PLE, shared K/V, and FFN layouts",
    "xIELU、MMap、chat EOS 停止": "xIELU, MMap, and chat EOS stopping",
    "目前邊界與判定原則": "Current boundaries and evaluation rules",
    "GGUF 架構尚無對應的 mlx-swift-lm 模型實作；保留在「尚未測試」群組供手動嘗試，不宣告相容。": "No corresponding mlx-swift-lm model implementation exists for this GGUF architecture. It remains selectable in the “Untested” group for manual attempts, without a compatibility claim.",
    "影音／擴散 GGUF": "Video / diffusion GGUF",
    "LTX Video、MiniMax H3 影音 checkpoint 與獨立 T5 encoder 並非文字 LLM，不會因副檔名為 GGUF 就列入文字模型名單。": "LTX Video, MiniMax H3 media checkpoints, and standalone T5 encoders are not text LLMs and are not listed as text models merely because they use the GGUF extension.",
    "MMap 不是硬性記憶體上限": "MMap is not a hard memory limit",
    "它會嘗試讓檔案頁可回收並保留目標空間；需要重新量化的 tensor 仍可能產生常駐 MLX 權重。": "It tries to keep file-backed pages reclaimable and preserve target headroom; tensors that require requantization may still produce resident MLX weights.",
    "衍生 checkpoint 必須重新驗證": "Derived checkpoints require revalidation",
    "即使": "Even when",
    "相同，新增 expert、processor 或非標準 tensor 名稱仍可能需要新的通用映射。": "is identical, added experts, processors, or non-standard tensor names may require new generic mappings.",
    "可生成不代表品質可用": "Successful generation does not guarantee usable quality",
    "本輪 fastGGUF 的三個樣本都能載入並完成 128-token 生成，但固定題庫全部未過品質閘門；相容性狀態必須同時記錄載入與數值品質。": "All three fastGGUF samples loaded and completed 128-token generation, but every fixed-set quality gate failed. Compatibility status must record both loading and numerical quality.",
    "閱讀效能比較 →": "Read the performance comparison →"
  };

  const PERFORMANCE = {
    "Tanpopo MLX 與 GGUF 效能及精確度比較": "Tanpopo MLX and GGUF Performance & Accuracy Comparison",
    "Tanpopo 在 Apple M4 Pro 上比較原生 MLX、fastGGUF 開關與 llama.cpp GGUF 的速度及精確度。": "Tanpopo speed and accuracy comparison of native MLX, fastGGUF on/off, and llama.cpp GGUF on Apple M4 Pro.",
    "MLX 與 GGUF": "MLX and GGUF",
    "速度及精確度": "Speed and Accuracy",
    "以 4B、9B、27B 三組配對模型重新比較純 MLX、fastGGUF 關閉、fastGGUF 開啟及 llama+GGUF。速度接近純 MLX 並不足夠；本報告也以固定 100 題驗證輸出品質。": "Three paired 4B, 9B, and 27B models compare native MLX, fastGGUF off, fastGGUF on, and llama+GGUF. Approaching native MLX speed is not enough; a fixed 100-question set also validates output quality.",
    "測試摘要": "Test summary",
    "3 組": "3 pairs",
    "執行模式": "Execution modes",
    "4 種": "4 modes",
    "速度正式量測": "Formal speed runs",
    "36 次": "36 runs",
    "精度正式問答": "Formal accuracy questions",
    "1,200 次": "1,200 questions",
    "速度成功，品質尚未達標": "Speed target met; quality target missed",
    "跨模型速度使用同模型倍率的幾何平均；精度以每模型相同的 100 題比較。": "Cross-model speed uses the geometric mean of within-model ratios; accuracy uses the same 100 questions for each model.",
    "fastGGUF 開啟對關閉": "fastGGUF on vs. off",
    "三個模型生成速度倍率的幾何平均。": "Geometric mean of generation-speed ratios across three models.",
    "fastGGUF 達到純 MLX": "fastGGUF relative to native MLX",
    "三個樣本分別為 99.7%、101.3%、109.8%。": "The three samples reached 99.7%, 101.3%, and 109.8%.",
    "fastGGUF 品質閘門": "fastGGUF quality gates",
    "開、關各三組，全部未達 llama+GGUF 精度門檻。": "All three on and all three off runs missed the llama+GGUF accuracy threshold.",
    "目前 fastGGUF 只能視為效能 POC": "fastGGUF currently remains a performance POC",
    "開啟後，4B、9B、27B 的中位生成速度都接近或超過純 MLX；但固定題庫中，4B 只有 40%， 9B 與 27B 更出現大量無效輸出。關閉 fastGGUF 的 Auto／INT8 路徑同樣沒有恢復品質， 因此開關不能被描述成「速度／品質」二選一，正式啟用前仍需要通用的數值保真策略與架構能力門控。": "With fastGGUF on, median generation speed for 4B, 9B, and 27B approached or exceeded native MLX. On the fixed set, however, 4B reached only 40%, while 9B and 27B produced many invalid outputs. The Auto/INT8 path with fastGGUF off did not restore quality either. The toggle therefore cannot be described as a speed/quality tradeoff; production enablement still requires a generic numerical-fidelity strategy and architecture capability gates.",
    "測試環境": "Test environment",
    "四種模式在同一台主機串行執行；同一時間只啟動一個 mlx-server 或 llama-server。": "All four modes ran serially on the same host, with only one mlx-server or llama-server active at a time.",
    "主機": "Host",
    "12 核／16 核": "12 cores / 16 cores",
    "64 GB 統一記憶體": "64 GB unified memory",
    "系統與模型磁碟": "System and model storage",
    "架構／Metal": "Architecture / Metal",
    "模型卷宗": "Model volume",
    "儲存": "Storage",
    "測試前確認沒有其他 mlx-server／llama-server；": "Before testing, no other mlx-server or llama-server process was running;",
    "未回報 thermal 或 performance warning。 速度異常組合另行重跑，正式吞吐採三次中位數降低偶發背景負載影響。未清除 macOS 系統檔案快取。": "reported no thermal or performance warning. Outlier combinations were rerun, and formal throughput uses the median of three runs to reduce incidental background-load effects. The macOS file cache was not cleared.",
    "fastGGUF 開關實際策略": "Actual fastGGUF toggle strategy",
    "表內速度與精度是整合 recurrent controls 前的基線；主系統現行 fastGGUF 另將控制投影保留 BF16，整合後數據需重新量測。": "The speed and accuracy values in the table are the baseline before recurrent-controls integration. The current fastGGUF path also retains control projections as BF16, so the integrated strategy requires a new measurement run.",
    "參數": "Parameters",
    "主要權重": "Primary weights",
    "定位": "Role",
    "原生 checkpoint": "Native checkpoint",
    "速度與品質基準": "Speed and quality baseline",
    "Q4 等低位元來源重新量化為 INT8": "Requantizes low-bit sources such as Q4 to INT8",
    "較保守，但不是保真模式": "More conservative, but not a fidelity mode",
    "低位元矩陣轉為 INT4；控制投影保留 BF16": "Converts low-bit matrices to INT4; retains control projections as BF16",
    "實驗性速度／精度折衷": "Experimental speed/accuracy tradeoff",
    "原始 GGUF block": "Original GGUF blocks",
    "Q4_0／Q4_K_M 原生執行": "Native Q4_0 / Q4_K_M execution",
    "相同 GGUF 的品質參考": "Quality reference for the same GGUF",
    "會走 FP32 參考權重，只用於誤差診斷，不屬於 fastGGUF 開關，也未納入本次速度比較。": "uses FP32 reference weights for error diagnostics only. It is not part of the fastGGUF toggle and is excluded from this speed comparison.",
    "生成速度": "Generation speed",
    "每組先暖機一次，再取三次 128-token 回覆的 Runtime 生成速度中位數。": "Each combination receives one warm-up, followed by the median runtime generation speed from three 128-token responses.",
    "中位 tok/s": "Median tok/s",
    "端到端 tok/s": "End-to-end tok/s",
    "比較基準 (MLX)": "Baseline (MLX)",
    "比較基準 (LLAMA)": "Baseline (LLAMA)",
    "「中位 tok/s」是 Runtime 回報的三次生成速度中位數；「端到端 tok/s」依請求開始到回覆完成的實際經過時間換算，包含 API、排程與資料處理開銷。": "“Median tok/s” is the median of three generation speeds reported by the runtime. “End-to-end tok/s” uses actual elapsed time from request start to response completion, including API, scheduling, and data-processing overhead.",
    "精確度與有效答案": "Accuracy and valid answers",
    "MMLU 固定 100 題、seed 0；四種模式使用完全相同題目、prompt、temperature 0 與 seed 42。": "MMLU uses a fixed 100-question set with seed 0. All four modes use identical questions, prompts, temperature 0, and seed 42.",
    "fast 關": "fast off",
    "fast 開": "fast on",
    "無效（關／開）": "Invalid (off / on)",
    "品質閘門": "Quality gate",
    "兩者失敗": "Both failed",
    "品質閘門定義": "Quality-gate definition",
    "fastGGUF 精度不得比同一 GGUF 的 llama 參考低超過 2 個百分點，無效答案不得多超過 1 題。 逐題一致率只作診斷，不直接當門檻。本輪 6 個 fastGGUF 組合全部失敗；27B 抽查可見連續標點輸出， 證實無效答案不是評分器漏抓 A–D。": "fastGGUF accuracy may be at most 2 percentage points below the llama reference for the same GGUF, with at most one additional invalid answer. Per-question agreement is diagnostic only, not a direct gate. All six fastGGUF combinations failed. A 27B spot check showed repeated punctuation, confirming that invalid answers were not caused by the evaluator missing A–D.",
    "載入時間與程序峰值 RSS": "Load time and peak process RSS",
    "載入從建立程序到": "Load time runs from process creation until",
    "可用；RSS 每 200 ms 取樣，不能視為統一記憶體硬上限。": "is available. RSS is sampled every 200 ms and must not be treated as a hard unified-memory limit.",
    "載入秒數": "Load time",
    "峰值 RSS": "Peak RSS",
    "INT8 轉換": "INT8 conversion",
    "INT4 轉換": "INT4 conversion",
    "原生 GGUF block": "Native GGUF blocks",
    "本模型最高 RSS": "Highest RSS for this model",
    "較快轉換": "Faster conversion",
    "直接載入": "Direct loading",
    "最慢轉換": "Slowest conversion",
    "RSS 未顯著下降": "No material RSS reduction",
    "MLX+GGUF 必須解碼並重新量化 K-quant，且目前沒有跨程序的永久轉換快取；MMap 也不會消除已轉換 MLX 權重的常駐成本。": "MLX+GGUF must decode and requantize K-quants, and currently has no persistent conversion cache across processes. MMap also does not eliminate the resident cost of converted MLX weights.",
    "已整合為 fastGGUF 的實驗策略，預設仍關閉。": "Integrated as an experimental fastGGUF strategy; it remains off by default.",
    "只提升 alpha／beta 控制投影可改善 4B，但仍未過門檻": "Promoting only alpha/beta control projections improves 4B, but still misses the gate",
    "先前以 Qwen 3.5 4B 將 Gated Delta／SSM 的 alpha、beta 投影升為 BF16，固定 100 題由 fastGGUF 基線 41% 提升到 59%，無效答案由 29 降至 3，生成維持約 73.6 tok/s；但仍比 llama 參考 72% 低 13 點。 此改善不足以讓 fastGGUF 預設開啟，因此只在使用者明確啟用時，以 tensor 語意角色通用套用；後續仍需擴充誤差敏感度與架構合約。": "A prior Qwen 3.5 4B experiment promoted Gated Delta/SSM alpha and beta projections to BF16. Accuracy on the fixed 100 questions improved from the 41% fastGGUF baseline to 59%, invalid answers fell from 29 to 3, and generation remained around 73.6 tok/s. It still trailed the 72% llama reference by 13 points. This improvement is insufficient to enable fastGGUF by default, so the generic tensor-role policy is applied only when users explicitly opt in. Error-sensitivity and architecture contracts still require expansion.",
    "POC 彙總：": "POC summary:",
    "；主系統參數為": "; main-system parameter:",
    "測試方法與限制": "Methodology and limitations",
    "OpenAI 相容": "OpenAI-compatible",
    "Temperature 0、Top-K 1、thinking 關閉": "Temperature 0, Top-K 1, thinking disabled",
    "暖機 1 次；正式 3 次採中位 tok/s": "One warm-up; median tok/s from three formal runs",
    "固定 100 題、seed 0": "Fixed 100 questions, seed 0",
    "每個模型與模式使用完全相同題目": "Identical questions for every model and mode",
    "只接受獨立 A、B、C、D；其餘為無效": "Only standalone A, B, C, or D is valid; all other output is invalid",
    "共同 Runtime": "Common runtime settings",
    "兩個 Runtime 均使用 Release build": "Both runtimes use Release builds",
    "MLX+GGUF 明確傳入": "MLX+GGUF explicitly receives",
    "；純 MLX 未傳入": "; native MLX does not",
    "KV cache 上限 4096 tokens": "KV cache limited to 4096 tokens",
    "未啟用 DFlash 與 KV Cache 量化": "DFlash and KV-cache quantization disabled",
    "llama：Metal 全層、Flash Attention、8 threads": "llama: all layers on Metal, Flash Attention, 8 threads",
    "解讀限制": "Interpretation limits",
    "三個模型是通用策略的抽樣證據，不是支援白名單": "The three models are samples supporting a generic strategy, not a support allowlist",
    "載入時間未清除系統檔案快取": "System file cache was not cleared for load-time measurements",
    "程序 RSS 不等於系統統一記憶體總量": "Process RSS is not total system unified-memory use",
    "Runtime 或 Metal 更新後應重新測試": "Retest after runtime or Metal updates",
    "速度彙總：": "Speed summary:",
    "· 精度彙總：": "· Accuracy summary:",
    "· 重現腳本：": "· Reproduction scripts:",
    "目前建議": "Current recommendations",
    "正式使用優先純 MLX": "Prefer native MLX for production",
    "若有可信原生 MLX checkpoint，這仍是本輪速度、載入、RSS 與精度最完整的路徑。": "When a trustworthy native MLX checkpoint exists, it remains the most complete path in this round for speed, loading, RSS, and accuracy.",
    "GGUF 先回退 llama": "Use llama as the GGUF fallback",
    "llama+GGUF 在三模型都維持有效答案與合理精度，且不需支付 MLX 重新量化的載入成本。": "llama+GGUF maintained valid answers and reasonable accuracy for all three models without the MLX requantization load cost.",
    "fastGGUF 保持實驗旗標": "Keep fastGGUF experimental",
    "速度目標已達成，但不可只因模型能載入就開放；至少應通過模型級 smoke quality gate 才能啟用。": "The speed target is met, but loadability alone is insufficient. At minimum, a model-level smoke quality gate should pass before enablement.",
    "下一步是數值合約": "Next: numerical contracts",
    "建立逐層漂移、logit 一致性、敏感 tensor 提升與自動回退，讓策略依 tensor／架構能力決定，而非模型名稱。": "Add per-layer drift checks, logit agreement, sensitive-tensor promotion, and automatic fallback so strategy follows tensor and architecture capabilities rather than model names.",
    "← 閱讀模型相容性": "← Read model compatibility",
    "圖例": "Legend",
    "三個模型在四種模式的生成速度長條圖": "Generation-speed bar chart for three models across four modes",
    "三個模型在四種模式的 MMLU 精確度長條圖": "MMLU accuracy bar chart for three models across four modes"
  };

  const reportName = document.body.dataset.report;
  const catalog = { ...COMMON, ...(reportName === "performance" ? PERFORMANCE : COMPATIBILITY) };
  const textNodes = [];
  const attributes = [];

  const normalized = (value) => value.trim().replace(/\s+/g, " ");
  const languageButtons = Array.from(document.querySelectorAll("[data-report-language]"));
  const languageControl = document.querySelector(".language-switcher");

  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      if (!normalized(node.nodeValue || "")) return NodeFilter.FILTER_REJECT;
      if (languageControl && languageControl.contains(node.parentElement)) return NodeFilter.FILTER_REJECT;
      return NodeFilter.FILTER_ACCEPT;
    }
  });
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    textNodes.push({ node, original: node.nodeValue });
  }

  document.querySelectorAll("[aria-label], meta[name='description']").forEach((element) => {
    const name = element.matches("meta") ? "content" : "aria-label";
    const original = element.getAttribute(name);
    if (original) attributes.push({ element, name, original });
  });

  const autoLanguage = () => {
    const candidates = Array.isArray(navigator.languages) && navigator.languages.length
      ? navigator.languages
      : [navigator.language || ""];
    return candidates.some((language) => /^(zh)(-|$)/i.test(language)) ? "zh-Hant" : "en";
  };

  const preferredLanguage = () => {
    let stored = "auto";
    try {
      stored = localStorage.getItem(STORAGE_KEY) || "auto";
    } catch (_) {
      // file:// 與部分隱私模式可能停用 storage；仍可在本頁切換語言。
    }
    return ["auto", "zh-Hant", "en"].includes(stored) ? stored : "auto";
  };

  const translate = (value, language) => {
    if (language !== "en") return value;
    const key = normalized(value);
    const translated = catalog[key];
    if (!translated) return value;
    const leading = value.match(/^\s*/)?.[0] || "";
    const trailing = value.match(/\s*$/)?.[0] || "";
    return `${leading}${translated}${trailing}`;
  };

  const applyLanguage = (preference) => {
    const language = preference === "auto" ? autoLanguage() : preference;
    document.documentElement.lang = language;
    textNodes.forEach(({ node, original }) => {
      node.nodeValue = translate(original, language);
    });
    attributes.forEach(({ element, name, original }) => {
      element.setAttribute(name, normalized(translate(original, language)));
    });
    const titleSource = reportName === "performance"
      ? "Tanpopo MLX 與 GGUF 效能及精確度比較"
      : "Tanpopo 模型相容性報告";
    document.title = language === "en" ? catalog[titleSource] : titleSource;
    languageButtons.forEach((button) => {
      const selected = button.dataset.reportLanguage === preference;
      button.setAttribute("aria-pressed", selected ? "true" : "false");
    });
  };

  languageButtons.forEach((button) => {
    button.addEventListener("click", () => {
      const language = button.dataset.reportLanguage;
      try {
        localStorage.setItem(STORAGE_KEY, language);
      } catch (_) {
        // 無法寫入 storage 時只套用本次頁面，不阻斷切換。
      }
      applyLanguage(language);
    });
  });

  applyLanguage(preferredLanguage());
})();
