package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"LlamaLoader/src/session"
)

func TestAutomaticUpdateRequiresSameOriginAndLocalOrAuthenticatedAccess(t *testing.T) {
	for _, scenario := range []struct {
		name, remote, host, origin, header string
		want                               int
	}{
		{"local", "127.0.0.1:9000", "localhost:10082", "http://localhost:10082", "1", 409},
		{"local-ipv6", "[::1]:9000", "[::1]:10082", "http://[::1]:10082", "1", 409},
		{"remote-no-auth", "192.168.1.5:9000", "192.168.1.2:10082", "http://192.168.1.2:10082", "1", 403},
		{"foreign-origin", "127.0.0.1:9000", "localhost:10082", "https://attacker.invalid", "1", 403},
		{"no-header", "127.0.0.1:9000", "localhost:10082", "", "", 403},
		{"dns-rebinding", "127.0.0.1:9000", "attacker.invalid", "http://attacker.invalid", "1", 403},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := &Server{sessions: session.NewStore("admin", "test", time.Hour, false)}
			r := httptest.NewRequest(http.MethodPost, "http://"+scenario.host+"/api/app-update/start", nil)
			r.RemoteAddr = scenario.remote
			r.Header.Set("Origin", scenario.origin)
			r.Header.Set("X-Tanpopo-Update", scenario.header)
			w := httptest.NewRecorder()
			s.requireAPI(s.handleAppUpdateStart)(w, r)
			if w.Code != scenario.want {
				t.Fatalf("錯誤的存取控制：%d %s", w.Code, w.Body.String())
			}
		})
	}
	s := &Server{sessions: session.NewStore("admin", "test", time.Hour, true)}
	r := httptest.NewRequest(http.MethodPost, "http://localhost/api/app-update/start", nil)
	r.Header.Set("X-Tanpopo-Update", "1")
	w := httptest.NewRecorder()
	s.requireAPI(s.handleAppUpdateStart)(w, r)
	if w.Code != 401 {
		t.Fatal("未登入可以要求更新")
	}
}
