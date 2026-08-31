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
    "原生MLX檔案未轉換": "Native MLX File, Unconverted",
    "MLX + 預設快速轉換": "MLX + Default Fast Conversion",
    "MLX + Beta 1": "MLX + Beta 1",
    "MLX + Beta 2": "MLX + Beta 2",
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
    "Tanpopo 公開測試報告首頁": "Tanpopo public reports home",
    "繁中": "Traditional Chinese",
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
    "本輪重新驗證：23 項程式回歸、3 組實體模型": "Revalidated: 23 regression checks and 3 physical model pairs",
    "2026-08-31 重新執行": "Re-ran on 2026-08-31:",
    "，共 23 項，其中 20 項通過、0 失敗， 另 3 項需額外實體模型環境的測試在一般回歸中略過；並以 4B、9B、27B 配對模型逐一完成 原生 MLX、MLX+GGUF 與 llama+GGUF 的載入、生成及固定 100 題品質驗證。": ". Of 23 checks, 20 passed with no failures and three requiring additional physical-model fixtures were skipped by the regular regression run. Paired 4B, 9B, and 27B models were also tested for loading, generation, and fixed-set 100-question quality across native MLX, MLX+GGUF, and llama+GGUF.",
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
    "讓低位元矩陣走 MLX INT4，直接保留 GGUF 32 元素 block 時使用 group 32，需要重新量化時才優先 group 64，並將量化的 recurrent 控制投影保留為 BF16。這是通用 tensor 角色策略，不依模型名稱；本輪數據已重測。": "routes low-bit matrices through MLX INT4, uses group 32 when preserving native 32-element GGUF blocks, prefers group 64 only when requantization is required, and retains quantized recurrent control projections as BF16. This generic tensor-role strategy does not depend on model names, and this round has been retested.",
    "Quality 診斷路徑": "Quality diagnostic path",
    "會以 FP32 參考權重執行，適合定位轉換誤差，不是 fastGGUF 開關的一部分，也不適合作為一般速度基準。": "runs FP32 reference weights to isolate conversion errors. It is not part of the fastGGUF toggle and is not suitable as a normal speed baseline.",
    "本機實測樣本": "Locally tested samples",
    "本輪三組抽樣同時驗證「可載入」與「輸出品質」；兩者不可互相替代。": "The three samples validate both loadability and output quality; neither result substitutes for the other.",
    "驗證項目": "Validation",
    "結果": "Result",
    "四模式載入、生成、MMLU 100 題": "Four-mode load, generation, and 100-question MMLU",
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
    "本輪重新驗證：27 項程式回歸、3 組實體模型": "Revalidated: 27 regression checks and 3 physical model pairs",
    "本次重新執行": "This round reran",
    "，共 27 項，其中 24 項通過、0 失敗， 另 3 項需額外實體模型環境的測試在一般回歸中略過；並以 4B、9B、27B 配對模型逐一完成 原生 MLX、MLX+GGUF 與 llama+GGUF 的載入、生成及固定 100 題品質驗證。": ". Of 27 checks, 24 passed with no failures and three requiring physical-model fixtures were skipped in the regular regression run. Paired 4B, 9B, and 27B models also completed loading, generation, and fixed 100-question quality validation.",
    "產品以": "The product uses",
    "載入低位元 GGUF；直接 Q4 與 K-Quant／IQ 等需二次轉換來源至少使用 MLX INT8。這是較保守的數值路徑，但不等於無損模式。": "to load low-bit GGUF. Direct Q4 and sources requiring secondary conversion, including K-Quant and IQ, use at least MLX INT8. This is a conservative numerical path, not a lossless mode.",
    "較高轉換成本": "Higher conversion cost",
    "fastGGUF 開啟：Speed／來源感知": "fastGGUF on: Speed / source-aware",
    "只讓可直接沿用來源布局的 Q4_0／Q4_1 保留 INT4；Q1_K～Q6_K、IQ 與其他需二次量化的低位元來源至少使用 INT8。recurrent BF16 提升保留為手動選項。這是通用 metadata 策略，不依模型名稱。": "preserves INT4 only for Q4_0/Q4_1 layouts that can be reused directly. Q1_K through Q6_K, IQ, and other low-bit sources requiring secondary quantization use at least INT8. Recurrent BF16 promotion remains manual. This generic metadata strategy is not model-name-specific.",
    "永久 .fgguf 快取": "Persistent .fgguf cache",
    "轉換結果以分片容器放在原始 GGUF 同一目錄；每個 tensor 依收益選擇 LZFSE 無損壓縮或 raw MMap 對齊。格式不是標準 GGUF，可由「清除快取」獨立移除。": "Converted weights are stored as sharded containers beside the source GGUF. Each tensor uses lossless LZFSE compression only when beneficial, otherwise raw MMap-aligned storage. This is not standard GGUF and can be removed independently with Clear Cache.",
    "FGGUF 格式結構": "FGGUF format structure",
    "永久重用": "Persistent reuse",
    "三種 MLX 模式載入、生成、MMLU 100 題": "Three MLX modes: load, generation, and 100-question MMLU",
    "本輪重新驗證：32 項程式回歸與固定題集": "Revalidated: 32 regression checks and fixed evaluation sets",
    "本次 Swift 測試共 32 項，其中 29 項通過、0 失敗，另 3 項需額外實體模型環境而略過； Ornith 1.5 9B 完成原生 MLX 與三種 MLX+GGUF 策略的固定 100 題及資源量測， Qwen 3.5 4B Q4_0 另以 20 題確認修改前後逐題結果一致。": "The Swift suite contained 32 tests: 29 passed, none failed, and three requiring additional physical model fixtures were skipped. Ornith 1.5 9B completed a fixed 100-question and resource evaluation across native MLX and three MLX+GGUF strategies. Qwen 3.5 4B Q4_0 also completed a 20-question regression with identical per-question results before and after the change.",
    "支援；需模型級品質驗證": "Supported; model-level quality validation is required",
    "來源量化能否原樣保留，會直接影響磁碟占用、記憶體與數值差異。": "Whether source quantization can be preserved directly affects storage, memory, and numerical differences.",
    "MLX + 預設快速轉換：Speed／Auto": "MLX + Default Fast Conversion: Speed / Auto",
    "將 K-Quant 重新量化為 MLX INT8；auto 固定使用 group 64。這是本輪三種 GGUF 量測路徑之一，不等於無損模式。": "requantizes K-Quant to MLX INT8, with auto fixed at group 64. This is one of the three GGUF paths measured in this round and is not a lossless mode.",
    "來源 block 沿用：Speed Passthrough": "Source-block reuse: Speed Passthrough",
    "依 GGUF block metadata 沿用可表示的來源 4-bit block；其他張量仍按明確指定的 group 32 或 64 處理。這是通用 tensor 策略，不依模型名稱。": "reuses representable source 4-bit blocks according to GGUF block metadata. Other tensors use an explicitly specified group 32 or 64. This generic tensor strategy does not depend on model names.",
    "Beta 1／Beta 2 實測": "Beta 1 / Beta 2 measured",
    "表中分開標示本輪量測與先前實機紀錄；可載入與輸出品質是不同驗證項目。": "The table distinguishes this round's measurements from earlier hardware records. Loadability and output quality are separate validation items.",
    "本輪修改前後回歸，固定 20 題": "Before-and-after regression in this round, fixed 20 questions",
    "15／20；逐題結果一致": "15/20; identical per-question results",
    "原生 MLX 與三種 MLX+GGUF 策略，固定 100 題": "Native MLX and three MLX+GGUF strategies, fixed 100 questions",
    "先前固定 100 題紀錄；本輪未重測": "Earlier fixed 100-question record; not rerun this round",
    "本輪策略均能載入並完成生成，但速度、資源與固定題集結果不同；相容性紀錄必須同時保留執行狀態與數值品質。": "All strategies in this round loaded and generated successfully, but speed, resources, and fixed-set results differed. Compatibility records must retain both execution status and numerical quality.",
    "閱讀效能比較 →": "Read the performance comparison →"
  };

  const PERFORMANCE = {
    "Tanpopo MLX 與 GGUF 轉換的載入速度及運算精確度": "Tanpopo MLX and GGUF Conversion Loading Speed and Computational Accuracy",
    "Tanpopo 在 Apple M4 Pro 上比較原生 MLX 與三種 MLX+GGUF 策略的速度、磁碟、RAM 與精確度。": "Tanpopo compares speed, storage, RAM, and accuracy for native MLX and three MLX+GGUF strategies on Apple M4 Pro.",
    "MLX 與 GGUF 轉換的": "MLX and GGUF Conversion",
    "載入速度及運算精確度": "Loading Speed and Computational Accuracy",
    "以 4B、9B、27B 三組配對模型重新比較原生MLX檔案未轉換、fastGGUF 開啟、fastGGUF 關閉及 llama+GGUF。速度接近原生MLX檔案未轉換 並不足夠；本報告也以固定 100 題驗證輸出品質。": "Three paired 4B, 9B, and 27B models compare native MLX, fastGGUF on, fastGGUF off, and llama+GGUF. Approaching native MLX speed is not enough; a fixed 100-question set also validates output quality.",
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
    "fastGGUF 達到原生MLX檔案未轉換": "fastGGUF relative to native MLX",
    "三個樣本分別為 92.6%、102.4%、90.3%。": "The three samples reached 92.6%, 102.4%, and 90.3%.",
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
    "表內速度與精度均為目前主系統策略的重測結果；Group 自動策略固定使用 64，除非使用者明確指定才會改用 Group 32。": "All speed and accuracy values were retested with the current main-system strategy. Automatic group sizing is fixed at 64; group 32 is used only when explicitly requested.",
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
    "MMLU 固定 100 題；四種模式使用完全相同題目。": "MMLU uses a fixed 100-question set. All four modes use identical questions.",
    "fast 關": "fast off",
    "fast 開": "fast on",
    "無效（關／開）": "Invalid (off / on)",
    "兩者失敗": "Both failed",
    "88.2%／54.4%，皆失敗": "88.2% / 54.4%; both failed",
    "84.6%／0%，皆失敗": "84.6% / 0%; both failed",
    "79.8%／87.6%，皆失敗": "79.8% / 87.6%; both failed",
    "可用；RSS 每 200 ms 取樣，不能視為統一記憶體硬上限。": "is available. RSS is sampled every 200 ms and must not be treated as a hard unified-memory limit.",
    "峰值 RSS": "Peak RSS",
    "INT8 轉換": "INT8 conversion",
    "INT4 轉換": "INT4 conversion",
    "INT8 轉換、不寫快取": "INT8 conversion, no cache write",
    "INT4 轉換、不寫快取": "INT4 conversion, no cache write",
    "原生 GGUF block": "Native GGUF blocks",
    "本模型最高 RSS": "Highest RSS for this model",
    "較快轉換": "Faster conversion",
    "直接載入": "Direct loading",
    "最慢轉換": "Slowest conversion",
    "RSS 未顯著下降": "No material RSS reduction",
    "為隔離轉換與生成效能，MLX+GGUF 此表使用": "To isolate conversion and generation performance, MLX+GGUF in this table uses",
    "的時間。正式產品預設會在首次轉換後保存快取，後續啟動可直接重用；MMap 仍不會消除已轉換 MLX 權重的常駐成本。": "time. The product saves a cache after the first conversion by default so later launches can reuse it. MMap still does not eliminate the resident cost of converted MLX weights.",
    "控制投影提升已納入本輪 fastGGUF 開啟測試，但不能單獨保證模型品質。": "Control-projection promotion is included in this round of fastGGUF-enabled tests, but cannot guarantee model quality by itself.",
    "recurrent controls 仍需配合模型級品質驗證": "Recurrent controls still require model-level quality validation",
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
    "；原生MLX檔案未轉換模式未傳入": "; Native MLX File, Unconverted mode does not",
    "KV cache 上限 4096 tokens": "KV cache limited to 4096 tokens",
    "未啟用 DFlash 與 KV Cache 量化": "DFlash and KV-cache quantization disabled",
    "llama：Metal 全層、Flash Attention、8 threads": "llama: all layers on Metal, Flash Attention, 8 threads",
    "解讀限制": "Interpretation limits",
    "速度量測以": "Speed measurements use",
    "排除快取壓縮與寫入": "to exclude cache compression and writes",
    "程序 RSS 不等於系統統一記憶體總量": "Process RSS is not total system unified-memory use",
    "Runtime 或 Metal 更新後應重新測試": "Retest after runtime or Metal updates",
    "速度彙總：": "Speed summary:",
    "· 精度彙總：": "· Accuracy summary:",
    "· 重現腳本：": "· Reproduction scripts:",
    "若有可信原生 MLX checkpoint，這仍是本輪速度、載入、RSS 與精度最完整的路徑。": "When a trustworthy native MLX checkpoint exists, it remains the most complete path in this round for speed, loading, RSS, and accuracy.",
    "llama+GGUF 在三模型都維持有效答案與合理精度，且不需支付 MLX 重新量化的載入成本。": "llama+GGUF maintained valid answers and reasonable accuracy for all three models without the MLX requantization load cost.",
    "fastGGUF 通過閘門後使用": "Use fastGGUF after it passes the gate",
    "下一步是數值合約": "Next: numerical contracts",
    "建立逐層漂移、logit 一致性、敏感 tensor 提升與自動回退，讓策略依 tensor／架構能力決定，而非模型名稱。": "Add per-layer drift checks, logit agreement, sensitive-tensor promotion, and automatic fallback so strategy follows tensor and architecture capabilities rather than model names.",
    "← 閱讀模型相容性": "← Read model compatibility",
    "圖例": "Legend",
    "三個模型在四種模式的生成速度長條圖": "Generation-speed bar chart for three models across four modes",
    "900 次": "900 questions",
    "品質通過，速度視來源格式而定": "Quality passed; speed depends on source format",
    "三個樣本分別為 84.6%、55.6%、56.6%。": "The three samples reached 84.6%, 55.6%, and 56.6%.",
    "低位元來源保守重新量化為 INT8": "Conservatively requantizes low-bit sources to INT8",
    "保守基準，但不是無損模式": "Conservative baseline, not lossless",
    "可直接沿用的 Q4_0／Q4_1 保留 INT4；K-Quant／IQ 二次量化至少 INT8": "Q4_0/Q4_1 retain INT4 when directly reusable; secondary K-Quant/IQ quantization uses at least INT8",
    "可評分（開／關）": "Evaluable (on / off)",
    "105.9%／108.4%，皆通過": "105.9% / 108.4%; both pass",
    "108.4%／105.9%，皆通過": "108.4% / 105.9%; both pass",
    "91.1%／91.1%，皆通過": "91.1% / 91.1%; both pass",
    "97.8%／97.8%，皆通過": "97.8% / 97.8%; both pass",
    "且尚未出現可解析答案時才排除分母，模型自行停止卻未作答仍算錯。 本輪 6 個 MLX+GGUF 組合全部通過；通過只代表本樣本可採用較快策略，不構成其他 GGUF 的保證。": "without a parseable answer is excluded from the denominator. A model that stops without answering is still wrong. All six MLX+GGUF combinations passed; passing only permits the faster strategy for these samples and does not guarantee other GGUF models.",
    "INT8 永久快取命中": "Persistent INT8 cache hit",
    "直接保留 Q4_0 INT4": "Direct Q4_0 INT4 preservation",
    "INT8 永久快取命中／MMap": "Persistent INT8 cache hit / MMap",
    "MLX+GGUF 載入數字是在永久": "MLX+GGUF load numbers were measured after the persistent",
    "Recurrent controls 策略判定": "Recurrent-controls strategy decision",
    "控制投影提升保留為進階選項，但不再隨 fastGGUF 預設開啟。": "Control-projection promotion remains an advanced option but is no longer enabled by fastGGUF by default.",
    "預設關閉 recurrent BF16 提升": "Recurrent BF16 promotion is disabled by default",
    "Ornith 在 K-Quant→INT8 下，將 recurrent 控制張量升為 BF16 得到 69／100，低於 recurrent 關閉的 72／100；因此正式快速模式採": "With K-Quant converted to INT8, promoting recurrent control tensors to BF16 scored 69/100 on Ornith, below 72/100 with recurrent promotion disabled. Production fast mode therefore uses",
    "。這不是 Ornith 特例，而是「未證實普遍有益就不預設 改變控制張量」的通用原則；仍可用": ". This is not an Ornith special case; it is the generic rule that control tensors are not changed by default without evidence of broad benefit. You can still use",
    "手動診斷。": "for manual diagnostics.",
    "歷史 POC：": "Historical POC:",
    "；主系統預設為": "; main-system default:",
    "關閉 thinking；輸出上限 512 tokens、單題逾時 600～900 秒": "Thinking disabled; 512-token output cap; 600-900 second per-question timeout",
    "只接受獨立 A、B、C、D；length 且尚無答案才排除": "Only standalone A, B, C, or D is valid; length without an answer is excluded",
    "MLX+GGUF 速度量測使用已完成的永久快取；首次轉換另列限制": "MLX+GGUF speed uses completed persistent caches; first conversion is documented separately",
    "有原生 MLX 時仍優先比較": "Compare native MLX when available",
    "依來源量化選擇": "Select by source quantization",
    "Q4_0／Q4_1 可直接保留 INT4；K-Quant／IQ 若需二次量化至少使用 INT8，不能為追速度硬降位元。": "Q4_0/Q4_1 can preserve INT4 directly; K-Quant/IQ requiring secondary quantization uses at least INT8 and is not forced to a lower bit-width for speed.",
    "三個模型在三種 MLX 模式的 MMLU 精確度長條圖": "MMLU accuracy bar chart for three models across three MLX modes",
    "三個模型在四種模式的 MMLU 精確度長條圖": "MMLU accuracy bar chart for three models across four modes",
    "以同一份 Ornith 1.5 9B Q4_K_M 比較 MLX + 預設快速轉換、MLX + Beta 1 與 MLX + Beta 2 三種策略，並以原生 MLX 作為品質與速度基準。速度、磁碟與 RAM 都必須和固定 100 題品質一起判讀。": "Using the same Ornith 1.5 9B Q4_K_M, this report compares MLX + Default Fast Conversion, MLX + Beta 1, and MLX + Beta 2 against native MLX as the quality and speed baseline. Speed, storage, and RAM are reported alongside a fixed 100-question quality set.",
    "完整策略模型": "Full-strategy model",
    "1 組": "1 pair",
    "12 次": "12 runs",
    "400 次": "400 questions",
    "四種模式在同一台主機串行執行；同一時間只啟動一個 mlx-server。": "All four modes ran serially on the same host, with only one mlx-server active at a time.",
    "測試前以": "Before testing,",
    "與": "and",
    "確認沒有其他 mlx-server／llama-server；正式速度量測前 load average 已降至 1.97，原生 MLX 結果也與先前乾淨基準一致。正式吞吐採三次中位數降低偶發背景負載影響。": "confirmed that no other mlx-server or llama-server was running. Load average had fallen to 1.97 before formal speed measurements, and the native MLX result matched the earlier clean baseline. Formal throughput uses the median of three runs to reduce incidental background-load effects.",
    "MLX+GGUF 三策略": "Three MLX+GGUF strategies",
    "Group 自動策略固定使用 64，除非明確指定才使用 Group 32；策略判定依來源 block 與 tensor metadata，不依模型名稱。": "Automatic group sizing is fixed at 64; group 32 is used only when explicitly specified. Strategy decisions use source blocks and tensor metadata rather than model names.",
    "預設": "Default",
    "K-Quant 重新量化為 INT8；auto 固定 group 64": "K-Quant requantized to INT8; auto fixed at group 64",
    "INT8 轉換路徑": "INT8 conversion path",
    "Q4_K 沿用來源 4-bit block；其餘張量 group 32": "Q4_K reuses source 4-bit blocks; other tensors use group 32",
    "全域 Group 32 對照": "Global group 32 comparison",
    "Q4_K 沿用來源 4-bit block；其餘張量 group 64": "Q4_K reuses source 4-bit blocks; other tensors use group 64",
    "混合 Group 對照": "Mixed-group comparison",
    "Q4_K 沿用時每個 32 元素 sub-block 的 group 由格式決定；Beta 1／Beta 2 的差異只作用於其他需要重新量化的張量。": "When Q4_K is reused, each 32-element sub-block's group is defined by the format. The Beta 1/Beta 2 difference applies only to other tensors requiring requantization.",
    "Ornith 1.5 9B 在四種模式的生成速度長條圖": "Generation-speed bar chart for Ornith 1.5 9B across four modes",
    "比較 MLX + 預設快速轉換": "Compared with MLX + Default Fast Conversion",
    "Ornith 1.5 9B 在四種模式的 MMLU 精確度長條圖": "MMLU accuracy bar chart for Ornith 1.5 9B across four modes",
    "可評分題數": "Evaluable questions",
    "計分與排除規則": "Scoring and exclusion rules",
    "已產生 A～D 的回覆一律正常計分；只有": "Responses containing A-D are scored normally; only",
    "且尚未出現可解析答案時才排除分母，模型自行停止卻未作答仍算錯。 本輪只有 Beta 2 的 1 題符合排除條件，其餘 399 次問答均納入精確度計算。": "without a parseable answer is excluded from the denominator. A model that stops without answering is still counted wrong. Only one Beta 2 question met the exclusion rule; the other 399 responses were included in accuracy calculations.",
    "磁碟占用與程序 RAM": "Storage and process RAM",
    "磁碟分開列出原始模型與永久轉換快取；RAM 每 200 ms 取樣，列出程序生命週期峰值與生成期間平均值。": "Storage lists the source model and persistent conversion cache separately. RAM is sampled every 200 ms, reporting process-lifetime peak and generation-period average.",
    "原始模型": "Source model",
    "轉換快取": "Conversion cache",
    "RAM 峰值": "Peak RAM",
    "RAM 平均": "Average RAM",
    "無轉換快取": "No conversion cache",
    "RAM 最低，快取最大": "Lowest RAM, largest cache",
    "各方面表現均衡": "Balanced across measured dimensions",
    "GGUF 策略中快取最小": "Smallest cache among GGUF strategies",
    "使用 MMap 技術降低 RAM 用量，原生 MLX 目前尚未支援。": "MMap reduces RAM usage; native MLX is not currently supported.",
    "Recurrent controls 量測範圍": "Recurrent-controls measurement scope",
    "本輪三種 GGUF 策略均使用相同的 recurrent controls 設定，避免額外變因。": "All three GGUF strategies use the same recurrent-controls setting in this round to avoid an additional variable.",
    "固定 controls 設定": "Fixed controls setting",
    "MLX + 預設快速轉換、MLX + Beta 1 與 MLX + Beta 2 均傳入": "MLX + Default Fast Conversion, MLX + Beta 1, and MLX + Beta 2 all receive",
    "； 本節只記錄測試條件，不把 recurrent promotion 的效果和三種儲存策略混為同一項比較。": "; this section records the test condition without combining recurrent-promotion effects with the three storage strategies.",
    "原生 MLX 與 MLX+GGUF 均使用相同 Release build": "Native MLX and MLX+GGUF use the same Release build",
    "MLX+GGUF 速度量測使用已完成的永久快取": "MLX+GGUF speed measurements use completed persistent caches",
    "本輪完整四策略比較為單一模型樣本，不代表所有 GGUF 的固定結果": "This round's complete four-strategy comparison is a single-model sample and does not represent a fixed result for every GGUF",
    "Qwen 3.5 4B Q4_0 另以 20 題確認改動前後逐題一致": "Qwen 3.5 4B Q4_0 used an additional 20 questions to confirm identical per-question results before and after the change",
    "策略量測彙總：": "Strategy measurement summary:",
    "結果補充": "Result notes",
    "原生 MLX 基準": "Native MLX baseline",
    "本輪原生 MLX 為 48.863 tok/s、79／100，並占用 4.711 GiB 模型磁碟與 4.897 GiB 程序 RAM。": "Native MLX measured 48.863 tok/s and 79/100, using 4.711 GiB of model storage and 4.897 GiB of process RAM.",
    "三種 GGUF 結果": "Three GGUF results",
    "MLX + 預設快速轉換、MLX + Beta 1、MLX + Beta 2 分別為 27.720、36.794、37.678 tok/s；正確題數為 69／100、68／100、67／99。": "MLX + Default Fast Conversion, MLX + Beta 1, and MLX + Beta 2 measured 27.720, 36.794, and 37.678 tok/s, with 69/100, 68/100, and 67/99 correct answers.",
    "來源量化差異": "Source-quantization differences",
    "MLX + 預設快速轉換將 K-Quant 重新量化為 INT8；MLX + Beta 1／MLX + Beta 2 沿用 Q4_K 來源 block，其他張量分別使用 Group 32／64。": "MLX + Default Fast Conversion requantizes K-Quant to INT8. MLX + Beta 1 and MLX + Beta 2 reuse Q4_K source blocks, with other tensors using group 32 and 64 respectively.",
    "適用範圍": "Scope",
    "結果只代表本次模型、Runtime 與測試條件；其他架構、量化格式或 checkpoint 需另行量測。": "Results apply only to this model, runtime, and test conditions. Other architectures, quantization formats, and checkpoints require separate measurement."
  };

  const reportName = document.body.dataset.report;
  const catalog = { ...COMMON, ...(reportName === "performance" ? PERFORMANCE : COMPATIBILITY) };
  const textNodes = [];
  const attributes = [];

  // 報告日期取自目前文件的 Last-Modified；本機無可靠值時才退回今日。
  // 讓 GitHub Pages 每次更新報告後自動反映部署日期，不必手動改 HTML。
  const modified = new Date(document.lastModified);
  const reportDate = Number.isNaN(modified.getTime()) ? new Date() : modified;
  const dateText = [
    reportDate.getFullYear(),
    String(reportDate.getMonth() + 1).padStart(2, "0"),
    String(reportDate.getDate()).padStart(2, "0")
  ].join(".");
  document.querySelectorAll("[data-report-date]").forEach((element) => {
    element.textContent = dateText;
    element.setAttribute("datetime", dateText.replaceAll(".", "-"));
  });

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
      ? "Tanpopo MLX 與 GGUF 轉換的載入速度及運算精確度"
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
