package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"LlamaLoader/src/cluster"
	"LlamaLoader/src/domain"
	"LlamaLoader/src/llamacpp"
)

// 預設關閉管理登入不代表允許區網替使用者加入叢集；此時配對只接受本機操作。
func (s *Server) requireClusterAdmin(next http.HandlerFunc) http.HandlerFunc {
	guard := http.NewCrossOriginProtection()
	return s.requireAPI(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !s.sessions.AuthenticationEnabled() && !net.ParseIP(host).IsLoopback() {
			writeError(w, http.StatusForbidden, errors.New("請從本機設定 TCP Ring，或先啟用管理登入後再遠端操作"))
			return
		}
		if s.cluster == nil {
			writeError(w, http.StatusServiceUnavailable, errors.New("TCP Ring 管理器尚未就緒"))
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		guard.Handler(next).ServeHTTP(w, r)
	})
}

func (s *Server) handleClusterStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cluster.Status())
}

func (s *Server) handleClusterConfig(w http.ResponseWriter, r *http.Request) {
	var request cluster.ConfigUpdate
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.cluster.Configure(request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Status cluster.Status `json:"status"`
	}{s.cluster.Status()})
}

func (s *Server) handleClusterStart(w http.ResponseWriter, r *http.Request) {
	var request struct {
		llamacpp.RingOptions
		PeerIDs                    []string `json:"peer_ids"`
		Model                      string   `json:"model"`
		StartupCommandID           string   `json:"startup_command_id"`
		KVCacheQuantizationEnabled bool     `json:"kv_cache_quantization_enabled"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	profile, err := s.startupCommands.Get(request.StartupCommandID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !request.KVCacheQuantizationEnabled {
		profile.KVCacheQuantization = domain.KVCacheQuantizationNone
	}
	request.RingOptions.GGUFStrategy = s.settings.Get().DefaultFastGGUFStrategy
	status, err := s.cluster.Begin(r.Context(), request.PeerIDs, request.Model, profile, request.RingOptions)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) handleClusterStop(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := s.cluster.Stop(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.cluster.Status())
}
