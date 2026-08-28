package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"LlamaLoader/src/domain"
)

func DefaultAgentConfig() domain.AgentConfig {
	return domain.AgentConfig{
		ServiceName:         "Llama Loader",
		HTTPHost:            "0.0.0.0",
		HTTPPort:            10082,
		WebPath:             "./website",
		SettingsPath:        "./data/settings.json",
		StartupCommandsPath: "./data/startup_commands.json",
		AccessControlPath:   "./data/access_control.json",
		DefaultAccount:      "admin",
		DefaultPassword:     "change-me",
		SessionHours:        24,
	}
}

func DefaultSettings() domain.Settings {
	homeDirectory, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDirectory) == "" {
		homeDirectory = "."
	}
	return domain.Settings{
		ModelDirectory:      filepath.Join(homeDirectory, "services", "models"),
		MLXModelDirectory:   filepath.Join(homeDirectory, "services", "mlx-models"),
		HuggingFaceEndpoint: "https://huggingface.co",
		DefaultRevision:     "main",
		ServerHost:          "0.0.0.0",
		ServerPort:          8080,
		ContextSize:         256 * 1024,
		GPULayers:           -1,
		Threads:             0,
		ExtraArgs:           []string{},
	}
}

func EnsureAgentConfig(path, samplePath string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	content, err := os.ReadFile(samplePath)
	if err != nil {
		return fmt.Errorf("讀取範例設定失敗: %w", err)
	}
	return writeFileAtomic(path, content, 0600)
}

func LoadAgentConfig(path string) (domain.AgentConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return domain.AgentConfig{}, err
	}
	result := DefaultAgentConfig()
	if err := json.Unmarshal(content, &result); err != nil {
		return domain.AgentConfig{}, fmt.Errorf("解析 %s 失敗: %w", path, err)
	}
	if err := validateAgent(result); err != nil {
		return domain.AgentConfig{}, err
	}
	return result, nil
}

func validateAgent(value domain.AgentConfig) error {
	if strings.TrimSpace(value.DefaultAccount) == "" || value.DefaultPassword == "" {
		return errors.New("管理帳號與密碼不可為空")
	}
	if value.HTTPPort < 1 || value.HTTPPort > 65535 {
		return errors.New("http_port 必須介於 1 到 65535")
	}
	if strings.TrimSpace(value.WebPath) == "" || strings.TrimSpace(value.SettingsPath) == "" || strings.TrimSpace(value.StartupCommandsPath) == "" || strings.TrimSpace(value.AccessControlPath) == "" {
		return errors.New("web_path、settings_path、startup_commands_path 與 access_control_path 不可為空")
	}
	if value.SessionHours < 1 || value.SessionHours > 24*30 {
		return errors.New("session_hours 必須介於 1 到 720")
	}
	return nil
}

type Store struct {
	mu       sync.RWMutex
	path     string
	settings domain.Settings
}

func NewStore(path string) (*Store, error) {
	store := &Store{path: path, settings: DefaultSettings()}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := store.Save(store.settings); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(content, &store.settings); err != nil {
		return nil, fmt.Errorf("解析 %s 失敗: %w", path, err)
	}
	store.settings = normalizeSettings(store.settings)
	if err := ValidateSettings(store.settings); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Get() domain.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := s.settings
	result.ExtraArgs = append([]string(nil), s.settings.ExtraArgs...)
	return result
}

func (s *Store) Public() domain.PublicSettings {
	value := s.Get()
	return domain.PublicSettings{
		ModelDirectory:      value.ModelDirectory,
		MLXModelDirectory:   value.MLXModelDirectory,
		HuggingFaceEndpoint: value.HuggingFaceEndpoint,
		HuggingFaceTokenSet: value.HuggingFaceToken != "",
		DefaultRevision:     value.DefaultRevision,
	}
}

func (s *Store) Save(value domain.Settings) error {
	value = normalizeSettings(value)
	if err := ValidateSettings(value); err != nil {
		return err
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := writeFileAtomic(s.path, content, 0600); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = value
	s.mu.Unlock()
	return nil
}

func normalizeSettings(value domain.Settings) domain.Settings {
	value.ModelDirectory = expandHome(strings.TrimSpace(value.ModelDirectory))
	value.MLXModelDirectory = expandHome(strings.TrimSpace(value.MLXModelDirectory))
	defaults := DefaultSettings()
	if value.MLXModelDirectory == "" {
		value.MLXModelDirectory = defaults.MLXModelDirectory
	}
	value.HuggingFaceEndpoint = strings.TrimRight(strings.TrimSpace(value.HuggingFaceEndpoint), "/")
	value.HuggingFaceToken = strings.TrimSpace(value.HuggingFaceToken)
	value.DefaultRevision = strings.TrimSpace(value.DefaultRevision)
	value.ServerHost = strings.TrimSpace(value.ServerHost)
	if value.HuggingFaceEndpoint == "" {
		value.HuggingFaceEndpoint = "https://huggingface.co"
	}
	if value.DefaultRevision == "" {
		value.DefaultRevision = "main"
	}
	if value.ServerHost == "" {
		value.ServerHost = "0.0.0.0"
	}
	if value.ExtraArgs == nil {
		value.ExtraArgs = []string{}
	}
	cleanArgs := value.ExtraArgs[:0]
	for _, arg := range value.ExtraArgs {
		if trimmed := strings.TrimSpace(arg); trimmed != "" {
			cleanArgs = append(cleanArgs, trimmed)
		}
	}
	value.ExtraArgs = cleanArgs
	return value
}

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	homeDirectory, err := os.UserHomeDir()
	if err != nil || homeDirectory == "" {
		return path
	}
	if path == "~" {
		return homeDirectory
	}
	return filepath.Join(homeDirectory, path[2:])
}

func ValidateSettings(value domain.Settings) error {
	if strings.TrimSpace(value.ModelDirectory) == "" {
		return errors.New("model_directory 不可為空")
	}
	if strings.TrimSpace(value.MLXModelDirectory) == "" {
		return errors.New("mlx_model_directory 不可為空")
	}
	if value.ServerPort < 1 || value.ServerPort > 65535 {
		return errors.New("server_port 必須介於 1 到 65535")
	}
	if value.ContextSize < 128 || value.ContextSize > 1048576 {
		return errors.New("context_size 必須介於 128 到 1048576")
	}
	if value.GPULayers < -1 {
		return errors.New("gpu_layers 不可小於 -1")
	}
	if value.Threads < 0 {
		return errors.New("threads 不可小於 0")
	}
	if !strings.HasPrefix(value.HuggingFaceEndpoint, "https://") && !strings.HasPrefix(value.HuggingFaceEndpoint, "http://") {
		return errors.New("huggingface_endpoint 僅支援 http 或 https")
	}
	for _, arg := range value.ExtraArgs {
		if strings.ContainsAny(arg, "\r\n\x00") {
			return errors.New("extra_args 不可包含換行或 NUL 字元")
		}
	}
	return nil
}

func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".llamaloader-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}
