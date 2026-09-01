package llamacpp

import (
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

const runtimeStateVersion = 6

// persistedRuntimeState 只保存重建模型服務所需的相對模型名稱與參數 ID，
// 不保存 Runtime 二進位檔或模型根目錄等本機絕對路徑。
type persistedRuntimeState struct {
	Version                 int       `json:"version"`
	DesiredRunning          bool      `json:"desired_running"`
	Runtime                 string    `json:"runtime"`
	Model                   string    `json:"model,omitempty"`
	MMProj                  string    `json:"mmproj,omitempty"`
	DraftModel              string    `json:"draft_model,omitempty"`
	DraftKind               string    `json:"draft_kind,omitempty"`
	DFlashEnabled           bool      `json:"dflash_enabled"`
	MMapEnabled             bool      `json:"mmap_enabled"`
	FastGGUF                bool      `json:"fast_gguf"`
	SkipGGUFConversionCache bool      `json:"skip_gguf_conversion_cache"`
	KVCacheQuantization     string    `json:"kv_cache_quantization,omitempty"`
	StartupCommandID        string    `json:"startup_command_id,omitempty"`
	StartupCommandName      string    `json:"startup_command_name,omitempty"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type runtimeStateStore struct {
	mu    sync.RWMutex
	path  string
	state persistedRuntimeState
}

func newRuntimeStateStore(path string) (*runtimeStateStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("模型服務狀態檔路徑不可為空")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	store := &runtimeStateStore{path: absolute}
	content, err := os.ReadFile(absolute)
	if os.IsNotExist(err) {
		state := persistedRuntimeState{
			Version:   runtimeStateVersion,
			Runtime:   domain.RuntimeLlamaServer,
			UpdatedAt: time.Now(),
		}
		if err := store.Save(state); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var state persistedRuntimeState
	if err := json.Unmarshal(content, &state); err != nil {
		return nil, fmt.Errorf("解析 %s 失敗: %w", absolute, err)
	}
	state = normalizeRuntimeState(state)
	if err := validateRuntimeState(state); err != nil {
		return nil, fmt.Errorf("模型服務狀態格式錯誤: %w", err)
	}
	store.state = state
	return store, nil
}

func (s *runtimeStateStore) Get() persistedRuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *runtimeStateStore) Save(state persistedRuntimeState) error {
	state = normalizeRuntimeState(state)
	state.Version = runtimeStateVersion
	state.UpdatedAt = time.Now()
	if err := validateRuntimeState(state); err != nil {
		return err
	}
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".runtime-state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return err
	}
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	return nil
}

func normalizeRuntimeState(state persistedRuntimeState) persistedRuntimeState {
	state.Runtime = strings.TrimSpace(state.Runtime)
	if state.Runtime == "" {
		state.Runtime = domain.RuntimeLlamaServer
	}
	state.Model = filepath.ToSlash(strings.TrimSpace(state.Model))
	state.MMProj = filepath.ToSlash(strings.TrimSpace(state.MMProj))
	state.DraftModel = filepath.ToSlash(strings.TrimSpace(state.DraftModel))
	state.DraftKind = strings.ToLower(strings.TrimSpace(state.DraftKind))
	state.StartupCommandID = strings.TrimSpace(state.StartupCommandID)
	state.StartupCommandName = strings.TrimSpace(state.StartupCommandName)
	state.KVCacheQuantization = strings.ToLower(strings.TrimSpace(state.KVCacheQuantization))
	if state.Runtime == domain.RuntimeMLXServer {
		state.MMProj = ""
	} else {
		state.FastGGUF = false
		state.SkipGGUFConversionCache = false
	}
	if state.DFlashEnabled {
		state.DraftKind = "dflash"
	}
	if state.DraftKind != "dflash" && state.DraftKind != "mtp" {
		state.DraftKind = ""
	}
	if state.DraftKind == "dflash" && !state.DFlashEnabled {
		state.DraftKind = ""
	}
	if state.DraftKind == "" {
		state.DraftModel = ""
	}
	if state.DraftKind != "" {
		state.KVCacheQuantization = domain.KVCacheQuantizationNone
	}
	return state
}

func validateRuntimeState(state persistedRuntimeState) error {
	if state.Version > runtimeStateVersion {
		return fmt.Errorf("狀態檔版本 %d 高於目前支援版本 %d", state.Version, runtimeStateVersion)
	}
	if state.Runtime != domain.RuntimeLlamaServer && state.Runtime != domain.RuntimeMLXServer {
		return errors.New("runtime 不支援")
	}
	for label, value := range map[string]string{
		"model": state.Model, "mmproj": state.MMProj, "draft_model": state.DraftModel,
		"startup_command_id": state.StartupCommandID, "startup_command_name": state.StartupCommandName,
	} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("%s 格式錯誤", label)
		}
	}
	if state.DesiredRunning && (state.Model == "" || state.StartupCommandID == "") {
		return errors.New("執行狀態缺少模型或啟動參數 ID")
	}
	if state.DraftKind != "" && state.DraftModel == "" {
		return errors.New("推測解碼狀態缺少 Draft 模型")
	}
	if state.DraftKind == "mtp" && state.Runtime != domain.RuntimeMLXServer {
		return errors.New("MTP 狀態僅支援 mlx-server")
	}
	if state.KVCacheQuantization != domain.KVCacheQuantizationNone &&
		state.KVCacheQuantization != domain.KVCacheQuantizationQ8 &&
		state.KVCacheQuantization != domain.KVCacheQuantizationQ4 {
		return errors.New("KV Cache 量化狀態只支援 Q8 或 Q4")
	}
	if state.DraftKind != "" && state.KVCacheQuantization != domain.KVCacheQuantizationNone {
		return errors.New("推測解碼與 KV Cache 量化狀態不可同時啟用")
	}
	return nil
}
