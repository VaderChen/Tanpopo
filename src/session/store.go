package session

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

const cookieName = "llama_loader_session"

// Store 只保存記憶體 Session；帳號密碼仍以本機 agent.properties 為唯一來源。
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
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		s.mu.Unlock()
		return false
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	expires := time.Now().Add(s.duration)
	s.pruneLocked(time.Now())
	s.sessions[token] = expires
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
	if !ok || !expires.After(now) {
		delete(s.sessions, cookie.Value)
		return false
	}
	return true
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

func secureEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}
