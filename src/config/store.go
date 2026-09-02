package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"LlamaLoader/src/domain"
)

const (
	maxDownloadFavorites       = 64
	maxPerformanceCalibrations = 128
)

var downloadFavoriteRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

func DefaultAgentConfig() domain.AgentConfig {
	return domain.AgentConfig{
		ServiceName:           "Tanpopo",
		HTTPHost:              "0.0.0.0",
		HTTPPort:              10082,
		WebPath:               "./website",
		SettingsPath:          "./data/settings.json",
		StartupCommandsPath:   "./data/startup_commands.json",
		AccessControlPath:     "./data/access_control.json",
		RuntimeStatePath:      "./data/runtime_state.json",
		DefaultAccount:        "root",
		DefaultPassword:       "root",
		DisableAuthentication: false,
		SessionHours:          24,
	}
}

func DefaultSettings() domain.Settings {
	homeDirectory, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDirectory) == "" {
		homeDirectory = "."
	}
	return domain.Settings{
		ModelDirectory:          filepath.Join(homeDirectory, "services", "models"),
		MLXModelDirectory:       filepath.Join(homeDirectory, "services", "mlx-models"),
		DefaultFastGGUFEnabled:  true,
		DefaultFastGGUFStrategy: domain.FastGGUFStrategyMode1,
		DefaultKVCacheEnabled:   false,
		DefaultMMapEnabled:      false,
		DefaultDFlashEnabled:    false,
		RemoveOriginalGGUF:      false,
		AutoCalibrationEnabled:  true,
		MemoryProtectionEnabled: false,
		UILanguage:              "auto",
		UITheme:                 "tanpopo",
		HuggingFaceEndpoint:     "https://huggingface.co",
		DefaultRevision:         "main",
		ServerHost:              "0.0.0.0",
		ServerPort:              8080,
		ContextSize:             256 * 1024,
		GPULayers:               -1,
		Threads:                 0,
		ExtraArgs:               []string{},
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
	if strings.EqualFold(strings.TrimSpace(result.ServiceName), "Llama Loader") ||
		strings.EqualFold(strings.TrimSpace(result.ServiceName), "Open Loader") ||
		strings.EqualFold(strings.TrimSpace(result.ServiceName), "OpenLoader") {
		result.ServiceName = "Tanpopo"
	}
	if err := validateAgent(result); err != nil {
		return domain.AgentConfig{}, err
	}
	return result, nil
}

// UpdateAgentSecurity 以原子寫入方式更新管理介面登入設定。
// 帳號密碼不限制字元種類；停用驗證時仍保留帳密，方便之後重新啟用。
func UpdateAgentSecurity(path string, disableAuthentication bool, account, password string, updatePassword bool) (domain.AgentConfig, error) {
	value, err := LoadAgentConfig(path)
	if err != nil {
		return domain.AgentConfig{}, err
	}
	value.DefaultAccount = strings.TrimSpace(account)
	if updatePassword {
		value.DefaultPassword = password
	}
	value.DisableAuthentication = disableAuthentication
	if err := validateAgent(value); err != nil {
		return domain.AgentConfig{}, err
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return domain.AgentConfig{}, err
	}
	content = append(content, '\n')
	if err := writeFileAtomic(path, content, 0600); err != nil {
		return domain.AgentConfig{}, err
	}
	return value, nil
}

