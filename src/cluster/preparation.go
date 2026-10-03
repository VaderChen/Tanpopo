package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"time"

	"LlamaLoader/src/domain"
	"LlamaLoader/src/llamacpp"
	"LlamaLoader/src/modelbundle"
)

// Begin 只保留工作並立即回傳；檔案核對與下載不依附瀏覽器請求的生命週期。
func (s *Service) Begin(ctx context.Context, peerIDs []string, model string, profile domain.StartupCommand, launch ...llamacpp.RingOptions) (Status, error) {
	var options llamacpp.RingOptions
	if len(launch) > 0 {
		options = launch[0]
	}
	options.TextOnly = options.TextOnly || options.DFlashEnabled
	if options.DraftModel == "" {
		options.DraftModel = profile.DraftModel
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return s.Status(), err
	}
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
	if !enabled || closed || busy || len(peers) != len(peerIDs) || len(peers) < 1 || len(peers)+1 > llamacpp.MaxRingNodes || len(peers)+1 > capability.MaxNodes {
		return s.Status(), errors.New("請啟用探索、選擇 1–7 個不同的在線節點，並先停止目前叢集")
	}
	for _, peer := range peers {
		if peer.Clustered || peer.ModelSyncVersion != 2 || !peer.Capabilities.Available || !peer.Capabilities.ManagedParentStdin || peer.Capabilities.Version != capability.Version || peer.Capabilities.MaxNodes < len(peers)+1 {
			return s.Status(), fmt.Errorf("%s 已加入叢集，或 Tanpopo／Runtime 不支援相同的模型同步與節點數，請更新兩端", peer.Name)
		}
	}
	if profile.Runtime != domain.RuntimeMLXServer || profile.ContextSize < 128 || profile.ContextSize > 1048576 {
		return s.Status(), errors.New("請選擇有效的 mlx-server 啟動參數")
	}
	if err := llamacpp.ValidateRingProfile(profile); err != nil {
		return s.Status(), err
	}
	descriptor, err := s.backend.InspectRingModel(model, options)
	if err != nil {
		return s.Status(), err
	}
	model = descriptor.Path
	owner := randomID()
	if err := s.backend.ReserveRing(owner); err != nil {
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
	jobCtx, cancel := context.WithCancel(s.ctx)
	active := &activeSession{Session: Session{ID: owner, Members: members, Rank: 0, Role: "coordinator", Phase: "verifying",
		Model: model, StartedAt: time.Now(), Addresses: make([]string, len(members))},
		peers: peers, listener: listener, lastLease: time.Now(), profile: profile, descriptor: descriptor, options: options,
		ctx: jobCtx, cancel: cancel, starting: true, done: make(chan struct{})}
	active.Addresses[0] = listener.Addr().String()
	for _, member := range members {
		active.Preparation = append(active.Preparation, ModelPreparation{NodeID: member.ID, Name: member.Name,
			Progress: modelbundle.Progress{Phase: "waiting"}})
	}
	s.mu.Lock()
	s.active, s.lastError = active, ""
	s.mu.Unlock()
	go func() {
		defer close(active.done)
		if err := s.coordinate(active); err != nil {
			s.failPreparation(active, err)
		}
	}()
	return s.Status(), nil
}

// Start 供需要等待交握的呼叫者使用；HTTP 管理介面使用 Begin 與狀態輪詢。
func (s *Service) Start(ctx context.Context, peerIDs []string, model string, profile domain.StartupCommand, launch ...llamacpp.RingOptions) (Status, error) {
	status, err := s.Begin(ctx, peerIDs, model, profile, launch...)
	if err != nil {
		return status, err
	}
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if active == nil {
		return s.Status(), errors.New("叢集準備已結束")
	}
	select {
	case <-active.done:
		status = s.Status()
		if status.Session == nil || status.Session.ID != active.ID {
			return status, fmt.Errorf("叢集準備未完成：%s", status.LastError)
		}
		return status, nil
	case <-ctx.Done():
		s.failPreparation(active, ctx.Err())
		return s.Status(), ctx.Err()
	}
}

func (s *Service) report(active *activeSession, rank int, progress modelbundle.Progress, model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != active {
		return
	}
	for i := range active.Preparation {
		if active.Preparation[i].NodeID == active.Members[rank].ID {
			active.Preparation[i].Progress = progress
			if model != "" {
				active.Preparation[i].Model = model
			}
		}
	}
}

func (s *Service) failPreparation(active *activeSession, err error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	current := s.active == active
	s.mu.Unlock()
	if !current {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_ = s.stopLocked(ctx, err.Error(), true)
}

func (s *Service) coordinate(active *activeSession) error {
	ctx := active.ctx
	// 已先取得獨占保留，正常單機服務可由這次明確的配對操作切換。
	if err := s.stopPreviousRuntime(active); err != nil {
		return err
	}
	var auxiliary []string
	if active.descriptor.MMProj != "" {
		auxiliary = []string{active.descriptor.MMProj}
	}
	snapshot, err := modelbundle.CreateSelection(ctx, s.backend.RingModelDirectory(active.Model), strings.TrimPrefix(active.Model, "gguf:"), auxiliary, func(p modelbundle.Progress) { s.report(active, 0, p, active.Model) })
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.active != active {
		s.mu.Unlock()
		return context.Canceled
	}
	active.snapshot = snapshot
	active.descriptor.Fingerprint = snapshot.Manifest.Digest
	active.Phase = "synchronizing"
	version := s.capability.Version
	s.mu.Unlock()
	s.report(active, 0, modelbundle.Progress{Phase: "prepared", BytesDone: snapshot.Manifest.Bytes, BytesTotal: snapshot.Manifest.Bytes}, active.Model)
	request := controlRequest{SessionID: active.ID, Model: active.descriptor, Version: version, Members: active.Members, ContextSize: active.profile.ContextSize,
		Options: llamacpp.RingOptions{FastGGUFEnabled: active.options.FastGGUFEnabled, GGUFStrategy: active.options.GGUFStrategy, TextOnly: active.options.TextOnly}}
	if _, err := s.callPeers(ctx, active.peers, "prepare", request); err != nil {
		return err
	}
	lastSuccess := time.Now()
	var prepared []controlResponse
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		replies, err := s.callPeers(pollCtx, active.peers, "lease", controlRequest{SessionID: active.ID})
		cancel()
		for i, reply := range replies {
			if reply.Error != "" {
				return fmt.Errorf("%s：%s", active.peers[i].Name, reply.Error)
			}
		}
		ready := err == nil
		if err == nil {
			lastSuccess = time.Now()
			for i, reply := range replies {
				if reply.Error != "" {
					return fmt.Errorf("%s：%s", active.peers[i].Name, reply.Error)
				}
				ready = ready && reply.Phase == "prepared"
				for _, progress := range reply.Preparation {
					if progress.NodeID == active.peers[i].ID {
						s.report(active, i+1, progress.Progress, progress.Model)
					}
				}
			}
		} else if time.Since(lastSuccess) > leaseLifetime {
			return fmt.Errorf("模型同步租約失效：%w", err)
		}
		if ready {
			prepared = replies
			break
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	// 只在最後幾個短交握階段持鎖；下載／核對時停止按鈕始終可操作。
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	current := s.active == active
	s.mu.Unlock()
	if !current || ctx.Err() != nil {
		return context.Canceled
	}
	addresses := append([]string(nil), active.Addresses...)
	for i, reply := range prepared {
		if !endpointAt(reply.Address, active.peers[i].IP) {
			return fmt.Errorf("%s 回傳的 Ring 位址不符來源", active.peers[i].Name)
		}
		addresses[i+1] = reply.Address
	}
	request.Addresses = addresses
	s.mu.Lock()
	active.Addresses, active.Phase = addresses, "starting"
	s.mu.Unlock()
	if _, err := s.callPeers(ctx, active.peers, "arm", request); err != nil {
		return err
	}
	_ = active.listener.Close()
	s.mu.Lock()
	active.listener = nil
	s.mu.Unlock()
	if _, err := s.callPeers(ctx, active.peers, "commit", request); err != nil {
		return err
	}
	if _, err := s.backend.StartRing(active.ID, active.Model, 0, addresses, active.profile, active.options); err != nil {
		return err
	}
	s.mu.Lock()
	active.Phase, active.lastLease, active.starting = "loading", time.Now(), false
	s.mu.Unlock()
	return nil
}

func (s *Service) prepareWorker(active *activeSession, source Peer) {
	defer close(active.done)
	err := func() error {
		if err := s.stopPreviousRuntime(active); err != nil {
			return err
		}
		manifest, err := s.fetchManifest(active.ctx, source, active.ID)
		if err != nil {
			return err
		}
		if manifest.Digest != active.descriptor.Fingerprint {
			return errors.New("發起端模型清單與交握摘要不同")
		}
		model, err := modelbundle.Ensure(active.ctx, s.backend.RingModelDirectory(active.descriptor.Path), strings.TrimPrefix(active.descriptor.Path, "gguf:"), manifest,
			func(ctx context.Context, index int) (io.ReadCloser, error) {
				return s.fetchModelFile(ctx, source, active.ID, index)
			},
			func(p modelbundle.Progress) { s.report(active, active.Rank, p, "") })
		if err != nil {
			return err
		}
		if active.descriptor.Format != "" {
			model = "gguf:" + model
		}
		if active.descriptor.MMProj != "" {
			active.options.MMProj = path.Join(path.Dir(strings.TrimPrefix(model, "gguf:")), active.descriptor.MMProj)
		}
		descriptor, err := s.backend.InspectRingModel(model, active.options)
		if err != nil {
			return err
		}
		if descriptor.Architecture != active.descriptor.Architecture || descriptor.Kind != active.descriptor.Kind {
			return errors.New("遠端 Runtime 解析的模型架構／模式與發起端不符")
		}
		s.opMu.Lock()
		defer s.opMu.Unlock()
		s.mu.Lock()
		current := s.active == active
		s.mu.Unlock()
		if !current || active.ctx.Err() != nil {
			return context.Canceled
		}
		listener, err := reserveAddress(source.IP)
		if err != nil {
			return err
		}
		if !endpointAt(listener.Addr().String(), active.Members[active.Rank].IP) {
			listener.Close()
			return errors.New("節點使用不同網路路徑")
		}
		s.mu.Lock()
		active.Model, active.Phase, active.listener = model, "prepared", listener
		active.Addresses[active.Rank] = listener.Addr().String()
		s.mu.Unlock()
		s.report(active, active.Rank, modelbundle.Progress{Phase: "prepared", BytesDone: manifest.Bytes, BytesTotal: manifest.Bytes}, model)
		return nil
	}()
	if err != nil {
		s.mu.Lock()
		if s.active == active {
			active.prepareError = err.Error()
		}
		s.mu.Unlock()
	}
}

func (s *Service) stopPreviousRuntime(active *activeSession) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	current := s.active == active
	s.mu.Unlock()
	if !current || active.ctx.Err() != nil {
		return context.Canceled
	}
	return s.backend.Stop(active.ctx)
}
