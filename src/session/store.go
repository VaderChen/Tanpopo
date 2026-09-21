package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cookieName          = "llama_loader_session"
	rememberTokenPrefix = "r1"
)

// Store 的一般 Session 只保存在記憶體；持久憑證另以雜湊白名單保存，
// 簽章與伺服器紀錄皆有效才接受，登出後的撤銷可跨服務重啟。
type Store struct {
	mu                    sync.Mutex
	account               string
	password              string
	authenticationEnabled bool
	duration              time.Duration
	sessions              map[string]time.Time
	remembered            map[string]time.Time
	persistencePath       string
}

func NewStore(account, password string, duration time.Duration, authenticationEnabled bool) *Store {
	return &Store{
		account:               account,
		password:              password,
		authenticationEnabled: authenticationEnabled,
		duration:              duration,
		sessions:              make(map[string]time.Time),
		remembered:            make(map[string]time.Time),
	}
}

func (s *Store) Login(w http.ResponseWriter, r *http.Request, account, password string, remember bool) (bool, error) {
	s.mu.Lock()
	if !s.authenticationEnabled {
		s.mu.Unlock()
		return true, nil
	}
	if !secureEqual(account, s.account) || !secureEqual(password, s.password) {
		s.mu.Unlock()
		return false, nil
	}
	now := time.Now()
	expires := now.Add(s.duration)
	token, err := s.newTokenLocked(expires, remember)
	if err != nil {
		s.mu.Unlock()
		return false, fmt.Errorf("建立登入憑證失敗: %w", err)
	}
	s.pruneLocked(now)
	if remember {
		if len(s.remembered) >= 10000 {
			s.mu.Unlock()
			return false, errors.New("持久登入數量已達上限")
		}
		key := tokenDigest(token)
		s.remembered[key] = expires
		if err := s.persistLocked(); err != nil {
			delete(s.remembered, key)
			s.mu.Unlock()
			return false, fmt.Errorf("保存登入紀錄失敗: %w", err)
		}
	} else {
		s.sessions[token] = expires
	}
	s.mu.Unlock()
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	}
	if remember {
		cookie.Expires = expires
		cookie.MaxAge = int(s.duration.Seconds())
	}
	http.SetCookie(w, cookie)
	return true, nil
}

func (s *Store) Logout(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie(cookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
		key := tokenDigest(cookie.Value)
		if expires, exists := s.remembered[key]; exists {
			delete(s.remembered, key)
			if err := s.persistLocked(); err != nil {
				s.remembered[key] = expires
				s.mu.Unlock()
				return err
			}
		}
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func (s *Store) Authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(cookieName)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.authenticationEnabled {
		return true
	}
	if err != nil || cookie.Value == "" {
		return false
	}
	expires, ok := s.sessions[cookie.Value]
	if ok && expires.After(now) {
		return true
	}
	if ok {
		delete(s.sessions, cookie.Value)
	}
	expires, ok = s.remembered[tokenDigest(cookie.Value)]
	return ok && expires.After(now) && s.validRememberTokenLocked(cookie.Value, now)
}

func (s *Store) AuthenticationEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authenticationEnabled
}

func (s *Store) Account() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account
}

func (s *Store) VerifyPassword(password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return secureEqual(password, s.password)
}

// Security 是一次完整的登入策略，避免磁碟與記憶體套用不同版本。
type Security struct {
	Enabled  bool
	Account  string
	Password string
}

func (s *Store) UpdateSecurity(enabled bool, account, password string) error {
	return s.UpdateSecurityWith(func() (Security, error) {
		return Security{Enabled: enabled, Account: account, Password: password}, nil
	})
}

// UpdateSecurityWith 將撤銷紀錄、設定提交與記憶體狀態放在同一登入鎖內。
// commit 不得回頭呼叫 Store；提交前無法保存撤銷時不執行 commit。
// 設定提交失敗則復原舊憑證；若復原亦無法保存，維持撤銷並回報錯誤。
func (s *Store) UpdateSecurityWith(commit func() (Security, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.remembered
	s.remembered = make(map[string]time.Time)
	if err := s.persistLocked(); err != nil {
		s.remembered = previous
		return err
	}
	next, err := commit()
	if err != nil {
		s.remembered = previous
		if restoreErr := s.persistLocked(); restoreErr != nil {
			s.remembered = make(map[string]time.Time)
			s.sessions = make(map[string]time.Time)
			return errors.Join(err, fmt.Errorf("復原登入紀錄失敗，既有登入已撤銷: %w", restoreErr))
		}
		return err
	}
	s.authenticationEnabled = next.Enabled
	s.account = next.Account
	s.password = next.Password
	s.sessions = make(map[string]time.Time)
	return nil
}

func (s *Store) pruneLocked(now time.Time) {
	for token, expires := range s.remembered {
		if !expires.After(now) {
			delete(s.remembered, token)
		}
	}
	for token, expires := range s.sessions {
		if !expires.After(now) {
			delete(s.sessions, token)
		}
	}
}

func (s *Store) newTokenLocked(expires time.Time, remember bool) (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(tokenBytes)
	if !remember {
		return nonce, nil
	}
	payload := strconv.FormatInt(expires.Unix(), 10) + "." + nonce
	signature := s.signRememberPayloadLocked(payload)
	return rememberTokenPrefix + "." + payload + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *Store) validRememberTokenLocked(token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != rememberTokenPrefix {
		return false
	}
	expiresUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || !time.Unix(expiresUnix, 0).After(now) {
		return false
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(nonce) != 32 {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(signature) != sha256.Size {
		return false
	}
	expected := s.signRememberPayloadLocked(parts[1] + "." + parts[2])
	return hmac.Equal(signature, expected)
}

func (s *Store) signRememberPayloadLocked(payload string) []byte {
	key := sha256.Sum256([]byte("tanpopo-remember-v1\x00" + s.account + "\x00" + s.password))
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func secureEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

// NewPersistentStore 只載入本機已核發的憑證雜湊，不接受舊版無狀態憑證。
func NewPersistentStore(path, account, password string, duration time.Duration, enabled bool) (*Store, error) {
	s := NewStore(account, password, duration, enabled)
	s.persistencePath = path
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
		return nil, errors.New("登入紀錄檔案無效")
	}
	var state rememberedState
	if err := json.NewDecoder(file).Decode(&state); err != nil {
		return nil, err
	}
	if state.Version != 1 || len(state.Tokens) > 10000 {
		return nil, errors.New("登入紀錄格式無效")
	}
	for key := range state.Tokens {
		digest, err := hex.DecodeString(key)
		if err != nil || len(digest) != sha256.Size {
			return nil, errors.New("登入紀錄摘要無效")
		}
	}
	if state.Tokens != nil {
		s.remembered = state.Tokens
	}
	s.pruneLocked(time.Now())
	return s, nil
}

type rememberedState struct {
	Version int                  `json:"version"`
	Tokens  map[string]time.Time `json:"tokens"`
}

func tokenDigest(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// RevokeAll 必須在帳密設定寫入前成功，避免同帳密重新啟用時復活舊憑證。
func (s *Store) RevokeAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.remembered
	s.remembered = make(map[string]time.Time)
	if err := s.persistLocked(); err != nil {
		s.remembered = previous
		return err
	}
	s.sessions = make(map[string]time.Time)
	return nil
}

func (s *Store) persistLocked() error {
	if s.persistencePath == "" {
		return nil
	}
	content, err := json.Marshal(rememberedState{Version: 1, Tokens: s.remembered})
	if err != nil {
		return err
	}
	directory := filepath.Dir(s.persistencePath)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".sessions-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(content); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.persistencePath)
}
