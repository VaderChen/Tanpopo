package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"LlamaLoader/src/domain"
	"LlamaLoader/src/llamacpp"
)

type fakeBackend struct {
	mu        sync.Mutex
	owner     string
	status    domain.LlamaStatus
	failStart bool
	rank      int
	root      string
}

func (b *fakeBackend) RingModelDirectory(_ string) string { return b.root }

func (b *fakeBackend) RingCapabilities(context.Context) (llamacpp.RingCapabilities, error) {
	return llamacpp.RingCapabilities{Version: "smoke-v1", Available: true, ManagedParentStdin: true, MaxNodes: 8}, nil
}
func (b *fakeBackend) InspectRingModel(model string, _ ...llamacpp.RingOptions) (llamacpp.RingModel, error) {
	if _, err := os.Stat(filepath.Join(b.root, model, "config.json")); err != nil {
		return llamacpp.RingModel{}, errors.New("找不到模型")
	}
	return llamacpp.RingModel{Path: model, Architecture: "llama", Kind: "text", Fingerprint: strings.Repeat("a", 64)}, nil
}
func (b *fakeBackend) ReserveRing(owner string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.owner != "" {
		return errors.New("busy")
	}
	b.owner = owner
	return nil
}
func (b *fakeBackend) ReleaseRing(owner string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.owner == owner {
		b.owner = ""
	}
}
func (b *fakeBackend) StartRing(owner, model string, rank int, addresses []string, _ domain.StartupCommand, _ ...llamacpp.RingOptions) (domain.LlamaStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if owner != b.owner || addresses[0] == addresses[1] || b.failStart {
		return b.status, errors.New("啟動失敗")
	}
	// 模擬 MLX 一啟動便連線的行為。fake 不會監聽原生埠，所以任何成功
	// 連線都代表仍有 Go 保留 listener，會讓真實 Ring 誤接後停在 accept。
	for _, address := range addresses {
		conn, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return b.status, errors.New("啟動前尚未釋放所有節點的 Ring 保留埠")
		}
	}
	b.rank = rank
	b.status = domain.LlamaStatus{Running: true, Ready: rank == 0, Model: model}
	return b.status, nil
}
func (b *fakeBackend) Status() domain.LlamaStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}
func (b *fakeBackend) Stop(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = domain.LlamaStatus{}
	return nil
}

func loopbackInterface(t *testing.T) string {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 && iface.Flags&net.FlagUp != 0 {
			return iface.Name
		}
	}
	t.Fatal("找不到 Loopback 介面")
	return ""
}

func testNode(t *testing.T, port int) (*Service, *fakeBackend) {
	t.Helper()
	backend := &fakeBackend{root: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(backend.root, "fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"config.json": `{"model_type":"llama"}`, "model.safetensors": "fixture weights"} {
		if err := os.WriteFile(filepath.Join(backend.root, "fixture", name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var service *Service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == modelTransferPath {
			service.ModelTransfer(w, r)
		} else {
			service.Control(w, r)
		}
	}))
	_, httpPort, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	n, _ := strconv.Atoi(httpPort)
	ctx, cancel := context.WithCancel(context.Background())
	service = New(ctx, filepath.Join(t.TempDir(), "cluster.json"), n, backend)
	err := service.Configure(ConfigUpdate{Enabled: true, DiscoveryPort: port, Interface: loopbackInterface(t)})
	if err != nil {
		cancel()
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-service.done:
		case <-time.After(15 * time.Second):
			t.Error("探索服務未停止")
		}
		server.Close()
	})
	return service, backend
}

func discoveryPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func awaitPeers(t *testing.T, nodes ...*Service) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		complete := true
		for _, node := range nodes {
			node.announce()
			peers := node.Status().Peers
			complete = complete && len(peers) == len(nodes)-1
			for _, peer := range peers {
				complete = complete && !peer.Busy
			}
		}
		if complete {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, node := range nodes {
		t.Logf("探索狀態：%+v", node.Status())
	}
	t.Fatal("UDP 未互相找到空閒節點")
}

