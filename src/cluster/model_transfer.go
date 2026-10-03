package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"

	"LlamaLoader/src/modelbundle"
)

const modelTransferPath = "/api/cluster/model"

type modelRequest struct {
	SessionID string `json:"session_id"`
	Index     int    `json:"index"`
}

// 此端點只提供目前叢集已選模型的清單與檔案；不接受 URL 或檔案路徑。
func (s *Service) ModelTransfer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method != http.MethodPost || mediaType != "application/json" || r.Header.Get("Origin") != "" {
		http.Error(w, "無效的模型同步請求", http.StatusForbidden)
		return
	}
	var p packet
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&p); err != nil || p.To == "" || !s.accept(p) {
		http.Error(w, "模型同步驗證失敗", http.StatusUnauthorized)
		return
	}
	var request modelRequest
	if json.Unmarshal(p.Payload, &request) != nil || !validID(request.SessionID) || (p.Kind != "model-manifest" && p.Kind != "model-file") {
		http.Error(w, "模型同步參數無效", http.StatusBadRequest)
		return
	}
	address, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.mu.Lock()
	active := s.active
	allowed := false
	if active != nil && active.ID == request.SessionID && active.Role == "coordinator" && active.snapshot != nil {
		for _, peer := range active.peers {
			allowed = allowed || (peer.ID == p.From && peer.IP == address)
		}
	}
	var snapshot *modelbundle.Snapshot
	var jobCtx context.Context
	if allowed {
		snapshot, jobCtx = active.snapshot, active.ctx
	}
	s.mu.Unlock()
	if !allowed || jobCtx.Err() != nil {
		http.Error(w, "不屬於此模型同步工作", http.StatusForbidden)
		return
	}
	if p.Kind == "model-manifest" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot.Manifest)
		return
	}
	file, err := snapshot.Open(request.Index)
	if err != nil {
		http.Error(w, "發起端模型檔案已變更或索引無效", http.StatusConflict)
		return
	}
	defer file.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(jobCtx, cancel)
	defer stop()
	item := snapshot.Manifest.Files[request.Index]
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(item.Size, 10))
	buffer := make([]byte, 1<<20)
	var copied int64
	for copied < item.Size && ctx.Err() == nil {
		n, err := file.Read(buffer[:min(int64(len(buffer)), item.Size-copied)])
		if n > 0 {
			written, writeErr := w.Write(buffer[:n])
			copied += int64(written)
			if writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Service) fetchModel(ctx context.Context, peer Peer, sessionID, kind string, index int) (*http.Response, error) {
	s.mu.Lock()
	p := s.packetLocked(kind, peer.ID, "", modelRequest{SessionID: sessionID, Index: index})
	s.mu.Unlock()
	data, _ := json.Marshal(p)
	url := "http://" + net.JoinHostPort(peer.IP, strconv.Itoa(peer.Port)) + modelTransferPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.transferClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("向發起端下載模型失敗：%w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("發起端拒絕模型同步（HTTP %d）", response.StatusCode)
	}
	return response, nil
}

func (s *Service) fetchManifest(ctx context.Context, peer Peer, sessionID string) (modelbundle.Manifest, error) {
	response, err := s.fetchModel(ctx, peer, sessionID, "model-manifest", 0)
	if err != nil {
		return modelbundle.Manifest{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, modelbundle.MaxManifestBytes+1))
	if err != nil {
		return modelbundle.Manifest{}, err
	}
	if len(data) > modelbundle.MaxManifestBytes {
		return modelbundle.Manifest{}, errors.New("模型清單超限")
	}
	var manifest modelbundle.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, manifest.Validate()
}

func (s *Service) fetchModelFile(ctx context.Context, peer Peer, sessionID string, index int) (io.ReadCloser, error) {
	response, err := s.fetchModel(ctx, peer, sessionID, "model-file", index)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}
