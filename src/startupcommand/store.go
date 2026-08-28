package startupcommand

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"LlamaLoader/src/domain"
)

type fileData struct {
	Version  int                     `json:"version"`
	Commands []domain.StartupCommand `json:"commands"`
}

const (
	currentFileVersion = 5
	defaultContextSize = 256 * 1024
)

type Store struct {
	mu       sync.RWMutex
	path     string
	commands []domain.StartupCommand
}

func NewStore(path string, fallback domain.StartupCommand) (*Store, error) {
	store := &Store{path: strings.TrimSpace(path)}
	if store.path == "" {
		return nil, errors.New("啟動參數設定檔路徑不可為空")
	}
	content, err := os.ReadFile(store.path)
	if os.IsNotExist(err) {
		store.commands = builtinCommands(fallback, time.Now())
		if err := store.persist(store.commands); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var data fileData
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("解析 %s 失敗: %w", store.path, err)
	}
	if data.Version > currentFileVersion {
		return nil, fmt.Errorf("啟動參數設定檔版本 %d 高於目前支援的版本 %d", data.Version, currentFileVersion)
	}
	if len(data.Commands) == 0 {
		return nil, errors.New("啟動參數清單不可為空")
	}
	seenIDs := make(map[string]bool, len(data.Commands))
	seenNames := make(map[string]bool, len(data.Commands))
	for index := range data.Commands {
		data.Commands[index] = normalize(data.Commands[index])
		command := data.Commands[index]
		if err := Validate(command); err != nil {
			return nil, fmt.Errorf("啟動參數 %q 格式錯誤: %w", command.Name, err)
		}
		if seenIDs[command.ID] {
			return nil, fmt.Errorf("啟動參數 ID 重複: %s", command.ID)
		}
		nameKey := strings.ToLower(command.Name)
		if seenNames[nameKey] {
			return nil, fmt.Errorf("啟動參數名稱重複: %s", command.Name)
		}
		seenIDs[command.ID] = true
		seenNames[nameKey] = true
	}
	if data.Version < currentFileVersion {
		now := time.Now()
		for index := range data.Commands {
			if data.Commands[index].ID == "default" {
				data.Commands[index].ContextSize = defaultContextSize
				data.Commands[index].UpdatedAt = now
			}
			if data.Version < 5 && data.Commands[index].ID == "mlx-default" && hasArgument(data.Commands[index].ExtraArgs, "--prompt-cache-size") {
				data.Commands[index].Name = "MLX Apple Silicon（256K）"
				data.Commands[index].Runtime = domain.RuntimeMLXServer
				data.Commands[index].DraftModel = ""
				data.Commands[index].ContextSize = defaultContextSize
				data.Commands[index].ExtraArgs = []string{"--prefill-step-size", "2048"}
				data.Commands[index].UpdatedAt = now
			}
		}
		for _, preset := range builtinCommands(fallback, now)[1:] {
			nameKey := strings.ToLower(preset.Name)
			if seenIDs[preset.ID] || seenNames[nameKey] {
				continue
			}
			data.Commands = append(data.Commands, preset)
			seenIDs[preset.ID] = true
			seenNames[nameKey] = true
		}
		store.commands = data.Commands
		if err := store.persist(store.commands); err != nil {
			return nil, err
		}
		return store, nil
	}
	store.commands = data.Commands
	return store, nil
}

func hasArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func builtinCommands(fallback domain.StartupCommand, now time.Time) []domain.StartupCommand {
	base := fallback
	base.ID = "default"
	base.Name = "預設參數（256K）"
	base.Runtime = domain.RuntimeLlamaServer
	base.DraftModel = ""
	base.ContextSize = defaultContextSize
	base.ExtraArgs = []string{}
	base.CreatedAt = now
	base.UpdatedAt = now
	base = normalize(base)

	presets := []domain.StartupCommand{
		base,
		newPreset(base, "kv-cache-q8", "KV Cache Q8（256K）", []string{
			"--cache-type-k", "q8_0",
			"--cache-type-v", "q8_0",
			"--flash-attn", "on",
		}),
		newPreset(base, "kv-cache-q4", "KV Cache Q4（256K）", []string{
			"--cache-type-k", "q4_0",
			"--cache-type-v", "q4_0",
			"--flash-attn", "on",
		}),
		newPreset(base, "no-reasoning", "強制關閉思考（256K）", []string{
			"--reasoning", "off",
			"--reasoning-budget", "0",
			"--jinja",
		}),
		newPreset(base, "mtp", "MTP（256K）", []string{
			"--spec-type", "draft-mtp",
			"--spec-draft-n-max", "2",
			"--flash-attn", "on",
			"--jinja",
		}),
		newPreset(base, "dflash", "DFlash（256K，需 Draft GGUF）", []string{
			"--spec-type", "draft-dflash",
			"--spec-draft-n-max", "15",
			"--flash-attn", "on",
			"--jinja",
		}),
		{
			ID:          "mlx-default",
			Name:        "MLX Apple Silicon（256K）",
			Runtime:     domain.RuntimeMLXServer,
			ServerHost:  "0.0.0.0",
			ServerPort:  8080,
			ContextSize: defaultContextSize,
			GPULayers:   -1,
			Threads:     0,
			ExtraArgs: []string{
				"--prefill-step-size", "2048",
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:          "mlx-kv-q4",
			Name:        "MLX KV Cache Q4（256K）",
			Runtime:     domain.RuntimeMLXServer,
			ServerHost:  "0.0.0.0",
			ServerPort:  8080,
			ContextSize: defaultContextSize,
			GPULayers:   -1,
			Threads:     0,
			ExtraArgs: []string{
				"--kv-bits", "4",
				"--kv-group-size", "64",
				"--prefill-step-size", "2048",
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:          "mlx-no-thinking",
			Name:        "MLX 強制關閉思考（256K）",
			Runtime:     domain.RuntimeMLXServer,
			ServerHost:  "0.0.0.0",
			ServerPort:  8080,
			ContextSize: defaultContextSize,
			GPULayers:   -1,
			Threads:     0,
			ExtraArgs: []string{
				"--no-thinking",
				"--prefill-step-size", "2048",
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	return presets
}

func newPreset(base domain.StartupCommand, id, name string, extraArgs []string) domain.StartupCommand {
	preset := base
	preset.ID = id
	preset.Name = name
	preset.ExtraArgs = append([]string(nil), extraArgs...)
	return preset
}

func DefaultFromSettings(settings domain.Settings) domain.StartupCommand {
	return domain.StartupCommand{
		Runtime:     domain.RuntimeLlamaServer,
		ServerHost:  settings.ServerHost,
		ServerPort:  settings.ServerPort,
		ContextSize: settings.ContextSize,
		GPULayers:   settings.GPULayers,
		Threads:     settings.Threads,
		ExtraArgs:   append([]string(nil), settings.ExtraArgs...),
	}
}

func (s *Store) List() []domain.StartupCommand {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneCommands(s.commands)
}

func (s *Store) Get(id string) (domain.StartupCommand, error) {
	id = strings.TrimSpace(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, command := range s.commands {
		if command.ID == id {
			return cloneCommand(command), nil
		}
	}
	return domain.StartupCommand{}, errors.New("找不到指定的啟動參數")
}

func (s *Store) Create(command domain.StartupCommand) (domain.StartupCommand, error) {
	id, err := randomID()
	if err != nil {
		return domain.StartupCommand{}, err
	}
	now := time.Now()
	command.ID = id
	command.CreatedAt = now
	command.UpdatedAt = now
	command = normalize(command)
	if err := Validate(command); err != nil {
		return domain.StartupCommand{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureUniqueName(s.commands, command.Name, ""); err != nil {
		return domain.StartupCommand{}, err
	}
	updated := append(cloneCommands(s.commands), command)
	if err := s.persist(updated); err != nil {
		return domain.StartupCommand{}, err
	}
	s.commands = updated
	return cloneCommand(command), nil
}

func (s *Store) Update(id string, command domain.StartupCommand) (domain.StartupCommand, error) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for currentIndex, current := range s.commands {
		if current.ID == id {
			index = currentIndex
			command.ID = current.ID
			command.CreatedAt = current.CreatedAt
			command.UpdatedAt = time.Now()
			break
		}
	}
	if index < 0 {
		return domain.StartupCommand{}, errors.New("找不到指定的啟動參數")
	}
	command = normalize(command)
	if err := Validate(command); err != nil {
		return domain.StartupCommand{}, err
	}
	if err := ensureUniqueName(s.commands, command.Name, id); err != nil {
		return domain.StartupCommand{}, err
	}
	updated := cloneCommands(s.commands)
	updated[index] = command
	if err := s.persist(updated); err != nil {
		return domain.StartupCommand{}, err
	}
	s.commands = updated
	return cloneCommand(command), nil
}

func (s *Store) Delete(id string) error {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.commands) <= 1 {
		return errors.New("至少需要保留一組啟動參數")
	}
	updated := make([]domain.StartupCommand, 0, len(s.commands)-1)
	found := false
	for _, command := range s.commands {
		if command.ID == id {
			found = true
			continue
		}
		updated = append(updated, cloneCommand(command))
	}
	if !found {
		return errors.New("找不到指定的啟動參數")
	}
	if err := s.persist(updated); err != nil {
		return err
	}
	s.commands = updated
	return nil
}

func Validate(command domain.StartupCommand) error {
	if command.ID == "" || len(command.ID) > 64 {
		return errors.New("啟動參數 ID 格式錯誤")
	}
	for _, char := range command.ID {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return errors.New("啟動參數 ID 僅可包含小寫英數字、連字號與底線")
		}
	}
	if command.Name == "" || len([]rune(command.Name)) > 100 || strings.ContainsAny(command.Name, "\r\n\x00") {
		return errors.New("名稱不可為空、不可換行且最多 100 個字元")
	}
	if command.Runtime != domain.RuntimeLlamaServer && command.Runtime != domain.RuntimeMLXServer {
		return errors.New("Runtime 僅支援 llama-server 或 mlx-server")
	}
	if len(command.DraftModel) > 1024 || strings.ContainsAny(command.DraftModel, "\r\n\x00") {
		return errors.New("Draft 模型不可換行且最多 1024 個字元")
	}
	if command.Runtime == domain.RuntimeLlamaServer && command.DraftModel != "" && !strings.EqualFold(filepath.Ext(command.DraftModel), ".gguf") {
		return errors.New("Draft 模型必須是 .gguf 檔案")
	}
	if command.Runtime == domain.RuntimeMLXServer && command.DraftModel != "" {
		return errors.New("MLX Runtime 目前不使用 Draft GGUF")
	}
	if command.ServerHost == "" || strings.ContainsAny(command.ServerHost, "\r\n\x00") {
		return errors.New("監聽 Host 格式錯誤")
	}
	if command.ServerPort < 1 || command.ServerPort > 65535 {
		return errors.New("監聽 Port 必須介於 1 到 65535")
	}
	if command.ContextSize < 128 || command.ContextSize > 1048576 {
		return errors.New("Context Size 必須介於 128 到 1048576")
	}
	if command.GPULayers < -1 {
		return errors.New("GPU Layers 不可小於 -1")
	}
	if command.Threads < 0 {
		return errors.New("Threads 不可小於 0")
	}
	for _, arg := range command.ExtraArgs {
		if strings.ContainsAny(arg, "\r\n\x00") {
			return errors.New("額外參數不可包含換行或 NUL 字元")
		}
	}
	return nil
}

func normalize(command domain.StartupCommand) domain.StartupCommand {
	command.ID = strings.TrimSpace(command.ID)
	command.Name = strings.TrimSpace(command.Name)
	command.Runtime = strings.TrimSpace(command.Runtime)
	if command.Runtime == "" {
		command.Runtime = domain.RuntimeLlamaServer
	}
	command.DraftModel = filepath.ToSlash(strings.TrimSpace(command.DraftModel))
	command.ServerHost = strings.TrimSpace(command.ServerHost)
	if command.ServerHost == "" {
		command.ServerHost = "0.0.0.0"
	}
	if command.ExtraArgs == nil {
		command.ExtraArgs = []string{}
	}
	args := make([]string, 0, len(command.ExtraArgs))
	for _, arg := range command.ExtraArgs {
		if trimmed := strings.TrimSpace(arg); trimmed != "" {
			args = append(args, trimmed)
		}
	}
	command.ExtraArgs = args
	return command
}

func ensureUniqueName(commands []domain.StartupCommand, name, exceptID string) error {
	nameKey := strings.ToLower(strings.TrimSpace(name))
	for _, command := range commands {
		if command.ID != exceptID && strings.ToLower(command.Name) == nameKey {
			return errors.New("啟動參數名稱不可重複")
		}
	}
	return nil
}

func (s *Store) persist(commands []domain.StartupCommand) error {
	content, err := json.MarshalIndent(fileData{Version: currentFileVersion, Commands: commands}, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".startup-commands-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
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
	return os.Rename(tempPath, s.path)
}

func cloneCommands(commands []domain.StartupCommand) []domain.StartupCommand {
	result := make([]domain.StartupCommand, len(commands))
	for index, command := range commands {
		result[index] = cloneCommand(command)
	}
	return result
}

func cloneCommand(command domain.StartupCommand) domain.StartupCommand {
	command.ExtraArgs = append([]string(nil), command.ExtraArgs...)
	return command
}

func randomID() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "profile-" + hex.EncodeToString(value), nil
}
