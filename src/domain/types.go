package domain

import "time"

const (
	RuntimeLlamaServer = "llama-server"
	RuntimeMLXServer   = "mlx-server"

	// Mode 1：K-quant super-block 沿用來源 4-bit block，速度約 +36%，為預設。
	// Mode 2：低位元來源重新量化為 INT8／group 64，保守路徑。
	FastGGUFStrategyMode1 = "mode1"
	FastGGUFStrategyMode2 = "mode2"
	// Mode 3：一律重新量化為 INT4／group 64，頻寬最低。
	FastGGUFStrategyMode3 = "mode3"

	// 舊設定值：default 對應保守路徑；beta1／beta2 都併入 Mode 1。
	FastGGUFStrategyLegacyDefault = "default"
	FastGGUFStrategyLegacyBeta1   = "beta1"
	FastGGUFStrategyLegacyBeta2   = "beta2"

	KVCacheQuantizationNone = ""
	KVCacheQuantizationQ8   = "q8"
	KVCacheQuantizationQ4   = "q4"

	ModelPreparationLoading       = "loading"
	ModelPreparationCheckingCache = "checking_cache"
	ModelPreparationConverting    = "converting"
	ModelPreparationSavingCache   = "saving_cache"
	ModelPreparationLoadingCache  = "loading_cache"
	ModelPreparationDirectLoading = "direct_loading"
)

// AgentConfig 是服務啟動階段使用的設定；變更後需重新啟動服務。
type AgentConfig struct {
	ServiceName           string `json:"service_name"`
	HTTPHost              string `json:"http_host"`
	HTTPPort              int    `json:"http_port"`
	WebPath               string `json:"web_path"`
	SettingsPath          string `json:"settings_path"`
	StartupCommandsPath   string `json:"startup_commands_path"`
	AccessControlPath     string `json:"access_control_path"`
	RuntimeStatePath      string `json:"runtime_state_path"`
	DefaultAccount        string `json:"default_account"`
	DefaultPassword       string `json:"default_pwd"`
	DisableAuthentication bool   `json:"disable_authentication"`
	SessionHours          int    `json:"session_hours"`
}

// Settings 是管理畫面可即時保存的模型目錄與 Hugging Face 設定。
type Settings struct {
	ModelDirectory          string   `json:"model_directory"`
	MLXModelDirectory       string   `json:"mlx_model_directory"`
	ResidentMode            bool     `json:"resident_mode"`
	DefaultFastGGUFEnabled  bool     `json:"default_fast_gguf_enabled"`
	DefaultFastGGUFStrategy string   `json:"default_fast_gguf_strategy"`
	DefaultKVCacheEnabled   bool     `json:"default_kv_cache_quantization_enabled"`
	DefaultMMapEnabled      bool     `json:"default_mmap_enabled"`
	DefaultDFlashEnabled    bool     `json:"default_dflash_enabled"`
	UILanguage              string   `json:"ui_language"`
	UITheme                 string   `json:"ui_theme"`
	HuggingFaceEndpoint     string   `json:"huggingface_endpoint"`
	HuggingFaceToken        string   `json:"huggingface_token,omitempty"`
	DefaultRevision         string   `json:"default_revision"`
	ServerHost              string   `json:"server_host"`
	ServerPort              int      `json:"server_port"`
	ContextSize             int      `json:"context_size"`
	GPULayers               int      `json:"gpu_layers"`
	Threads                 int      `json:"threads"`
	ExtraArgs               []string `json:"extra_args"`
}

// PublicSettings 排除 Hugging Face Token 明文，僅回傳是否已設定。
type PublicSettings struct {
	ModelDirectory          string `json:"model_directory"`
	MLXModelDirectory       string `json:"mlx_model_directory"`
	ResidentMode            bool   `json:"resident_mode"`
	DefaultFastGGUFEnabled  bool   `json:"default_fast_gguf_enabled"`
	DefaultFastGGUFStrategy string `json:"default_fast_gguf_strategy"`
	DefaultKVCacheEnabled   bool   `json:"default_kv_cache_quantization_enabled"`
	DefaultMMapEnabled      bool   `json:"default_mmap_enabled"`
	DefaultDFlashEnabled    bool   `json:"default_dflash_enabled"`
	UILanguage              string `json:"ui_language"`
	UITheme                 string `json:"ui_theme"`
	HuggingFaceEndpoint     string `json:"huggingface_endpoint"`
	HuggingFaceTokenSet     bool   `json:"huggingface_token_set"`
	DefaultRevision         string `json:"default_revision"`
}