func TestKeylessDiscoveryAndMultiNodeRing(t *testing.T) {
	port := discoveryPort(t)
	a, aBackend := testNode(t, port)
	b, bBackend := testNode(t, port)
	c, cBackend := testNode(t, port)
	unused, unusedBackend := testNode(t, port)
	awaitPeers(t, a, b, c, unused)
	if a.Status().Peers[0].IP != "127.0.0.1" {
		t.Fatal("未使用封包來源位址")
	}
	saved, err := os.ReadFile(a.configPath)
	if err != nil || strings.Contains(string(saved), "pairing_key") {
		t.Fatal("免金鑰設定不應儲存金鑰", err)
	}
	info, err := os.Stat(a.configPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("設定檔權限：%v %v", info, err)
	}
	profile := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 512}
	ids := []string{b.Status().Local.ID, c.Status().Local.ID}
	for _, invalid := range [][]string{nil, {ids[0], ids[0]}, {randomID()}} {
		if _, err := a.Start(context.Background(), invalid, "fixture", profile); err == nil {
			t.Fatal("無效選取被接受")
		}
	}
	status, err := a.Start(context.Background(), ids, "fixture", profile)
	if err != nil {
		t.Fatal(err)
	}
	if status.Session == nil || len(status.Session.Members) != 3 || aBackend.rank != 0 || bBackend.rank != 1 || cBackend.rank != 2 {
		t.Fatal("未啟動三個不同 Rank")
	}
	if unusedBackend.Status().Running || unused.Status().Session != nil {
		t.Fatal("不應啟動未勾選節點")
	}
	status.Session.Members[0].ID = "modified"
	status.Session.Addresses[0] = "modified"
	if a.Status().Session.Members[0].ID == "modified" || a.Status().Session.Addresses[0] == "modified" {
		t.Fatal("狀態沒有深複製")
	}
	if err := aBackend.ReserveRing("conflict"); err == nil {
		t.Fatal("同時啟動未被阻擋")
	}
	a.maintain()
	for _, node := range []*Service{a, b, c} {
		if node.Status().Session.Phase != "running" {
			t.Fatal("租約未交換就緒狀態")
		}
	}
	// 任一工作節點停止，主節點必須清理全部已選成員。
	if err = c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, node := range []*Service{a, b, c, unused} {
		if node.backend.Status().Running || node.Status().Session != nil {
			t.Fatal("整組停止未完成")
		}
		if node.Status().LastError != "" {
			t.Fatal("正常停止不應顯示失敗原因")
		}
	}
}

func TestWorkerFailureReasonReachesAllMembers(t *testing.T) {
	port := discoveryPort(t)
	a, _ := testNode(t, port)
	b, backend := testNode(t, port)
	c, _ := testNode(t, port)
	awaitPeers(t, a, b, c)
	profile := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 512}
	if _, err := a.Start(context.Background(), []string{b.Status().Local.ID, c.Status().Local.ID}, "fixture", profile); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.status.Running = false
	backend.status.LastError = "TCP 連線失敗：Connection refused"
	backend.mu.Unlock()
	b.maintain()
	for _, node := range []*Service{a, b, c} {
		status := node.Status()
		if status.Session != nil || node.backend.Status().Running {
			t.Fatal("工作節點失敗後未停止整組")
		}
		if !strings.Contains(status.LastError, b.name) || !strings.Contains(status.LastError, "Connection refused") {
			t.Fatalf("未保留失敗節點與原因：%q", status.LastError)
		}
	}
}

func TestHandshakeRollbackAndLeaseExpiry(t *testing.T) {
	port := discoveryPort(t)
	a, aBackend := testNode(t, port)
	b, bBackend := testNode(t, port)
	c, cBackend := testNode(t, port)
	awaitPeers(t, a, b, c)
	profile := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 512}
	ids := []string{b.Status().Local.ID, c.Status().Local.ID}
	// 部分 worker 已成功啟動時，另一個 worker 失敗也必須全部回滾。
	cBackend.failStart = true
	if _, err := a.Start(context.Background(), ids, "fixture", profile); err == nil {
		t.Fatal("應回報節點啟動失敗")
	}
	if a.Status().Session != nil || b.Status().Session != nil || c.Status().Session != nil || bBackend.Status().Running {
		t.Fatal("部分失敗未回滾")
	}
	cBackend.failStart = false
	awaitPeers(t, a, b, c)
	if _, err := a.Start(context.Background(), ids, "fixture", profile); err != nil {
		t.Fatal(err)
	}
	b.opMu.Lock()
	b.mu.Lock()
	b.active.lastLease = time.Now().Add(-leaseLifetime - time.Second)
	b.mu.Unlock()
	b.opMu.Unlock()
	b.maintain()
	if b.Status().Session != nil || bBackend.Status().Running || aBackend.Status().Running || cBackend.Status().Running {
		t.Fatal("逾期租約未清理整組")
	}
}

