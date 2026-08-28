// Package accesscontrol 管理兩種模型 Runtime 共用的存取金鑰與 IP 白名單快照。
package accesscontrol

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	currentFileVersion = 1
	keyPrefix          = "olk_"
	defaultLoopbackIP  = "127.0.0.1"
	maxKeys            = 100
	maxIPPatterns      = 256
)

var wildcardPattern = regexp.MustCompile(`^[0-9A-Fa-f:.*]+$`)

// Policy 的兩個開關彼此獨立；同時啟用時，請求必須同時通過兩項檢查。
type Policy struct {
	APIKeyEnabled      bool     `json:"api_key_enabled"`
	IPAllowlistEnabled bool     `json:"ip_allowlist_enabled"`
	IPAllowlist        []string `json:"ip_allowlist"`
}

type PublicKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	CreatedAt time.Time `json:"created_at"`
}

type View struct {
	Policy Policy      `json:"policy"`
	Keys   []PublicKey `json:"keys"`
}

// IssuedKey 的 Key 只會在核發當下回傳一次，設定檔不保存明文。
type IssuedKey struct {
	PublicKey
	Key string `json:"key"`
}

type storedKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
}

type fileData struct {
	Version int         `json:"version"`
	Policy  Policy      `json:"policy"`
	Keys    []storedKey `json:"keys"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data fileData
}

func NewStore(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("模型 API 安全設定檔路徑不可為空")
	}
	store := &Store{
		path: path,
		data: fileData{
			Version: currentFileVersion,
			Policy:  Policy{IPAllowlist: []string{defaultLoopbackIP}},
			Keys:    []storedKey{},
		},
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := store.persist(store.data); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var data fileData
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("解析模型 API 安全設定失敗: %w", err)
	}
	if data.Version != currentFileVersion {
		return nil, fmt.Errorf("不支援的模型 API 安全設定版本: %d", data.Version)
	}
	originalPolicy := clonePolicy(data.Policy)
	policyInput := clonePolicy(data.Policy)
	if policyInput.IPAllowlist == nil {
		policyInput.IPAllowlist = []string{defaultLoopbackIP}
	}
	policy, err := normalizePolicy(policyInput)
	if err != nil {
		return nil, err
	}
	data.Policy = policy
	keysWereNil := data.Keys == nil
	if data.Keys == nil {
		data.Keys = []storedKey{}
	}
	if err := validateStoredKeys(data.Keys); err != nil {
		return nil, err
	}
	if data.Policy.APIKeyEnabled && len(data.Keys) == 0 {
		return nil, errors.New("模型 API 已啟用金鑰驗證，但設定檔內沒有可用金鑰")
	}
	if !samePolicy(originalPolicy, data.Policy) || keysWereNil {
		if err := store.persist(data); err != nil {
			return nil, fmt.Errorf("更新模型 API 安全設定格式失敗: %w", err)
		}
	} else if err := os.Chmod(path, 0600); err != nil {
		return nil, fmt.Errorf("設定模型 API 安全檔案權限失敗: %w", err)
	}
	store.data = data
	return store, nil
}

func (s *Store) Public() View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]PublicKey, 0, len(s.data.Keys))
	for _, key := range s.data.Keys {
		keys = append(keys, publicKey(key))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].CreatedAt.After(keys[j].CreatedAt) })
	return View{Policy: publicPolicy(s.data.Policy), Keys: keys}
}

func (s *Store) Policy() Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clonePolicy(s.data.Policy)
}

func (s *Store) UpdatePolicy(value Policy) (View, error) {
	policy, err := normalizePolicy(value)
	if err != nil {
		return View{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if policy.APIKeyEnabled && len(s.data.Keys) == 0 {
		return View{}, errors.New("請先核發至少一把金鑰，再啟用金鑰驗證")
	}
	updated := cloneData(s.data)
	updated.Policy = policy
	if err := s.persist(updated); err != nil {
		return View{}, err
	}
	s.data = updated
	return publicView(updated), nil
}

func (s *Store) IssueKey(name string) (IssuedKey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 || strings.ContainsAny(name, "\r\n\x00") {
		return IssuedKey{}, errors.New("金鑰名稱不可為空、不可換行且最多 80 個字元")
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return IssuedKey{}, fmt.Errorf("產生金鑰失敗: %w", err)
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return IssuedKey{}, fmt.Errorf("產生金鑰識別碼失敗: %w", err)
	}
	secret := keyPrefix + base64.RawURLEncoding.EncodeToString(secretBytes)
	digest := sha256.Sum256([]byte(secret))
	record := storedKey{
		ID:        hex.EncodeToString(idBytes),
		Name:      name,
		Prefix:    secret[:12],
		Hash:      hex.EncodeToString(digest[:]),
		CreatedAt: time.Now().UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Keys) >= maxKeys {
		return IssuedKey{}, fmt.Errorf("金鑰數量不可超過 %d 把", maxKeys)
	}
	for _, current := range s.data.Keys {
		if strings.EqualFold(current.Name, name) {
			return IssuedKey{}, errors.New("金鑰名稱不可重複")
		}
	}
	updated := cloneData(s.data)
	updated.Keys = append(updated.Keys, record)
	if err := s.persist(updated); err != nil {
		return IssuedKey{}, err
	}
	s.data = updated
	return IssuedKey{PublicKey: publicKey(record), Key: secret}, nil
}

func (s *Store) RevokeKey(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("金鑰 ID 不可為空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := cloneData(s.data)
	keys := make([]storedKey, 0, len(updated.Keys))
	found := false
	for _, key := range updated.Keys {
		if key.ID == id {
			found = true
			continue
		}
		keys = append(keys, key)
	}
	if !found {
		return errors.New("找不到指定的金鑰")
	}
	if updated.Policy.APIKeyEnabled && len(keys) == 0 {
		return errors.New("金鑰驗證啟用中，不可撤銷最後一把金鑰")
	}
	updated.Keys = keys
	if err := s.persist(updated); err != nil {
		return err
	}
	s.data = updated
	return nil
}

func (s *Store) AuthorizeKey(secret string) bool {
	secret = strings.TrimSpace(secret)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.data.Policy.APIKeyEnabled {
		return true
	}
	if !strings.HasPrefix(secret, keyPrefix) {
		return false
	}
	digest := sha256.Sum256([]byte(secret))
	matched := 0
	for _, key := range s.data.Keys {
		expected, err := hex.DecodeString(key.Hash)
		if err != nil || len(expected) != sha256.Size {
			continue
		}
		matched |= subtle.ConstantTimeCompare(digest[:], expected)
	}
	return matched == 1
}

func (s *Store) AllowsRemote(remoteAddress string) bool {
	s.mu.RLock()
	policy := clonePolicy(s.data.Policy)
	s.mu.RUnlock()
	if !policy.IPAllowlistEnabled {
		return true
	}
	ip := remoteIP(remoteAddress)
	if ip == nil {
		return false
	}
	value := canonicalIP(ip)
	for _, pattern := range policy.IPAllowlist {
		if matchIPPattern(value, ip, pattern) {
			return true
		}
	}
	return false
}

func normalizePolicy(value Policy) (Policy, error) {
	patterns := make([]string, 0, len(value.IPAllowlist))
	seen := make(map[string]bool, len(value.IPAllowlist))
	for _, raw := range value.IPAllowlist {
		pattern, err := normalizeIPPattern(raw)
		if err != nil {
			return Policy{}, err
		}
		if pattern == "" || seen[pattern] {
			continue
		}
		seen[pattern] = true
		patterns = append(patterns, pattern)
		if len(patterns) > maxIPPatterns {
			return Policy{}, fmt.Errorf("IP 白名單最多可設定 %d 筆", maxIPPatterns)
		}
	}
	if value.IPAllowlistEnabled && len(patterns) == 0 {
		return Policy{}, errors.New("請先設定至少一筆 IP 白名單，再啟用 IP 限制")
	}
	return Policy{
		APIKeyEnabled:      value.APIKeyEnabled,
		IPAllowlistEnabled: value.IPAllowlistEnabled,
		IPAllowlist:        patterns,
	}, nil
}

func normalizeIPPattern(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	if value == "*" {
		return value, nil
	}
	if strings.Contains(value, "/") {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return "", fmt.Errorf("IP 白名單格式錯誤: %s", value)
		}
		return network.String(), nil
	}
	if strings.Contains(value, "*") {
		if len(value) > 128 || !wildcardPattern.MatchString(value) {
			return "", fmt.Errorf("IP 萬用字元格式錯誤: %s", value)
		}
		return value, nil
	}
	ip := net.ParseIP(strings.Trim(value, "[]"))
	if ip == nil {
		return "", fmt.Errorf("IP 白名單格式錯誤: %s", value)
	}
	return canonicalIP(ip), nil
}

func matchIPPattern(value string, ip net.IP, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if strings.Contains(pattern, "/") {
		_, network, err := net.ParseCIDR(pattern)
		return err == nil && network.Contains(ip)
	}
	if !strings.Contains(pattern, "*") {
		return value == pattern
	}
	expression := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$"
	matched, err := regexp.MatchString(expression, strings.ToLower(value))
	return err == nil && matched
}

func remoteIP(remoteAddress string) net.IP {
	host := strings.TrimSpace(remoteAddress)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	if zoneIndex := strings.LastIndex(host, "%"); zoneIndex >= 0 {
		host = host[:zoneIndex]
	}
	return net.ParseIP(host)
}

func canonicalIP(ip net.IP) string {
	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4.String()
	}
	return strings.ToLower(ip.String())
}

func validateStoredKeys(keys []storedKey) error {
	seenIDs := make(map[string]bool, len(keys))
	seenNames := make(map[string]bool, len(keys))
	if len(keys) > maxKeys {
		return fmt.Errorf("金鑰數量不可超過 %d 把", maxKeys)
	}
	for _, key := range keys {
		if len(key.ID) != 24 {
			return errors.New("模型 API 金鑰 ID 格式錯誤")
		}
		if _, err := hex.DecodeString(key.ID); err != nil {
			return errors.New("模型 API 金鑰 ID 格式錯誤")
		}
		if key.Name == "" || key.Prefix == "" || len(key.Hash) != sha256.Size*2 || key.CreatedAt.IsZero() {
			return errors.New("模型 API 金鑰資料不完整")
		}
		if _, err := hex.DecodeString(key.Hash); err != nil {
			return errors.New("模型 API 金鑰雜湊格式錯誤")
		}
		nameKey := strings.ToLower(key.Name)
		if seenIDs[key.ID] || seenNames[nameKey] {
			return errors.New("模型 API 金鑰資料重複")
		}
		seenIDs[key.ID] = true
		seenNames[nameKey] = true
	}
	return nil
}

func publicKey(key storedKey) PublicKey {
	return PublicKey{ID: key.ID, Name: key.Name, Prefix: key.Prefix, CreatedAt: key.CreatedAt}
}

func publicView(data fileData) View {
	keys := make([]PublicKey, 0, len(data.Keys))
	for _, key := range data.Keys {
		keys = append(keys, publicKey(key))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].CreatedAt.After(keys[j].CreatedAt) })
	return View{Policy: publicPolicy(data.Policy), Keys: keys}
}

func publicPolicy(value Policy) Policy {
	value = clonePolicy(value)
	if len(value.IPAllowlist) == 0 {
		value.IPAllowlist = []string{defaultLoopbackIP}
	}
	return value
}

func clonePolicy(value Policy) Policy {
	value.IPAllowlist = append([]string(nil), value.IPAllowlist...)
	return value
}

func samePolicy(left, right Policy) bool {
	return left.APIKeyEnabled == right.APIKeyEnabled &&
		left.IPAllowlistEnabled == right.IPAllowlistEnabled &&
		slices.Equal(left.IPAllowlist, right.IPAllowlist)
}

func cloneData(value fileData) fileData {
	value.Policy = clonePolicy(value.Policy)
	value.Keys = append([]storedKey(nil), value.Keys...)
	return value
}

func (s *Store) persist(value fileData) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".access-control-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, s.path)
}
