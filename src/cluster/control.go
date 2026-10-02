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
	"sync"
	"time"

	"LlamaLoader/src/domain"
	"LlamaLoader/src/llamacpp"
)

type controlRequest struct {
	SessionID   string             `json:"session_id"`
	Model       llamacpp.RingModel `json:"model"`
	Version     string             `json:"runtime_version"`
	Members     []Member           `json:"members,omitempty"`
	ContextSize int                `json:"context_size"`
	Addresses   []string           `json:"addresses,omitempty"`
	Ready       bool               `json:"ready"`
}

type controlResponse struct {
	Address string `json:"address,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Service) Start(ctx context.Context, peerIDs []string, model string, profile domain.StartupCommand) (Status, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	enabled, busy, closed, capability := s.config.Enabled, s.active != nil, s.closed, s.capability
	local := s.nodeLocked()
	peers := make([]Peer, 0, len(peerIDs))
	seen := make(map[string]bool)
	for _, id := range peerIDs {
		peer, exists := s.peers[id]
		if !exists || seen[id] || time.Since(peer.LastSeen) >= peerLifetime {
			continue
		}
		seen[id] = true
		peers = append(peers, peer)
	}
	s.mu.Unlock()
	if !enabled || closed || busy || len(peers) != len(peerIDs) || len(peers) < 1 ||
		len(peers)+1 > llamacpp.MaxRingNodes || len(peers)+1 > capability.MaxNodes {
		return s.Status(), errors.New("請啟用探索、選擇 1–7 個不同的在線節點，並先停止目前叢集服務")
	}
	for _, peer := range peers {
		if peer.Busy || !peer.Capabilities.Available || !peer.Capabilities.ManagedParentStdin ||
			peer.Capabilities.Version != capability.Version || peer.Capabilities.MaxNodes < len(peers)+1 {
			return s.Status(), fmt.Errorf("%s 忙碌、未支援此節點數，或 mlx-server 版本不同", peer.Name)
		}
	}
	if profile.Runtime != domain.RuntimeMLXServer || profile.ContextSize < 128 || profile.ContextSize > 1048576 {
		return s.Status(), errors.New("請選擇有效的 mlx-server 啟動參數")
	}
	descriptor, err := s.backend.InspectRingModel(model)
	if err != nil {
		return s.Status(), err
	}
	owner := randomID()
	if err = s.backend.ReserveRing(owner); err != nil {
		return s.Status(), err
	}
	listener, err := reserveAddress(peers[0].IP)
	if err != nil {
		s.backend.ReleaseRing(owner)
		return s.Status(), err
	}
	localIP, _, _ := net.SplitHostPort(listener.Addr().String())
	members := []Member{{ID: local.ID, Name: local.Name, IP: localIP, Port: local.Port}}
	for _, peer := range peers {
		members = append(members, Member{ID: peer.ID, Name: peer.Name, IP: peer.IP, Port: peer.Port})
	}
	addresses := make([]string, len(members))
	addresses[0] = listener.Addr().String()
	s.mu.Lock()
	s.active = &activeSession{Session: Session{ID: owner, Members: members, Rank: 0, Role: "coordinator", Phase: "preparing",
		Model: model, StartedAt: time.Now(), Addresses: append([]string(nil), addresses...)},
		peers: peers, listener: listener, lastLease: time.Now()}
	s.lastError = ""
	s.mu.Unlock()
	// 任一節點失敗就回收整組；即使回覆遺失或瀏覽器中斷，保留也會由租約清理。
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			_ = s.stopLocked(cleanup, err.Error(), true)
			cancel()
		}
	}()
	request := controlRequest{SessionID: owner, Model: descriptor, Version: capability.Version, Members: members, ContextSize: profile.ContextSize}
	prepared, prepareErr := s.callPeers(ctx, peers, "prepare", request)
	err = prepareErr
	if err != nil {
		return s.Status(), err
	}
	for i, reply := range prepared {
		if !endpointAt(reply.Address, peers[i].IP) {
			err = fmt.Errorf("%s 回傳不符來源 IP 的 Ring 位址", peers[i].Name)
			return s.Status(), err
		}
		addresses[i+1] = reply.Address
	}
	request.Addresses = addresses
	s.mu.Lock()
	s.active.Addresses, s.active.Phase = append([]string(nil), addresses...), "starting"
	s.mu.Unlock()
	_, err = s.callPeers(ctx, peers, "commit", request)
	if err != nil {
		return s.Status(), err
	}
	if err = ctx.Err(); err != nil {
		return s.Status(), err
	}
	_ = listener.Close()
	_, err = s.backend.StartRing(owner, model, 0, addresses, profile)
	if err != nil {
		return s.Status(), err
	}
	s.mu.Lock()
	s.active.listener, s.active.Phase, s.active.lastLease = nil, "loading", time.Now()
	s.mu.Unlock()
	return s.Status(), nil
}

func reserveAddress(peerIP string) (net.Listener, error) {
	ip := net.ParseIP(peerIP)
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return nil, errors.New("探索未取得有效 IPv4 位址")
	}
	// UDP connect 不傳送封包；由路由表選出真正可回連的本機 IPv4。
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: ip, Port: DefaultDiscoveryPort})
	if err != nil {
		return nil, err
	}
	localIP := conn.LocalAddr().(*net.UDPAddr).IP
	_ = conn.Close()
	return net.ListenTCP("tcp4", &net.TCPAddr{IP: localIP})
}

func endpointAt(endpoint, expectedIP string) bool {
	host, port, err := net.SplitHostPort(endpoint)
	n, _ := strconv.Atoi(port)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.To4() != nil && !ip.IsUnspecified() && !ip.IsMulticast() &&
		ip.Equal(net.ParseIP(expectedIP)) && n >= 1024 && n <= 65535
}

// 同一階段同步等待所有節點，避免節點數增加讓先保留的節點租約過期。
func (s *Service) callPeers(ctx context.Context, peers []Peer, kind string, request controlRequest) ([]controlResponse, error) {
	results, failures := make([]controlResponse, len(peers)), make([]error, len(peers))
	var group sync.WaitGroup
	for i, peer := range peers {
		group.Add(1)
		go func(i int, peer Peer) {
			defer group.Done()
			results[i], failures[i] = s.call(ctx, peer, kind, request)
			if failures[i] != nil {
				failures[i] = fmt.Errorf("%s：%w", peer.Name, failures[i])
			}
		}(i, peer)
	}
	group.Wait()
	return results, errors.Join(failures...)
}

func (s *Service) call(ctx context.Context, peer Peer, kind string, payload controlRequest) (controlResponse, error) {
	s.mu.Lock()
	p := s.packetLocked(kind, peer.ID, "", payload)
	s.mu.Unlock()
	data, _ := json.Marshal(p)
	url := "http://" + net.JoinHostPort(peer.IP, strconv.Itoa(peer.Port)) + controlPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return controlResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return controlResponse{}, fmt.Errorf("TCP Ring 握手失敗：%w", err)
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, 16385))
	var reply packet
	if err != nil || len(data) > 16384 || json.Unmarshal(data, &reply) != nil || reply.Kind != "response" ||
		reply.From != peer.ID || reply.To != p.From || reply.ReplyTo != p.Nonce || !s.accept(reply) {
		return controlResponse{}, errors.New("對端回覆無法核對，請重新搜尋並確認系統時間")
	}
	var result controlResponse
	if err = json.Unmarshal(reply.Payload, &result); err != nil {
		return result, err
	}
	if result.Error != "" {
		return result, errors.New(result.Error)
	}
	if response.StatusCode != http.StatusOK {
		return result, errors.New("對端握手未完成")
	}
	return result, nil
}

// Control 不使用瀏覽器登入 Cookie，只接受已開放探索且完成 UDP 往返的來源。
// 此為信任區網模式，nonce／IP 核對不等同密碼學身分驗證。
func (s *Service) Control(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	data, err := io.ReadAll(io.LimitReader(r.Body, 16385))
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var p packet
	if r.Method != http.MethodPost || mediaType != "application/json" || r.Header.Get("Origin") != "" ||
		err != nil || len(data) > 16384 || json.Unmarshal(data, &p) != nil || p.To == "" || !s.accept(p) {
		http.Error(w, "無法核對 TCP Ring 控制請求", http.StatusUnauthorized)
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	var request controlRequest
	var result controlResponse
	if json.Unmarshal(p.Payload, &request) != nil || !validID(request.SessionID) || net.ParseIP(host).To4() == nil {
		result.Error = "握手格式錯誤"
	} else {
		s.opMu.Lock()
		result, err = s.control(r.Context(), p, host, request)
		s.opMu.Unlock()
		if err != nil {
			result.Error = err.Error()
		}
	}
	s.mu.Lock()
	reply := s.packetLocked("response", p.From, p.Nonce, result)
	s.mu.Unlock()
	if result.Error != "" {
		w.WriteHeader(http.StatusConflict)
	}
	_ = json.NewEncoder(w).Encode(reply)
}

func (s *Service) knownPeerLocked(id, sourceIP string) (Peer, bool) {
	if s.active != nil {
		for _, peer := range s.active.peers {
			if peer.ID == id && peer.IP == sourceIP {
				return peer, true
			}
		}
	}
	peer, exists := s.peers[id]
	return peer, exists && peer.IP == sourceIP && time.Since(peer.LastSeen) < peerLifetime
}

// 成員順序就是 Rank 順序；只允許已探索的來源，不能藉握手指定其他網路端點。
func (s *Service) memberRank(members []Member, from, sourceIP string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(members) < 2 || len(members) > llamacpp.MaxRingNodes || len(members) > s.capability.MaxNodes ||
		members[0].ID != from || members[0].IP != sourceIP {
		return -1, errors.New("叢集成員或主節點位址無效")
	}
	rank := -1
	seen := make(map[string]bool)
	for i, member := range members {
		if !validID(member.ID) || seen[member.ID] || len(member.Name) > 255 ||
			!endpointAt(net.JoinHostPort(member.IP, strconv.Itoa(member.Port)), member.IP) {
			return -1, errors.New("叢集成員重複或端點格式無效")
		}
		seen[member.ID] = true
		if member.ID == s.config.NodeID {
			if i == 0 || member.Port != s.port {
				return -1, errors.New("工作節點身分不符")
			}
			rank = i
		} else {
			peer, ok := s.knownPeerLocked(member.ID, member.IP)
			if !ok || peer.Port != member.Port {
				return -1, errors.New("成員尚未完成探索，請稍後重新搜尋")
			}
		}
	}
	if rank < 1 {
		return -1, errors.New("成員名單缺少本機")
	}
	return rank, nil
}

func (s *Service) control(ctx context.Context, p packet, sourceIP string, request controlRequest) (controlResponse, error) {
	if err := ctx.Err(); err != nil {
		return controlResponse{}, err
	}
	s.mu.Lock()
	active, enabled, closed, version := s.active, s.config.Enabled, s.closed, s.capability.Version
	peer, known := s.knownPeerLocked(p.From, sourceIP)
	s.mu.Unlock()
	if !enabled || closed || !known {
		return controlResponse{}, errors.New("請先在兩端開啟探索並完成區網搜尋")
	}
	if p.Kind == "prepare" {
		if active != nil || request.Version != version || request.ContextSize < 128 || request.ContextSize > 1048576 {
			return controlResponse{}, errors.New("節點忙碌、Runtime 版本不符或握手參數無效")
		}
		rank, err := s.memberRank(request.Members, p.From, sourceIP)
		if err != nil {
			return controlResponse{}, err
		}
		model, err := s.backend.InspectRingModel(request.Model.Path)
		if err != nil {
			return controlResponse{}, err
		}
		if model.Fingerprint != request.Model.Fingerprint || model.Architecture != request.Model.Architecture {
			return controlResponse{}, errors.New("節點模型設定不一致；請安裝相同模型版本與量化格式")
		}
		if err = s.backend.ReserveRing(request.SessionID); err != nil {
			return controlResponse{}, err
		}
		listener, err := reserveAddress(sourceIP)
		if err != nil {
			s.backend.ReleaseRing(request.SessionID)
			return controlResponse{}, err
		}
		if !endpointAt(listener.Addr().String(), request.Members[rank].IP) {
			_ = listener.Close()
			s.backend.ReleaseRing(request.SessionID)
			return controlResponse{}, errors.New("節點使用不同網路路徑，請選擇各端共同可達的網路介面")
		}
		addresses := make([]string, len(request.Members))
		addresses[rank] = listener.Addr().String()
		s.mu.Lock()
		s.active = &activeSession{Session: Session{ID: request.SessionID, Members: append([]Member(nil), request.Members...), Rank: rank,
			Role: "worker", Phase: "prepared", Model: model.Path, StartedAt: time.Now(), Addresses: addresses},
			peers: []Peer{peer}, listener: listener, lastLease: time.Now(),
			profile: domain.StartupCommand{Runtime: domain.RuntimeMLXServer, Name: "TCP Ring 工作節點", ContextSize: request.ContextSize,
				ServerHost: "127.0.0.1", ServerPort: 10084, KVCacheQuantization: domain.KVCacheQuantizationNone}}
		s.lastError = ""
		s.mu.Unlock()
		return controlResponse{Address: listener.Addr().String()}, nil
	}
	if active == nil && p.Kind == "stop" {
		return controlResponse{}, nil
	}
	if active == nil || active.ID != request.SessionID {
		return controlResponse{}, errors.New("TCP Ring 租約不存在或屬於其他群組")
	}
	member := false
	for _, selected := range active.peers {
		member = member || selected.ID == p.From && selected.IP == sourceIP
	}
	if !member {
		return controlResponse{}, errors.New("節點不屬於目前群組")
	}
	switch p.Kind {
	case "commit":
		if active.Role != "worker" || active.Phase != "prepared" || len(request.Addresses) != len(active.Members) {
			return controlResponse{}, errors.New("TCP Ring 啟動狀態或節點數不符")
		}
		seen := make(map[string]bool)
		for i, address := range request.Addresses {
			if seen[address] || !endpointAt(address, active.Members[i].IP) ||
				(i == active.Rank && address != active.Addresses[i]) {
				return controlResponse{}, errors.New("TCP Ring 端點不符握手保留")
			}
			seen[address] = true
		}
		_ = active.listener.Close()
		if _, err := s.backend.StartRing(active.ID, active.Model, active.Rank, request.Addresses, active.profile); err != nil {
			_ = s.stopLocked(ctx, err.Error(), false)
			return controlResponse{}, err
		}
		s.mu.Lock()
		s.active.Addresses, s.active.listener, s.active.Phase, s.active.lastLease = append([]string(nil), request.Addresses...), nil, "loading", time.Now()
		s.mu.Unlock()
	case "lease":
		if active.Role != "worker" || active.Phase == "prepared" || !s.backend.Status().Running {
			return controlResponse{}, errors.New("工作節點程序已結束")
		}
		s.mu.Lock()
		s.active.lastLease = time.Now()
		if request.Ready {
			s.active.Phase = "running"
		}
		s.mu.Unlock()
	case "stop":
		// 工作節點離開時通知其餘成員，不回呼正在等待此回覆的發起者。
		return controlResponse{}, s.stopLocked(ctx, "", active.Role == "coordinator", p.From)
	default:
		return controlResponse{}, errors.New("未知的 TCP Ring 控制命令")
	}
	return controlResponse{}, nil
}

func (s *Service) Stop(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.stopLocked(ctx, "", true)
}

func (s *Service) stopLocked(ctx context.Context, reason string, notify bool, except ...string) error {
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if active == nil {
		return nil
	}
	if active.listener != nil {
		_ = active.listener.Close()
	}
	err := s.backend.Stop(ctx)
	s.backend.ReleaseRing(active.ID)
	if notify {
		peers := make([]Peer, 0, len(active.peers))
		for _, peer := range active.peers {
			if len(except) == 0 || peer.ID != except[0] {
				peers = append(peers, peer)
			}
		}
		notifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, notifyErr := s.callPeers(notifyCtx, peers, "stop", controlRequest{SessionID: active.ID})
		cancel()
		if notifyErr != nil && reason == "" {
			reason = "本機已停止；未回應節點將由租約回收：" + notifyErr.Error()
		}
	}
	s.mu.Lock()
	s.active, s.lastError = nil, reason
	s.mu.Unlock()
	return err
}

func (s *Service) maintain() {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if active == nil {
		return
	}
	status := s.backend.Status()
	reason := ""
	if active.Phase != "prepared" && active.Phase != "preparing" && active.Phase != "starting" && !status.Running {
		reason = "TCP Ring 程序已結束：" + status.LastError
	} else if active.Role == "worker" && time.Since(active.lastLease) > leaseLifetime {
		reason = "主節點租約逾時，已停止工作節點"
	} else if active.Role == "coordinator" {
		ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
		_, err := s.callPeers(ctx, active.peers, "lease", controlRequest{SessionID: active.ID, Ready: status.Ready})
		cancel()
		if err == nil {
			s.mu.Lock()
			s.active.lastLease = time.Now()
			if status.Ready {
				s.active.Phase = "running"
			}
			s.mu.Unlock()
		} else if time.Since(active.lastLease) > leaseLifetime {
			reason = "工作節點租約逾時：" + err.Error()
		}
	}
	if reason != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		_ = s.stopLocked(ctx, reason, true)
		cancel()
	}
}
