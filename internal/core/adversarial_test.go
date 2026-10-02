package core

import (
	"context"
	"errors"
	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

type fakeTunnel struct {
	mu                  sync.Mutex
	running, configured bool
	selection           string
	directCalls         int
	selected            []string
	failSelect          bool
	bootstrapCalls      int
	proxies             map[int]*url.URL
	health              *url.URL
}

func (f *fakeTunnel) Name() string { return "fake" }
func (f *fakeTunnel) Capabilities() tunnel.CoreCapabilities {
	return tunnel.CoreCapabilities{PersistentSelection: true}
}
func (f *fakeTunnel) Bootstrap(_ context.Context, pool tunnel.DesiredPool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = true
	f.configured = true
	f.selection = pool.Selection.Tag
	f.bootstrapCalls++
	return nil
}
func (f *fakeTunnel) ApplyPool(context.Context, tunnel.DesiredPool) error { return nil }
func (f *fakeTunnel) Select(_ context.Context, s tunnel.Selection) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSelect {
		return errors.New("selection interrupted")
	}
	f.selection = s.Tag
	f.selected = append(f.selected, s.Tag)
	return nil
}
func (f *fakeTunnel) EnterDirect(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.directCalls++
	return nil
}
func (f *fakeTunnel) ProbeEndpoint(slot int) (*url.URL, error) {
	if proxy, ok := f.proxies[slot]; ok {
		return proxy, nil
	}
	return nil, errors.New("not used")
}
func (f *fakeTunnel) HealthEndpoint() (*url.URL, error) {
	if f.health != nil {
		return f.health, nil
	}
	return nil, errors.New("not used")
}
func (f *fakeTunnel) Ready(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running {
		return errors.New("tunnel unavailable")
	}
	return nil
}
func (f *fakeTunnel) ActualState(context.Context) (tunnel.ActualCoreState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return tunnel.ActualCoreState{Running: f.running, Configured: f.configured, Selection: tunnel.Selection{Tag: f.selection}}, nil
}
func (f *fakeTunnel) Restore(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configured = false
	return nil
}

type fakeAdapter struct {
	platform.Adapter
	supportsBypass, bypass bool
	mu                     sync.Mutex
	processChecks          int
}

func (p *fakeAdapter) Kind() string { return "fake" }
func (p *fakeAdapter) Capabilities() platform.Capabilities {
	return platform.Capabilities{DirectBypass: p.supportsBypass}
}
func (p *fakeAdapter) EnterDirectBypass(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bypass = true
	return nil
}
func (p *fakeAdapter) LeaveDirectBypass(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bypass = false
	return nil
}
func (p *fakeAdapter) DirectBypassActive(context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bypass, nil
}
func (p *fakeAdapter) EnsureFirewall(context.Context) error { return nil }
func (p *fakeAdapter) RemoveFirewall(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bypass = false
	return nil
}
func (p *fakeAdapter) XrayRunning(context.Context) bool { p.processChecks++; return true }

type fakeFetcher struct{ nodes []model.Node }

func (f fakeFetcher) FetchAll(context.Context, map[string]model.SourceState, bool) subscription.Result {
	return subscription.Result{Nodes: f.nodes, States: map[string]model.SourceState{}}
}

type fakeBenchmark struct {
	results []model.Measurement
	start   chan struct{}
	release chan struct{}
}

