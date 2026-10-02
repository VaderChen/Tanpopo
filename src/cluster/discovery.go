package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// 區網探索不需要金鑰；版本、目的節點、時間與 nonce 防止誤投及重放。
// 實際啟動只接受已完成 UDP 回應驗證、來源 IP 相符的在線節點。
type packet struct {
	Version int             `json:"version"`
	Kind    string          `json:"kind"`
	From    string          `json:"from"`
	To      string          `json:"to,omitempty"`
	Nonce   string          `json:"nonce"`
	ReplyTo string          `json:"reply_to,omitempty"`
	Time    int64           `json:"time"`
	Payload json.RawMessage `json:"payload"`
}

func (s *Service) packetLocked(kind, target, reply string, payload any) packet {
	data, _ := json.Marshal(payload)
	p := packet{Version: 2, Kind: kind, From: s.config.NodeID, To: target, Nonce: randomID(), ReplyTo: reply,
		Time: time.Now().Unix(), Payload: data}
	return p
}

func (s *Service) accept(p packet) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled || (s.closed && p.Kind != "response") || p.Version != 2 || !validID(p.From) || p.From == s.config.NodeID ||
		!validID(p.Nonce) || (p.To != "" && p.To != s.config.NodeID) {
		return false
	}
	now := time.Now()
	if delta := now.Unix() - p.Time; delta < -30 || delta > 30 {
		return false
	}
	for nonce, seenAt := range s.seen {
		if now.Sub(seenAt) > time.Minute {
			delete(s.seen, nonce)
		}
	}
	cacheKey := p.From + p.Nonce
	if _, exists := s.seen[cacheKey]; exists || len(s.seen) >= 4096 {
		return false
	}
	s.seen[cacheKey] = now
	return true
}

func (s *Service) enable() error {
	s.mu.Lock()
	config := s.config
	s.mu.Unlock()
	if !validID(config.NodeID) || config.DiscoveryPort < 1024 || config.DiscoveryPort > 65535 {
		return errors.New("TCP Ring 節點識別碼或探索埠無效")
	}
	capability, err := s.backend.RingCapabilities(s.ctx)
	if err != nil {
		return err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	var sockets []discoverySocket
	group := &net.UDPAddr{IP: net.ParseIP(multicastAddress), Port: config.DiscoveryPort}
	var failures []error
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || (config.Interface != "" && config.Interface != iface.Name) {
			continue
		}
		addresses, _ := iface.Addrs()
		var ipv4 net.IP
		for _, address := range addresses {
			ip, _, _ := net.ParseCIDR(address.String())
			if ip.To4() != nil {
				ipv4 = ip.To4()
				break
			}
		}
		if ipv4 == nil || (iface.Flags&net.FlagMulticast == 0 && iface.Flags&net.FlagLoopback == 0) {
			continue
		}
		receiver, err := net.ListenMulticastUDP("udp4", &iface, group)
		var conn *net.UDPConn
		if err == nil {
			conn, err = net.ListenUDP("udp4", &net.UDPAddr{IP: ipv4})
		}
		if err == nil {
			err = configureMulticast(conn, ipv4)
		}
		if err != nil {
			if conn != nil {
				_ = conn.Close()
			}
			if receiver != nil {
				_ = receiver.Close()
			}
			failures = append(failures, fmt.Errorf("%s: %w", iface.Name, err))
			continue
		}
		sockets = append(sockets, discoverySocket{conn: conn, receiver: receiver, name: iface.Name})
	}
	if len(sockets) == 0 {
		return fmt.Errorf("無法開啟 UDP 探索，請檢查 IPv4 網路介面與探索埠：%v", errors.Join(failures...))
	}
	s.mu.Lock()
	s.capability, s.sockets = capability, sockets
	s.mu.Unlock()
	for _, socket := range sockets {
		go s.readDiscovery(socket.conn)
		go s.readDiscovery(socket.receiver)
	}
	s.announce()
	return nil
}

func (s *Service) announce() {
	busy := s.backend.Status().Running
	s.mu.Lock()
	if !s.config.Enabled || s.closed {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	for nonce, at := range s.challenges {
		if now.Sub(at) > peerLifetime {
			delete(s.challenges, nonce)
		}
	}
	for id, peer := range s.peers {
		if now.Sub(peer.LastSeen) >= peerLifetime {
			delete(s.peers, id)
		}
	}
	node := s.nodeLocked()
	node.Busy = node.Busy || busy
	sockets := append([]discoverySocket(nil), s.sockets...)
	p := s.packetLocked("hello", "", "", node)
	s.challenges[p.Nonce] = now
	port := s.config.DiscoveryPort
	s.mu.Unlock()
	data, _ := json.Marshal(p)
	for _, socket := range sockets {
		_, _ = socket.conn.WriteToUDP(data, &net.UDPAddr{IP: net.ParseIP(multicastAddress), Port: port})
	}
}

func (s *Service) readDiscovery(conn *net.UDPConn) {
	buffer := make([]byte, 4097)
	for {
		n, source, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if n > 4096 || source.IP.To4() == nil || source.IP.IsUnspecified() || source.IP.IsMulticast() {
			continue
		}
		var p packet
		if json.Unmarshal(buffer[:n], &p) != nil || (p.Kind != "hello" && p.Kind != "hello_ack") || !s.accept(p) {
			continue
		}
		var node Node
		if json.Unmarshal(p.Payload, &node) != nil || node.ID != p.From || node.Port < 1024 || node.Port > 65535 ||
			len(node.Name) > 255 || len(node.Capabilities.Version) > 256 || len(node.Platform) > 64 {
			continue
		}
		if p.Kind == "hello" {
			busy := s.backend.Status().Running
			s.mu.Lock()
			local := s.nodeLocked()
			local.Busy = local.Busy || busy
			reply := s.packetLocked("hello_ack", p.From, p.Nonce, local)
			s.mu.Unlock()
			data, _ := json.Marshal(reply)
			_, _ = conn.WriteToUDP(data, source)
			continue
		}
		s.mu.Lock()
		if at, exists := s.challenges[p.ReplyTo]; p.To == s.config.NodeID && exists && time.Since(at) < peerLifetime {
			// 使用實際來源 IP，不接受封包指定 URL、SSH 或本機可執行檔路徑。
			s.peers[p.From] = Peer{Node: node, IP: source.IP.String(), LastSeen: time.Now()}
		}
		s.mu.Unlock()
	}
}