func validateAgent(value domain.AgentConfig) error {
	if strings.TrimSpace(value.DefaultAccount) == "" || value.DefaultPassword == "" {
		return errors.New("管理帳號與密碼不可為空")
	}
	if value.HTTPPort < 1 || value.HTTPPort > 65535 {
		return errors.New("http_port 必須介於 1 到 65535")
	}
	if strings.TrimSpace(value.WebPath) == "" || strings.TrimSpace(value.SettingsPath) == "" || strings.TrimSpace(value.StartupCommandsPath) == "" || strings.TrimSpace(value.AccessControlPath) == "" || strings.TrimSpace(value.RuntimeStatePath) == "" {
		return errors.New("web_path、settings_path、startup_commands_path、access_control_path 與 runtime_state_path 不可為空")
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
	result.DownloadFavorites = append(
		make([]domain.DownloadFavorite, 0, len(s.settings.DownloadFavorites)),
		s.settings.DownloadFavorites...,
	)
	result.PerformanceCalibrations = clonePerformanceCalibrations(s.settings.PerformanceCalibrations)
	return result
}

func (s *Store) Public() domain.PublicSettings {
	value := s.Get()
	return domain.PublicSettings{
		ModelDirectory:          value.ModelDirectory,
		MLXModelDirectory:       value.MLXModelDirectory,
		ResidentMode:            value.ResidentMode,
		DefaultFastGGUFEnabled:  value.DefaultFastGGUFEnabled,
		DefaultFastGGUFStrategy: value.DefaultFastGGUFStrategy,
		DefaultKVCacheEnabled:   value.DefaultKVCacheEnabled,
		DefaultMMapEnabled:      value.DefaultMMapEnabled,
		DefaultDFlashEnabled:    value.DefaultDFlashEnabled,
		RemoveOriginalGGUF:      value.RemoveOriginalGGUF,
		AutoCalibrationEnabled:  value.AutoCalibrationEnabled,
		MemoryProtectionEnabled: value.MemoryProtectionEnabled,
		UILanguage:              value.UILanguage,
		UITheme:                 value.UITheme,
		HuggingFaceEndpoint:     value.HuggingFaceEndpoint,
		HuggingFaceTokenSet:     value.HuggingFaceToken != "",
		DefaultRevision:         value.DefaultRevision,
		DownloadFavorites: append(
			make([]domain.DownloadFavorite, 0, len(value.DownloadFavorites)),
			value.DownloadFavorites...,
		),
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
	value.UILanguage = normalizeUILanguage(value.UILanguage)
	value.UITheme = normalizeUITheme(value.UITheme)
	value.DefaultFastGGUFStrategy = normalizeFastGGUFStrategy(value.DefaultFastGGUFStrategy)
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
	value.DownloadFavorites = normalizeDownloadFavorites(value.DownloadFavorites)
	value.PerformanceCalibrations = normalizePerformanceCalibrations(value.PerformanceCalibrations)
	return value
}

func clonePerformanceCalibrations(values []domain.PerformanceCalibration) []domain.PerformanceCalibration {
	result := make([]domain.PerformanceCalibration, len(values))
	copy(result, values)
	for index := range result {
		result[index].Runs = append([]float64(nil), result[index].Runs...)
	}
	return result
}

func normalizePerformanceCalibrations(values []domain.PerformanceCalibration) []domain.PerformanceCalibration {
	result := make([]domain.PerformanceCalibration, 0, min(len(values), maxPerformanceCalibrations))
	seen := make(map[string]bool)
	for _, calibration := range values {
		calibration.Key = strings.TrimSpace(calibration.Key)
		calibration.HardwareFingerprint = strings.TrimSpace(calibration.HardwareFingerprint)
		calibration.Runtime = strings.ToLower(strings.TrimSpace(calibration.Runtime))
		calibration.Model = filepath.ToSlash(strings.TrimSpace(calibration.Model))
		calibration.StartupCommandID = strings.TrimSpace(calibration.StartupCommandID)
		calibration.StartupCommandFingerprint = strings.TrimSpace(calibration.StartupCommandFingerprint)
		if calibration.Key == "" || seen[calibration.Key] ||
			(calibration.Runtime != domain.RuntimeLlamaServer && calibration.Runtime != domain.RuntimeMLXServer) ||
			calibration.Model == "" || calibration.StartupCommandID == "" ||
			!validPerformanceTuning(calibration.Tuning) ||
			len(calibration.Runs) != 3 || !validCalibrationSpeeds(calibration.Runs) {
			continue
		}
		seen[calibration.Key] = true
		calibration.Runs = append([]float64(nil), calibration.Runs...)
		result = append(result, calibration)
		if len(result) == maxPerformanceCalibrations {
			break
		}
	}
	return result
}

func validPerformanceTuning(value domain.PerformanceTuning) bool {
	return value.Threads >= 0 && value.Threads <= 1024 &&
		value.BatchSize >= 0 && value.BatchSize <= 8192 &&
		value.UBatchSize >= 0 && value.UBatchSize <= 8192 &&
		value.PrefillStepSize >= 0 && value.PrefillStepSize <= 8192 &&
		(value.BatchSize == 0 || value.UBatchSize <= value.BatchSize)
}

func validCalibrationSpeeds(values []float64) bool {
	for _, value := range values {
		if value <= 0 || value > 1_000_000 {
			return false
		}
	}
	return true
}

func normalizeDownloadFavorites(values []domain.DownloadFavorite) []domain.DownloadFavorite {
	result := make([]domain.DownloadFavorite, 0, len(values))
	seen := make(map[string]bool)
	for _, favorite := range values {
		favorite.Runtime = strings.ToLower(strings.TrimSpace(favorite.Runtime))
		favorite.Repository = strings.TrimSpace(favorite.Repository)
		favorite.Revision = strings.TrimSpace(favorite.Revision)
		if favorite.Revision == "" {
			favorite.Revision = "main"
		}
		if favorite.Runtime != domain.RuntimeLlamaServer && favorite.Runtime != domain.RuntimeMLXServer {
			continue
		}
		if !downloadFavoriteRepositoryPattern.MatchString(favorite.Repository) ||
			strings.ContainsAny(favorite.Revision, "\r\n\x00") {
			continue
		}
		key := favorite.Runtime + "\x00" + strings.ToLower(favorite.Repository) + "\x00" + favorite.Revision
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, favorite)
		if len(result) == maxDownloadFavorites {
			break
		}
	}
	return result
}

func normalizeUILanguage(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "zh-hant", "zh-tw", "zh-hk", "traditional-chinese":
		return "zh-Hant"
	case "en", "english":
		return "en"
	case "ja", "jp", "japanese":
		return "ja"
	case "ko", "kr", "korean":
		return "ko"
	default:
		return "auto"
	}
}

