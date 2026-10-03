// Package cluster 以 UDP 探索區網 Server，透過免金鑰握手管理原生 TCP Ring。
package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"LlamaLoader/src/domain"
	"LlamaLoader/src/llamacpp"
	"LlamaLoader/src/modelbundle"
)

const (
	DefaultDiscoveryPort = 10083
	multicastAddress     = "239.255.82.82"
	peerLifetime         = 12 * time.Second
	leaseLifetime        = 20 * time.Second
	controlPath          = "/api/cluster/control"
)

type Backend interface {
	RingCapabilities(context.Context) (llamacpp.RingCapabilities, error)
	InspectRingModel(string) (llamacpp.RingModel, error)
	RingModelDirectory() string
	ReserveRing(string) error
	ReleaseRing(string)
	StartRing(string, string, int, []string, domain.StartupCommand) (domain.LlamaStatus, error)
	Status() domain.LlamaStatus
	Stop(context.Context) error
}

type Config struct {
	NodeID        string `json:"node_id"`
	Enabled       bool   `json:"enabled"`
	DiscoveryPort int    `json:"discovery_port"`
	Interface     string `json:"interface,omitempty"`
}

type ConfigUpdate struct {
	Enabled       bool   `json:"enabled"`
	DiscoveryPort int    `json:"discovery_port"`
	Interface     string `json:"interface,omitempty"`
}

type Node struct {
	ID               string                    `json:"id"`
	Name             string                    `json:"name"`
	Platform         string                    `json:"platform"`
	Port             int                       `json:"port"`
	Capabilities     llamacpp.RingCapabilities `json:"capabilities"`
	Busy             bool                      `json:"busy"`
	Clustered        bool                      `json:"clustered"`
	ModelSyncVersion int                       `json:"model_sync_version"`
}

type Peer struct {
	Node
	IP       string    `json:"ip"`
	LastSeen time.Time `json:"last_seen"`
}

type Member struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type Session struct {
	ID          string             `json:"id"`
	Members     []Member           `json:"members"`
	Rank        int                `json:"rank"`
	Role        string             `json:"role"`
	Phase       string             `json:"phase"`
	Model       string             `json:"model"`
	Addresses   []string           `json:"addresses"`
	StartedAt   time.Time          `json:"started_at"`
	Preparation []ModelPreparation `json:"preparation,omitempty"`
}

type ModelPreparation struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
	Model  string `json:"model,omitempty"`
	modelbundle.Progress
}

