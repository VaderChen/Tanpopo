package domain

import "time"

const (
	RuntimeLlamaServer = "llama-server"
	RuntimeMLXServer   = "mlx-server"
)

// AgentConfig 是服務啟動階段使用的設定；變更後需重新啟動服務。
type AgentConfig struct {
	ServiceName         string `json:"service_name"`
	HTTPHost            string `json:"http_host"`
	HTTPPort            int    `json:"http_port"`
	WebPath             string `json:"web_path"`
	SettingsPath        string `json:"settings_path"`
	StartupCommandsPath string `json:"startup_commands_path"`
	AccessControlPath   string `json:"access_control_path"`
	DefaultAccount      string `json:"default_account"`
	DefaultPassword     string `json:"default_pwd"`
	SessionHours        int    `json:"session_hours"`
}

// Settings 是管理畫面可即時保存的模型目錄與 Hugging Face 設定。
type Settings struct {
	ModelDirectory      string   `json:"model_directory"`
	MLXModelDirectory   string   `json:"mlx_model_directory"`
	HuggingFaceEndpoint string   `json:"huggingface_endpoint"`
	HuggingFaceToken    string   `json:"huggingface_token,omitempty"`
	DefaultRevision     string   `json:"default_revision"`
	ServerHost          string   `json:"server_host"`
	ServerPort          int      `json:"server_port"`
	ContextSize         int      `json:"context_size"`
	GPULayers           int      `json:"gpu_layers"`
	Threads             int      `json:"threads"`
	ExtraArgs           []string `json:"extra_args"`
}

// PublicSettings 排除 Hugging Face Token 明文，僅回傳是否已設定。
type PublicSettings struct {
	ModelDirectory      string `json:"model_directory"`
	MLXModelDirectory   string `json:"mlx_model_directory"`
	HuggingFaceEndpoint string `json:"huggingface_endpoint"`
	HuggingFaceTokenSet bool   `json:"huggingface_token_set"`
	DefaultRevision     string `json:"default_revision"`
}

type ModelFile struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
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
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Runtime     string    `json:"runtime"`
	DraftModel  string    `json:"draft_model,omitempty"`
	ServerHost  string    `json:"server_host"`
	ServerPort  int       `json:"server_port"`
	ContextSize int       `json:"context_size"`
	GPULayers   int       `json:"gpu_layers"`
	Threads     int       `json:"threads"`
	ExtraArgs   []string  `json:"extra_args"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type LlamaStatus struct {
	Running            bool      `json:"running"`
	Runtime            string    `json:"runtime"`
	PID                int       `json:"pid,omitempty"`
	Model              string    `json:"model,omitempty"`
	MMProj             string    `json:"mmproj,omitempty"`
	Binary             string    `json:"binary,omitempty"`
	StartupCommandID   string    `json:"startup_command_id,omitempty"`
	StartupCommandName string    `json:"startup_command_name,omitempty"`
	URL                string    `json:"url"`
	StartedAt          time.Time `json:"started_at,omitempty"`
	StoppedAt          time.Time `json:"stopped_at,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
}