func normalizeUITheme(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tanpopo", "ocean", "sakura", "wisteria":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "tanpopo"
	}
}

// normalizeFastGGUFStrategy 同時接受新舊設定值。beta1 實測精度與速度都不如
// beta2，已捨棄並併入 Mode 1；舊的 default 對應保守的 Mode 2。
func normalizeFastGGUFStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case domain.FastGGUFStrategyMode2, domain.FastGGUFStrategyLegacyDefault:
		return domain.FastGGUFStrategyMode2
	case domain.FastGGUFStrategyMode3:
		return domain.FastGGUFStrategyMode3
	default:
		return domain.FastGGUFStrategyMode1
	}
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
	if value.DefaultKVCacheEnabled && value.DefaultDFlashEnabled {
		return errors.New("KV Cache 量化與 DFlash 不可同時設為預設開啟")
	}
	switch value.DefaultFastGGUFStrategy {
	case domain.FastGGUFStrategyMode1, domain.FastGGUFStrategyMode2,
		domain.FastGGUFStrategyMode3, domain.FastGGUFStrategyLegacyDefault,
		domain.FastGGUFStrategyLegacyBeta1, domain.FastGGUFStrategyLegacyBeta2:
	default:
		return errors.New("default_fast_gguf_strategy 只支援 mode1、mode2 或 mode3")
	}
	switch value.UILanguage {
	case "auto", "zh-Hant", "en", "ja", "ko":
	default:
		return errors.New("ui_language 只支援 auto、zh-Hant、en、ja 或 ko")
	}
	switch value.UITheme {
	case "tanpopo", "ocean", "sakura", "wisteria":
	default:
		return errors.New("ui_theme 只支援 tanpopo、ocean、sakura 或 wisteria")
	}
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
	if len(value.DownloadFavorites) > maxDownloadFavorites {
		return fmt.Errorf("download_favorites 最多只能保存 %d 筆", maxDownloadFavorites)
	}
	if len(value.PerformanceCalibrations) > maxPerformanceCalibrations {
		return fmt.Errorf("performance_calibrations 最多只能保存 %d 筆", maxPerformanceCalibrations)
	}
	for _, calibration := range value.PerformanceCalibrations {
		if !validPerformanceTuning(calibration.Tuning) || len(calibration.Runs) != 3 ||
			!validCalibrationSpeeds(calibration.Runs) {
			return errors.New("performance_calibrations 包含無效的校準資料")
		}
	}
	for _, favorite := range value.DownloadFavorites {
		if favorite.Runtime != domain.RuntimeLlamaServer && favorite.Runtime != domain.RuntimeMLXServer {
			return errors.New("download_favorites runtime 格式錯誤")
		}
		if !downloadFavoriteRepositoryPattern.MatchString(favorite.Repository) {
			return errors.New("download_favorites repository 必須為 owner/model 格式")
		}
		if favorite.Revision == "" || strings.ContainsAny(favorite.Revision, "\r\n\x00") {
			return errors.New("download_favorites revision 格式錯誤")
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