func (b fakeBenchmark) Run(ctx context.Context, _ []model.Node, _ bench.Progress) ([]model.Measurement, error) {
	if b.start != nil {
		close(b.start)
	}
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return b.results, nil
}
func fixture(t *testing.T) (*Manager, *fakeTunnel, *fakeAdapter) {
	t.Helper()
	c := config.Default()
	c.Pool.Size = 2
	d := t.TempDir()
	st, err := store.New(d, d, model.NewState("rc2", c.Xray.SlotTagPrefix, c.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	nodes := []model.Node{{ID: "active", Label: "active"}, {ID: "fallback", Label: "fallback"}}
	if err = st.ReplaceNodes(nodes); err != nil {
		t.Fatal(err)
	}
	if err = st.Update(func(s *model.State) error {
		s.Pool[0].NodeID = "active"
		s.Pool[0].Healthy = true
		s.Pool[1].NodeID = "fallback"
		s.Pool[1].Healthy = true
		s.XrayConfigured = true
		s.ActiveSlot = 0
		s.ActiveNodeID = "active"
		s.ActiveSince = time.Now()
		s.LastSwitchAt = time.Now()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ops, err := operation.New(d)
	if err != nil {
		t.Fatal(err)
	}
	tun := &fakeTunnel{running: true, configured: true, selection: c.Xray.SlotTagPrefix + "0"}
	p := &fakeAdapter{supportsBypass: true}
	m := New(c, "rc2", st, ops, p, tun, fakeFetcher{nodes}, fakeBenchmark{})
	m.ctx = context.Background()
	m.benchmarkQueued = true
	return m, tun, p
}
func TestBenchmarkNeverEntersDirect(t *testing.T) {
	m, tun, _ := fixture(t)
	m.bench = fakeBenchmark{results: []model.Measurement{{NodeID: "active", Healthy: false}, {NodeID: "fallback", Healthy: false}}}
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if tun.directCalls != 0 || m.State().DirectMode || m.State().ActiveNodeID != "active" {
		t.Fatal("failed benchmark changed the working route")
	}
}
func TestBenchmarkAfterFailoverKeepsFallback(t *testing.T) {
	m, tun, _ := fixture(t)
	if err := m.store.Update(func(s *model.State) error { *s = vpnState(*s, s.Pool[1], "emergency failover"); return nil }); err != nil {
		t.Fatal(err)
	}
	m.bench = fakeBenchmark{results: []model.Measurement{{NodeID: "active", Healthy: true, Score: 1}, {NodeID: "fallback", Healthy: true, Score: 100}}}
	if err := m.RunBenchmark(context.Background(), "emergency", "test"); err != nil {
		t.Fatal(err)
	}
	if m.State().ActiveNodeID != "fallback" || tun.selection != m.cfg.Xray.SlotTagPrefix+"1" {
		t.Fatal("recovery benchmark immediately upgraded fallback")
	}
}
func TestXrayFailureUsesIndependentBypass(t *testing.T) {
	m, tun, p := fixture(t)
	tun.running = false
	m.checkHealth(context.Background())
	if !m.State().DirectMode || !p.bypass || tun.directCalls != 0 {
		t.Fatal("Xray failure did not use independent platform bypass")
	}
}
func TestUnsupportedBypassDoesNotClaimDirect(t *testing.T) {
	m, tun, p := fixture(t)
	tun.running = false
	p.supportsBypass = false
	m.checkHealth(context.Background())
	if m.State().DirectMode || p.bypass || m.State().LastHealthClass != "xray_failed" {
		t.Fatal("unsupported platform falsely claimed fail-open")
	}
}
func TestRestoreResetsControllerState(t *testing.T) {
	m, _, _ := fixture(t)
	if err := m.RestoreOriginalXray(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.State()
	if s.XrayConfigured || s.ActiveSlot != -1 || s.ActiveNodeID != "" || s.DirectMode || len(s.Pool) != 0 || len(s.Measurements) != 0 {
		t.Fatalf("restore left stale state: %#v", s)
	}
}
func TestCachedStatusDoesNotExecutePlatformCommands(t *testing.T) {
	m, _, p := fixture(t)
	for i := 0; i < 10; i++ {
		_ = m.Status(context.Background())
	}
	if p.processChecks != 0 {
		t.Fatal("status executed external command")
	}
}
func TestBenchmarkReservationIsSynchronous(t *testing.T) {
	m, _, _ := fixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	// The operation status may finish before its final event is persisted.
	// Join the worker before TempDir cleanup, including assertion failures.
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		m.Stop()
	}()
	m.bench = fakeBenchmark{start: started, release: release}
	op, err := m.RequestBenchmark(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	if op == nil || op.ID == "" || op.Status != "running" {
		t.Fatal("operation was not reserved before accepted response")
	}
	if _, err = m.RequestBenchmark(context.Background(), "second"); !errors.Is(err, operation.ErrBusy) {
		t.Fatal("concurrent benchmark reservation accepted")
	}
	<-started
	close(release)
	deadline := time.Now().Add(time.Second)
	for m.ops.Current().Status == "running" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.ops.Current().Status == "running" {
		t.Fatal("reserved benchmark did not finish")
	}
}
func TestInterruptedSelectionReplaysJournal(t *testing.T) {
	m, tun, _ := fixture(t)
	tun.failSelect = true
	if err := m.SwitchSlot(context.Background(), 1); err == nil {
		t.Fatal("expected injected failure")
	}
	tx, err := m.store.PendingTransaction()
	if err != nil || tx == nil {
		t.Fatal("failed selection has no journal")
	}
	tun.failSelect = false
	if err = m.reconcileStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.State().ActiveNodeID != "fallback" || tun.selection != m.cfg.Xray.SlotTagPrefix+"1" || tun.bootstrapCalls == 0 {
		t.Fatal("startup did not forward-repair interrupted desired state")
	}
	tx, err = m.store.PendingTransaction()
	if err != nil || tx != nil {
		t.Fatal("completed replay left pending journal")
	}
}

// A subprocess is killed, rather than returned normally, at each durable phase.
// Fake tunnel/platform adapters model the external resources during replay.
func TestCrashRecoveryAtEveryJournalStep(t *testing.T) {
	if os.Getenv("KRM_CRASH_HELPER") == "1" {
		c := config.Default()
		c.Pool.Size = 2
		dir := os.Getenv("KRM_CRASH_DIR")
		st, err := store.New(dir, dir, model.NewState("rc2", c.Xray.SlotTagPrefix, 2))
		if err != nil {
			panic(err)
		}
		before := st.State()
		next := vpnState(before, before.Pool[1], "crash-recovered selection")
		tx := store.Transaction{ID: "crash-test", Kind: "select", Before: before, Desired: next, Nodes: st.Nodes()}
		if err = st.PrepareTransaction(tx); err != nil {
			panic(err)
		}
		stage := os.Getenv("KRM_CRASH_STAGE")
		for _, current := range []string{store.Prepared, store.XrayFilesStaged, store.XrayRuntimeApplied, store.FirewallApplied, store.SelectionApplied, store.StateCommitted} {
			if current != store.Prepared {
				if current == store.StateCommitted {
					if err = st.Update(func(s *model.State) error { *s = next; return nil }); err != nil {
						panic(err)
					}
				}
				if err = st.AdvanceTransaction(current); err != nil {
					panic(err)
				}
			}
			if current == stage {
				if err = syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
					panic(err)
				}
				select {}
			}
		}
		panic("unknown crash stage")
	}
	for _, stage := range []string{store.Prepared, store.XrayFilesStaged, store.XrayRuntimeApplied, store.FirewallApplied, store.SelectionApplied, store.StateCommitted} {
		t.Run(stage, func(t *testing.T) {
			m, tun, p := fixture(t)
			dir := filepath.Dir(m.store.Path("state.json"))
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecoveryAtEveryJournalStep$")
			cmd.Env = append(os.Environ(), "KRM_CRASH_HELPER=1", "KRM_CRASH_DIR="+dir, "KRM_CRASH_STAGE="+stage)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatal("helper was not killed")
			}
			if len(output) != 0 {
				t.Fatalf("helper failed before injected kill: %s", output)
			}
			reopened, err := store.New(dir, dir, model.NewState("rc2", m.cfg.Xray.SlotTagPrefix, 2))
			if err != nil {
				t.Fatal(err)
			}
			m.store = reopened
			if err = m.reconcileStartup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if m.State().ActiveNodeID != "fallback" || tun.selection != m.cfg.Xray.SlotTagPrefix+"1" || p.bypass {
				t.Fatal("crash replay did not reconcile external selection and state")
			}
			if tx, err := m.store.PendingTransaction(); err != nil || tx != nil {
				t.Fatal("crash replay journal incomplete")
			}
		})
	}
}

func TestEmergencyFallbackProbesAreParallel(t *testing.T) {
	m, tun, _ := fixture(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer fast.Close()
	slowURL, _ := url.Parse(slow.URL)
	fastURL, _ := url.Parse(fast.URL)
	tun.proxies = map[int]*url.URL{0: slowURL, 1: fastURL}
	targets := []config.Target{{ID: "a", URL: "http://first.example/", Policy: "exact:204"}, {ID: "b", URL: "http://second.example/", Policy: "exact:204"}}
	started := time.Now()
	slot, ok := m.emergencyCandidate(context.Background(), m.State(), targets, true)
	if !ok || slot.Index != 1 {
		t.Fatal("first confirmed fallback was not selected")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("fallback waited for slow slot: %s", elapsed)
	}
}

func TestStopWaitsForReservedBenchmarkAndRejectsLateWork(t *testing.T) {
	m, _, _ := fixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	m.bench = fakeBenchmark{start: started, release: release}
	if _, err := m.RequestBenchmark(context.Background(), "api"); err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan struct{})
	go func() { m.Stop(); close(done) }()
	select {
	case <-done:
		t.Fatal("Stop returned before benchmark cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not finish after benchmark cleanup")
	}
	if _, err := m.RequestBenchmark(context.Background(), "late"); !errors.Is(err, context.Canceled) {
		t.Fatal("work accepted after shutdown")
	}
}

func TestHealthStateMachineComparesWANBeforeFailover(t *testing.T) {
	for _, tc := range []struct {
		name                                             string
		wanDown, oneTargetDown, activeDown, fallbackDown bool
		wantClass, wantNode                              string
		wantDirect                                       bool
	}{
		{name: "all monitoring targets down", wanDown: true, activeDown: true, fallbackDown: true, wantClass: "monitoring_inconclusive", wantNode: "active"},
		{name: "one target down VPN intact", oneTargetDown: true, wantClass: "healthy", wantNode: "active"},
		{name: "active VPN down", activeDown: true, wantClass: "vpn_path_failed", wantNode: "fallback"},
		{name: "all VPN down WAN alive", activeDown: true, fallbackDown: true, wantClass: "vpn_path_failed", wantDirect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, tun, p := fixture(t)
			m.cfg.Failover.FailureThreshold = 1
			handler := func(path string, vpnDown bool) int {
				if tc.wanDown || vpnDown || (tc.oneTargetDown && path == "/b") {
					return http.StatusServiceUnavailable
				}
				return http.StatusNoContent
			}
			direct := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(handler(r.URL.Path, false)) }))
			listener, err := net.Listen("tcp", "0.0.0.0:0")
			if err != nil {
				t.Fatal(err)
			}
			direct.Listener.Close()
			direct.Listener = listener
			direct.Start()
			defer direct.Close()
			_, port, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			m.cfg.Targets = []config.Target{{ID: "a", Role: "health", URL: "http://127.0.0.1:" + port + "/a", Policy: "exact:204"}, {ID: "b", Role: "health", URL: "http://localhost:" + port + "/b", Policy: "exact:204"}, {ID: "c", Role: "health", URL: "http://localhost.:" + port + "/c", Policy: "exact:204"}}
			active := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(handler(r.URL.Path, tc.activeDown)) }))
			defer active.Close()
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(handler(r.URL.Path, tc.fallbackDown)) }))
			defer fallback.Close()
			tun.health, _ = url.Parse(active.URL)
			fallbackURL, _ := url.Parse(fallback.URL)
			tun.proxies = map[int]*url.URL{0: tun.health, 1: fallbackURL}
			m.checkHealth(context.Background())
			state := m.State()
			if state.LastHealthClass != tc.wantClass || state.ActiveNodeID != tc.wantNode || state.DirectMode != tc.wantDirect {
				t.Fatalf("health result class=%s active=%s direct=%v", state.LastHealthClass, state.ActiveNodeID, state.DirectMode)
			}
			if p.bypass != tc.wantDirect {
				t.Fatal("platform bypass differs from committed direct state")
			}
		})
	}
}