type ModelFile struct {
	Path                 string    `json:"path"`
	Format               string    `json:"format,omitempty"`
	Size                 int64     `json:"size"`
	ModifiedAt           time.Time `json:"modified_at"`
	Architecture         string    `json:"architecture,omitempty"`
	RuntimeUntested      bool      `json:"runtime_untested,omitempty"`
	DFlashSupported      bool      `json:"dflash_supported"`
	DFlashDraft          bool      `json:"dflash_draft"`
	DFlashVariant        string    `json:"dflash_variant,omitempty"`
	ConversionCached     bool      `json:"conversion_cached,omitempty"`
	ConversionCacheBytes int64     `json:"conversion_cache_bytes,omitempty"`
	ConversionCacheCount int       `json:"conversion_cache_count,omitempty"`
}

type DownloadJob struct {
	ID          string    `json:"id"`
	Runtime     string    `json:"runtime"`
	Repository  string    `json:"repository"`
	Filename    string    `json:"filename"`
	Revision    string    `json:"revision"`
	Destination string    `json:"destination"`
	State       string    `json:"state"`
	BytesDone   int64     `json:"bytes_done"`
	BytesTotal  int64     `json:"bytes_total"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// StartupCommand 是可重複使用的模型 Runtime 啟動參數組合。
// 實際啟動時才會由後端與選定的 GGUF 或 MLX 模型動態組合命令列。
type StartupCommand struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Runtime             string    `json:"runtime"`
	DraftModel          string    `json:"draft_model,omitempty"`
	ServerHost          string    `json:"server_host"`
	ServerPort          int       `json:"server_port"`
	ContextSize         int       `json:"context_size"`
	GPULayers           int       `json:"gpu_layers"`
	Threads             int       `json:"threads"`
	MMapReserveGB       int       `json:"mmap_reserve_gb"`
	KVCacheQuantization string    `json:"kv_cache_quantization"`
	ExtraArgs           []string  `json:"extra_args"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type LlamaStatus struct {
	Running                 bool      `json:"running"`
	Ready                   bool      `json:"ready"`
	DesiredRunning          bool      `json:"desired_running"`
	Runtime                 string    `json:"runtime"`
	PID                     int       `json:"pid,omitempty"`
	Model                   string    `json:"model,omitempty"`
	MMProj                  string    `json:"mmproj,omitempty"`
	DraftModel              string    `json:"draft_model,omitempty"`
	DFlashEnabled           bool      `json:"dflash_enabled"`
	MMapEnabled             bool      `json:"mmap_enabled"`
	FastGGUF                bool      `json:"fast_gguf"`
	SkipGGUFConversionCache bool      `json:"skip_gguf_conversion_cache"`
	ModelPreparation        string    `json:"model_preparation,omitempty"`
	ModelPreparationDone    int64     `json:"model_preparation_completed_bytes,omitempty"`
	ModelPreparationTotal   int64     `json:"model_preparation_total_bytes,omitempty"`
	ModelPreparationPercent int       `json:"model_preparation_progress_percent,omitempty"`
	ModelPreparationKnown   bool      `json:"model_preparation_progress_determinate"`
	MMapReserveGB           int       `json:"mmap_reserve_gb"`
	KVCacheQuantization     string    `json:"kv_cache_quantization,omitempty"`
	Binary                  string    `json:"binary,omitempty"`
	StartupCommandID        string    `json:"startup_command_id,omitempty"`
	StartupCommandName      string    `json:"startup_command_name,omitempty"`
	URL                     string    `json:"url"`
	StartedAt               time.Time `json:"started_at,omitempty"`
	StoppedAt               time.Time `json:"stopped_at,omitempty"`
	LastError               string    `json:"last_error,omitempty"`
}

// ModelConversionPreflight 是 mlx-server 對 GGUF 永久轉換快取的啟動前判斷。
// CacheKey 同時作為使用者確認憑證；來源檔或策略若在確認後改變，啟動端會
// 因 key 不同而要求重新確認，避免未授權的硬碟寫入。
type ModelConversionPreflight struct {
	Applicable          bool   `json:"applicable"`
	RequiresConversion  bool   `json:"requires_conversion"`
	CacheHit            bool   `json:"cache_hit"`
	EstimatedCacheBytes int64  `json:"estimated_cache_bytes"`
	CacheDirectory      string `json:"cache_directory,omitempty"`
	CacheKey            string `json:"cache_key,omitempty"`
	Model               string `json:"model,omitempty"`
}
