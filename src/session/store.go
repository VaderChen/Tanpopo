package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cookieName          = "llama_loader_session"
	rememberTokenPrefix = "r1"
)

// Store 的一般 Session 只保存在記憶體；「記住我」使用帳號密碼衍生金鑰簽章，
// 讓服務重啟後仍可驗證，且帳號或密碼變更後會立即失效。
type Store struct {
	mu                    sync.Mutex
	account               string
	password              string
	authenticationEnabled bool
	duration              time.Duration
	sessions              map[string]time.Time
}

func NewStore(account, password string, duration time.Duration, authenticationEnabled bool) *Store {
	return &Store{
		account:               account,
		password:              password,
		authenticationEnabled: authenticationEnabled,
		duration:              duration,
		sessions:              make(map[string]time.Time),
	}
}

func (s *Store) Login(w http.ResponseWriter, r *http.Request, account, password string, remember bool) bool {
	s.mu.Lock()
	if !s.authenticationEnabled {
		s.mu.Unlock()
		return true
	}
	if !secureEqual(account, s.account) || !secureEqual(password, s.password) {
		s.mu.Unlock()
		return false
	}
	now := time.Now()
	expires := now.Add(s.duration)
	token, err := s.newTokenLocked(expires, remember)
	if err != nil {
		s.mu.Unlock()
		return false
	}
	s.pruneLocked(now)
	if !remember {
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
	return true
}

func (s *Store) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
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
	return s.validRememberTokenLocked(cookie.Value, now)
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

// UpdateSecurity 即時套用登入策略並撤銷所有既有 Session。
func (s *Store) UpdateSecurity(authenticationEnabled bool, account, password string) {
	s.mu.Lock()
	s.authenticationEnabled = authenticationEnabled
	s.account = account
	s.password = password
	s.sessions = make(map[string]time.Time)
	s.mu.Unlock()
}

func (s *Store) pruneLocked(now time.Time) {
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
