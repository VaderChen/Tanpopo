package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"LlamaLoader/src/cluster"
	"LlamaLoader/src/session"
)

func TestClusterAdministrationRequiresLocalConsentOrLogin(t *testing.T) {
	s := &Server{sessions: session.NewStore("test", "test", time.Hour, false), cluster: &cluster.Service{}}
	handler := s.requireClusterAdmin(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, test := range []struct {
		name, address, origin string
		want                  int
	}{
		{"本機可設定", "127.0.0.1:5000", "", 204},
		{"IPv6 本機可設定", "[::1]:5000", "", 204},
		{"未登入的區網不可設定", "192.168.1.20:5000", "", 403},
		{"本機瀏覽器跨站不能啟動", "127.0.0.1:5000", "https://untrusted.example", 403},
		{"同來源操作", "127.0.0.1:5000", "http://localhost:10082", 204},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://localhost:10082/api/cluster/start", nil)
			r.RemoteAddr = test.address
			if test.origin != "" {
				r.Header.Set("Origin", test.origin)
			}
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, test.want, w.Body.String())
			}
		})
	}
	s.sessions = session.NewStore("test", "test", time.Hour, true)
	r := httptest.NewRequest(http.MethodPost, "http://localhost:10082/api/cluster/start", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	s.requireClusterAdmin(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })(w, r)
	if w.Code != 401 {
		t.Fatalf("啟用登入後本機仍需登入：%d", w.Code)
	}
}