type Status struct {
	Enabled       bool     `json:"enabled"`
	DiscoveryPort int      `json:"discovery_port"`
	Interface     string   `json:"interface"`
	Local         Node     `json:"local"`
	Peers         []Peer   `json:"peers"`
	Session       *Session `json:"session,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
}

type activeSession struct {
	Session
	peers        []Peer
	listener     net.Listener
	lastLease    time.Time
	profile      domain.StartupCommand
	ctx          context.Context
	cancel       context.CancelFunc
	snapshot     *modelbundle.Snapshot
	descriptor   llamacpp.RingModel
	prepareError string
	starting     bool
	done         chan struct{}
}

type discoverySocket struct {
	conn     *net.UDPConn
	receiver *net.UDPConn
	name     string
}

type Service struct {
	ctx            context.Context
	backend        Backend
	configPath     string
	port           int
	name           string
	client         *http.Client
	transferClient *http.Client
	opMu           sync.Mutex
	mu             sync.Mutex
	config         Config
	capability     llamacpp.RingCapabilities
	peers          map[string]Peer
	seen           map[string]time.Time
	challenges     map[string]time.Time
	sockets        []discoverySocket
	active         *activeSession
	lastError      string
	closed         bool
	done           chan struct{}
}

func New(ctx context.Context, configPath string, port int, backend Backend) *Service {
	name, _ := os.Hostname()
	s := &Service{ctx: ctx, configPath: configPath, port: port, name: name, backend: backend,
		peers: make(map[string]Peer), seen: make(map[string]time.Time), challenges: make(map[string]time.Time), done: make(chan struct{}),
		client: &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	s.transferClient = &http.Client{Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 8 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.config = Config{NodeID: randomID(), DiscoveryPort: DefaultDiscoveryPort}
	data, err := os.ReadFile(configPath)
	if err == nil {
		err = json.Unmarshal(data, &s.config)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.lastError = "讀取 TCP Ring 設定失敗：" + err.Error()
		s.config.Enabled = false
	} else if s.config.Enabled {
		if err := s.enable(); err != nil {
			s.lastError = err.Error()
			s.config.Enabled = false
		}
	}
	go s.run()
	return s
}

func randomID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}

func validID(value string) bool {
	bytes, err := hex.DecodeString(value)
	return err == nil && len(bytes) == 16
}

func (s *Service) nodeLocked() Node {
	return Node{ID: s.config.NodeID, Name: s.name, Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Port: s.port, Capabilities: s.capability, Busy: s.active != nil, Clustered: s.active != nil, ModelSyncVersion: 1}
}

func (s *Service) Status() Status {
	busy := s.backend.Status().Running
	s.mu.Lock()
	defer s.mu.Unlock()
	status := Status{Enabled: s.config.Enabled, DiscoveryPort: s.config.DiscoveryPort,
		Interface: s.config.Interface, Local: s.nodeLocked(), Peers: []Peer{}, LastError: s.lastError}
	status.Local.Busy = status.Local.Busy || busy
	for _, peer := range s.peers {
		if time.Since(peer.LastSeen) < peerLifetime {
			status.Peers = append(status.Peers, peer)
		}
	}
	sort.Slice(status.Peers, func(i, j int) bool { return status.Peers[i].ID < status.Peers[j].ID })
	if s.active != nil {
		copy := s.active.Session
		copy.Preparation = append([]ModelPreparation(nil), copy.Preparation...)
		copy.Members = append([]Member(nil), copy.Members...)
		copy.Addresses = append([]string(nil), copy.Addresses...)
		status.Session = &copy
	}
	return status
}

// Configure 明確啟用區網探索；舊設定檔的 pairing_key 在重新儲存時移除。
func (s *Service) Configure(update ConfigUpdate) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	config := s.config
	busy, closed := s.active != nil, s.closed
	s.mu.Unlock()
	if closed || busy {
		return errors.New("請先停止叢集服務，再變更探索設定")
	}
	config.Enabled, config.Interface = update.Enabled, update.Interface
	if !validID(config.NodeID) {
		config.NodeID = randomID()
	}
	if update.DiscoveryPort != 0 {
		config.DiscoveryPort = update.DiscoveryPort
	}
	if config.DiscoveryPort < 1024 || config.DiscoveryPort > 65535 {
		return errors.New("UDP 探索埠必須介於 1024–65535")
	}
	if err := saveConfig(s.configPath, config); err != nil {
		return err
	}
	s.mu.Lock()
	s.closeSocketsLocked()
	s.config, s.lastError = config, ""
	s.peers, s.seen, s.challenges = make(map[string]Peer), make(map[string]time.Time), make(map[string]time.Time)
	s.mu.Unlock()
	if config.Enabled {
		if err := s.enable(); err != nil {
			s.mu.Lock()
			s.config.Enabled, s.lastError = false, err.Error()
			s.mu.Unlock()
			config.Enabled = false
			return errors.Join(err, saveConfig(s.configPath, config))
		}
	}
	return nil
}

func saveConfig(filename string, config Config) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".cluster-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filename)
}

func (s *Service) closeSocketsLocked() {
	for _, socket := range s.sockets {
		_ = socket.conn.Close()
		_ = socket.receiver.Close()
	}
	s.sockets = nil
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.opMu.Lock()
	s.mu.Lock()
	active := s.active
	s.closed = true
	s.closeSocketsLocked()
	s.mu.Unlock()
	err := s.stopLocked(ctx, "", true)
	s.opMu.Unlock()
	err = errors.Join(err, waitPreparation(ctx, active))
	s.client.CloseIdleConnections()
	s.transferClient.CloseIdleConnections()
	return err
}

func (s *Service) run() {
	defer close(s.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			_ = s.Shutdown(ctx)
			cancel()
			return
		case <-ticker.C:
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			s.announce()
			s.maintain()
		}
	}
}
