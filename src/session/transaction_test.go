package session

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSecurityPersistenceFailurePreservesPreviousState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := persistentTestStore(t, path, "secret")
	cookie := loginCookie(t, store, true)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSecurity(true, "changed", "new-secret"); err == nil {
		t.Fatal("預期保存失敗")
	}
	if store.Account() != "admin" || !store.Authenticated(requestWithCookie(cookie)) {
		t.Fatal("保存失敗後帳號或登入狀態已變更")
	}
}

func TestSecurityCommitFailureRestoresPersistentSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := persistentTestStore(t, path, "secret")
	cookie := loginCookie(t, store, true)
	expected := errors.New("設定保存失敗")
	if err := store.UpdateSecurityWith(func() (Security, error) { return Security{}, expected }); !errors.Is(err, expected) {
		t.Fatal(err)
	}
	if !store.Authenticated(requestWithCookie(cookie)) || !persistentTestStore(t, path, "secret").Authenticated(requestWithCookie(cookie)) {
		t.Fatal("設定提交失敗卻撤銷了原登入")
	}
}

func TestLoginCannotCrossSecurityCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store := persistentTestStore(t, path, "secret")
	entered, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- store.UpdateSecurityWith(func() (Security, error) {
			close(entered)
			<-release
			return Security{Enabled: true, Account: "admin", Password: "changed"}, nil
		})
	}()
	<-entered
	loggedIn := make(chan bool, 1)
	go func() {
		ok, _ := store.Login(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/login", nil), "admin", "secret", true)
		loggedIn <- ok
	}()
	select {
	case <-loggedIn:
		close(release)
		t.Fatal("登入越過尚未提交的設定")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if <-loggedIn {
		t.Fatal("舊密碼在新設定提交後仍可核發憑證")
	}
}