func TestControlRejectsUnknownSourceReplayAndForeignSession(t *testing.T) {
	port := discoveryPort(t)
	a, _ := testNode(t, port)
	b, backend := testNode(t, port)
	awaitPeers(t, a, b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.control(ctx, packet{Kind: "prepare", From: a.Status().Local.ID}, "127.0.0.1", controlRequest{SessionID: randomID()}); err == nil {
		t.Fatal("已取消的握手仍可保留節點")
	}
	a.mu.Lock()
	p := a.packetLocked("prepare", b.Status().Local.ID, "", controlRequest{SessionID: randomID()})
	a.mu.Unlock()
	if !b.accept(p) || b.accept(p) {
		t.Fatal("重放保護無效")
	}
	stale := p
	stale.Nonce, stale.Time = randomID(), p.Time-60
	if b.accept(stale) {
		t.Fatal("過期封包仍可使用")
	}
	stale.Nonce, stale.Time, stale.Version = randomID(), p.Time, 1
	if b.accept(stale) {
		t.Fatal("舊版探索封包不應混入群組")
	}
	for _, identity := range []struct{ id, ip string }{{randomID(), "127.0.0.1"}, {a.Status().Local.ID, "127.0.0.2"}} {
		if _, err := b.control(context.Background(), packet{Kind: "prepare", From: identity.id}, identity.ip, controlRequest{SessionID: randomID()}); err == nil {
			t.Fatal("未知節點／錯誤來源被接受")
		}
	}
	profile := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 512}
	if _, err := a.Start(context.Background(), []string{b.Status().Local.ID}, "fixture", profile); err != nil {
		t.Fatal(err)
	}
	if _, err := a.call(context.Background(), a.Status().Peers[0], "stop", controlRequest{SessionID: randomID()}); err == nil {
		t.Fatal("其他租約能停止此工作")
	}
	if !backend.Status().Running {
		t.Fatal("錯誤租約停止了程序")
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestModelSyncSwitchesRunningServiceAndUsesMatchingCopy(t *testing.T) {
	port := discoveryPort(t)
	a, _ := testNode(t, port)
	b, bb := testNode(t, port)
	c, cb := testNode(t, port)
	if err := os.WriteFile(filepath.Join(bb.root, "fixture/model.safetensors"), []byte("different data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(cb.root, "fixture"), filepath.Join(cb.root, "renamed")); err != nil {
		t.Fatal(err)
	}
	awaitPeers(t, a, b, c)
	bb.mu.Lock()
	bb.status = domain.LlamaStatus{Running: true, Model: "another-model"}
	bb.mu.Unlock()
	b.announce()
	profile := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 512}
	if _, err := a.Start(context.Background(), []string{b.Status().Local.ID, c.Status().Local.ID}, "fixture", profile); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bb.Status().Model, "cluster-models/") || cb.Status().Model != "renamed" {
		t.Fatalf("未協調各端載入：%+v %+v", bb.Status(), cb.Status())
	}
	data, _ := os.ReadFile(filepath.Join(bb.root, "fixture/model.safetensors"))
	if string(data) != "different data" {
		t.Fatal("覆寫了遠端原有模型")
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestModelDownloadCanBeCancelledWithoutBlockingControl(t *testing.T) {
	port := discoveryPort(t)
	a, _ := testNode(t, port)
	b, bb := testNode(t, port)
	if err := os.RemoveAll(filepath.Join(bb.root, "fixture")); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	transport := b.transferClient.Transport
	b.transferClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		var p packet
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		if p.Kind == "model-file" {
			close(entered)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return transport.RoundTrip(r)
	})
	awaitPeers(t, a, b)
	profile := domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 512}
	if _, err := a.Begin(context.Background(), []string{b.Status().Local.ID}, "fixture", profile); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("未進入下載")
	}
	b.mu.Lock()
	worker := b.active
	b.mu.Unlock()
	if worker == nil {
		t.Fatal("下載未保留租約")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := waitPreparation(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if a.Status().Session != nil || b.Status().Session != nil || bb.Status().Running {
		t.Fatal("取消未釋放叢集")
	}
	entries, _ := os.ReadDir(bb.root)
	if len(entries) > 0 {
		t.Fatalf("取消留下暫存檔：%v", entries)
	}
}

func TestModelTransferRejectsNonMemberWrongSourceAndInvalidIndex(t *testing.T) {
	port := discoveryPort(t)
	a, _ := testNode(t, port)
	b, _ := testNode(t, port)
	c, _ := testNode(t, port)
	awaitPeers(t, a, b, c)
	if _, err := a.Start(context.Background(), []string{b.Status().Local.ID}, "fixture", domain.StartupCommand{Runtime: domain.RuntimeMLXServer, ContextSize: 512}); err != nil {
		t.Fatal(err)
	}
	owner := a.Status().Session.ID
	for _, test := range []struct {
		sender                 *Service
		address, session, kind string
		index, want            int
	}{
		{c, "127.0.0.1", owner, "model-manifest", 0, 403},
		{b, "127.0.0.2", owner, "model-manifest", 0, 403},
		{b, "127.0.0.1", randomID(), "model-manifest", 0, 403},
		{b, "127.0.0.1", owner, "model-file", -1, 409},
		{b, "127.0.0.1", owner, "model-manifest", 0, 200},
	} {
		test.sender.mu.Lock()
		p := test.sender.packetLocked(test.kind, a.Status().Local.ID, "", modelRequest{owner, test.index})
		test.sender.mu.Unlock()
		p.Payload, _ = json.Marshal(modelRequest{test.session, test.index})
		body, _ := json.Marshal(p)
		r := httptest.NewRequest("POST", modelTransferPath, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = test.address + ":12345"
		w := httptest.NewRecorder()
		a.ModelTransfer(w, r)
		if w.Code != test.want {
			t.Fatalf("status=%d want=%d", w.Code, test.want)
		}
	}
}
