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
	mu       sync.Mutex
	account  string
	password string
	duration time.Duration
	sessions map[string]time.Time
}

func NewStore(account, password string, duration time.Duration) *Store {
	return &Store{
		account:  account,
		password: password,
		duration: duration,
		sessions: make(map[string]time.Time),
	}
}

func (s *Store) Login(w http.ResponseWriter, r *http.Request, account, password string) bool {
	if !secureEqual(account, s.account) || !secureEqual(password, s.password) {
		return false
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return false
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	expires := time.Now().Add(s.duration)
	s.mu.Lock()
	s.pruneLocked(time.Now())
	s.sessions[token] = expires
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(s.duration.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
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
	if err != nil || cookie.Value == "" {
		return false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.sessions[cookie.Value]
	if !ok || !expires.After(now) {
		delete(s.sessions, cookie.Value)
		return false
	}
	return true
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
