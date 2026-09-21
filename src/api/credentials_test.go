package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"LlamaLoader/src/session"
)

func TestRememberLoginReturnsServerErrorWhenStorageFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	sessions, err := session.NewPersistentStore(path, "admin", "secret", time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	server := &Server{sessions: sessions}
	for _, test := range []struct {
		password string
		want     int
	}{{"wrong", http.StatusUnauthorized}, {"secret", http.StatusInternalServerError}} {
		request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"account":"admin","password":"`+test.password+`","remember_me":true}`))
		response := httptest.NewRecorder()
		server.handleLogin(response, request)
		if response.Code != test.want {
			t.Fatalf("登入錯誤分類錯誤：%d %s", response.Code, response.Body.String())
		}
		if len(response.Result().Cookies()) != 0 {
			t.Fatal("登入失敗仍核發 Cookie")
		}
	}
}

func TestCredentialsConfigFailurePreservesExistingLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	sessions, err := session.NewPersistentStore(path, "admin", "secret", time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	if ok, err := sessions.Login(login, httptest.NewRequest("POST", "/api/login", nil), "admin", "secret", true); !ok || err != nil {
		t.Fatal(err)
	}
	server := &Server{sessions: sessions, agentConfigPath: filepath.Join(t.TempDir(), "missing.json")}
	request := httptest.NewRequest(http.MethodPut, "/api/admin/credentials", strings.NewReader(`{"account":"admin","current_password":"secret","password":"changed","authentication_enabled":true}`))
	response := httptest.NewRecorder()
	server.handleAdminCredentialsUpdate(response, request)
	if response.Code == http.StatusOK {
		t.Fatal("設定保存失敗卻回報成功")
	}
	authenticated := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	authenticated.AddCookie(login.Result().Cookies()[0])
	if !sessions.Authenticated(authenticated) {
		t.Fatal("設定未變更卻已登出")
	}
}
