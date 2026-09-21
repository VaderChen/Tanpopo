package session

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRememberedSessionSurvivesStoreRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	cookie := loginCookie(t, persistentTestStore(t, path, "secret"), true)
	restarted := persistentTestStore(t, path, "secret")

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
	path := filepath.Join(t.TempDir(), "sessions.json")
	cookie := loginCookie(t, persistentTestStore(t, path, "secret"), true)
	changed := persistentTestStore(t, path, "new-secret")

	if changed.Authenticated(requestWithCookie(cookie)) {
		t.Fatal("密碼變更後，既有持久登入憑證應失效")
	}
}

func loginCookie(t *testing.T, store *Store, remember bool) *http.Cookie {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://tanpopo.local/api/login", nil)
	if ok, err := store.Login(recorder, request, "admin", "secret", remember); !ok || err != nil {
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

func persistentTestStore(t *testing.T, path, password string) *Store {
	t.Helper()
	s, err := NewPersistentStore(path, "admin", password, 24*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLogoutRevokesCookieAcrossRestart(t *testing.T) {
	for _, remember := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "sessions.json")
		s := persistentTestStore(t, path, "secret")
		cookie := loginCookie(t, s, remember)
		request := requestWithCookie(cookie)
		if err := s.Logout(httptest.NewRecorder(), request); err != nil {
			t.Fatal(err)
		}
		if s.Authenticated(request) || persistentTestStore(t, path, "secret").Authenticated(request) {
			t.Fatalf("登出後憑證仍可重用，remember=%v", remember)
		}
	}
}

func TestRevocationDoesNotReturnAfterSecurityChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	s := persistentTestStore(t, path, "secret")
	cookie := loginCookie(t, s, true)
	if err := s.RevokeAll(); err != nil {
		t.Fatal(err)
	}
	s.UpdateSecurity(false, "admin", "secret")
	s.UpdateSecurity(true, "admin", "secret")
	if persistentTestStore(t, path, "secret").Authenticated(requestWithCookie(cookie)) {
		t.Fatal("撤銷憑證重新生效")
	}
}

func TestPersistentStoreContainsHashesAndLogoutReportsWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	s := persistentTestStore(t, path, "secret")
	cookie := loginCookie(t, s, true)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), cookie.Value) || strings.Contains(string(data), "secret") {
		t.Fatal("登入紀錄包含原始憑證")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("登入紀錄權限不正確：%v", info.Mode())
	}
	// 讓原子替換失敗；不得回報已登出或清掉瀏覽器 Cookie。
	os.Remove(path)
	os.Mkdir(path, 0700)
	recorder := httptest.NewRecorder()
	if err := s.Logout(recorder, requestWithCookie(cookie)); err == nil {
		t.Fatal("未回報持久化失敗")
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatal("撤銷未保存卻已清除瀏覽器憑證")
	}
}
