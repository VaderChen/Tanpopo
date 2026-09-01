package session

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRememberedSessionSurvivesStoreRestart(t *testing.T) {
	cookie := loginCookie(t, NewStore("admin", "secret", 24*time.Hour, true), true)
	restarted := NewStore("admin", "secret", 24*time.Hour, true)

	if !restarted.Authenticated(requestWithCookie(cookie)) {
		t.Fatal("勾選記住我後，服務重啟應仍可驗證登入憑證")
	}
}

func TestSessionCookieDoesNotSurviveStoreRestart(t *testing.T) {
	cookie := loginCookie(t, NewStore("admin", "secret", 24*time.Hour, true), false)
	restarted := NewStore("admin", "secret", 24*time.Hour, true)

	if restarted.Authenticated(requestWithCookie(cookie)) {
		t.Fatal("未勾選記住我的記憶體 Session 不應跨服務重啟")
	}
}

func TestRememberedSessionInvalidAfterCredentialChange(t *testing.T) {
	cookie := loginCookie(t, NewStore("admin", "secret", 24*time.Hour, true), true)
	changed := NewStore("admin", "new-secret", 24*time.Hour, true)

	if changed.Authenticated(requestWithCookie(cookie)) {
		t.Fatal("密碼變更後，既有持久登入憑證應失效")
	}
}

func loginCookie(t *testing.T, store *Store, remember bool) *http.Cookie {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://tanpopo.local/api/login", nil)
	if !store.Login(recorder, request, "admin", "secret", remember) {
		t.Fatal("登入失敗")
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("預期一個 Session Cookie，實際為 %d", len(cookies))
	}
	return cookies[0]
}

func requestWithCookie(cookie *http.Cookie) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://tanpopo.local/main.html", nil)
	request.AddCookie(cookie)
	return request
}
