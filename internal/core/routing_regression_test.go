package core

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegressionSingleSlotReplacement(t *testing.T) {
	for _, size := range []int{1, 2} {
		t.Run(string(rune('0'+size)), func(t *testing.T) {
			m, _, _ := fixture(t)
			c, err := config.Load("../../configs/linux-systemd.yaml")
			if err != nil {
				t.Fatal(err)
			}
			c.Pool.Size = size
			if err = c.Validate(); err != nil {
				t.Fatalf("legal configuration: %v", err)
			}
			m.cfg = c
			oldState, oldNodes := m.State(), m.store.Nodes()
			dir := t.TempDir()
			m.store, err = store.New(dir, dir, model.NewState("rc2", c.Xray.SlotTagPrefix, size))
			if err != nil {
				t.Fatal(err)
			}
			if err = m.store.ReplaceNodes(oldNodes); err != nil {
				t.Fatal(err)
			}
			if err = m.store.Update(func(s *model.State) error { *s = oldState; s.Pool = s.Pool[:size]; return nil }); err != nil {
				t.Fatal(err)
			}
			m.bench = fakeBenchmark{results: []model.Measurement{{NodeID: "active", Healthy: false, Score: 999999}, {NodeID: "fallback", Healthy: true, Score: 1}}}
			if err = m.RunBenchmark(context.Background(), "manual", "review"); err != nil {
				t.Fatal(err)
			}
			s := m.State()
			t.Logf("size=%d active=%s pool=%+v healthy replacement measurement=%+v", size, s.ActiveNodeID, s.Pool, s.Measurements["fallback"])
			if size == 1 && s.ActiveNodeID != "fallback" {
				t.Fatal("healthy replacement discarded")
			}
			if size == 2 && s.ActiveNodeID != "fallback" {
				t.Fatal("negative control failed")
			}
		})
	}
}

func TestRegressionRecoveryWithAlternatingWinners(t *testing.T) {
	for _, alternating := range []bool{true, false} {
		t.Run(map[bool]string{true: "alternating", false: "stable"}[alternating], func(t *testing.T) {
			m, tun, _ := fixture(t)
			m.cfg.Health.RecoveryThreshold = 2
			if err := m.store.Update(func(s *model.State) error { *s = directState(*s, "review direct"); return nil }); err != nil {
				t.Fatal(err)
			}
			var round atomic.Int32
			server := func(slot int32) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					fastest := int32(0)
					if alternating {
						fastest = round.Load() % 2
					}
					if slot != fastest {
						select {
						case <-r.Context().Done():
							return
						case <-time.After(150 * time.Millisecond):
						}
					}
					w.WriteHeader(http.StatusNoContent)
				}))
			}
			a, b := server(0), server(1)
			defer a.Close()
			defer b.Close()
			au, _ := url.Parse(a.URL)
			bu, _ := url.Parse(b.URL)
			tun.proxies = map[int]*url.URL{0: au, 1: bu}
			targets := []config.Target{{ID: "a", URL: "http://one.example/", Policy: "exact:204"}, {ID: "b", URL: "http://two.example/", Policy: "exact:204"}}
			for i := 0; i < 2; i++ {
				round.Store(int32(i))
				m.checkRecovery(context.Background(), m.State(), targets)
				t.Logf("round=%d direct=%v candidate=%s counter=%d", i, m.State().DirectMode, m.recoveryNode, m.recoveryCount)
			}
			if alternating && m.State().DirectMode {
				t.Fatal("healthy VPN did not recover")
			}
			if !alternating && m.State().DirectMode {
				t.Fatal("negative control failed")
			}
		})
	}
}

func TestRegressionEmergencyBudgetWAN(t *testing.T) {
	for _, budget := range []time.Duration{200 * time.Millisecond, 600 * time.Millisecond} {
		t.Run(budget.String(), func(t *testing.T) {
			m, tun, _ := fixture(t)
			c, err := config.Load("../../configs/linux-systemd.yaml")
			if err != nil {
				t.Fatal(err)
			}
			c.Pool.Size = 2
			c.Failover.FailureThreshold = 1
			c.Failover.ProbeTimeout = config.Dur(200 * time.Millisecond)
			c.Failover.OverallDeadline = config.Dur(budget)
			var wanRequests atomic.Int32
			wan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { wanRequests.Add(1); w.WriteHeader(http.StatusNoContent) }))
			defer wan.Close()
			active := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
			defer active.Close()
			fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
			defer fallback.Close()
			targets := []config.Target{{ID: "a", Role: "health", URL: wan.URL, Weight: 1, Policy: "exact:204", MaxResponseBytes: 64 << 10}, {ID: "b", Role: "health", URL: strings.Replace(wan.URL, "127.0.0.1", "localhost", 1), Weight: 1, Policy: "exact:204", MaxResponseBytes: 64 << 10}}
			c.Targets = append(c.TargetsByRole("score"), targets...)
			if err = c.Validate(); err != nil {
				t.Fatal(err)
			}
			m.cfg = c
			tun.health, _ = url.Parse(active.URL)
			fallbackURL, _ := url.Parse(fallback.URL)
			tun.proxies = map[int]*url.URL{0: tun.health, 1: fallbackURL}
			m.checkHealth(context.Background())
			s := m.State()
			t.Logf("budget=%s classification=%s direct=%v active=%s WAN HTTP requests=%d", budget, s.LastHealthClass, s.DirectMode, s.ActiveNodeID, wanRequests.Load())
			if budget == 200*time.Millisecond && !s.DirectMode {
				t.Fatal("WAN confirmation starved")
			}
			if budget == 600*time.Millisecond && !s.DirectMode {
				t.Fatal("negative control failed")
			}
		})
	}
}

func TestRegressionSingleSlotPreservesUnauthorizedRoute(t *testing.T) {
	for _, mode := range []string{"auto", "manual-direct"} {
		t.Run(mode, func(t *testing.T) {
			m, tun, _ := fixture(t)
			m.cfg.Pool.Size = 1
			old, nodes := m.State(), m.store.Nodes()
			d := t.TempDir()
			var err error
			m.store, err = store.New(d, d, model.NewState("rc2", m.cfg.Xray.SlotTagPrefix, 1))
			if err != nil {
				t.Fatal(err)
			}
			if err = m.store.ReplaceNodes(nodes); err != nil {
				t.Fatal(err)
			}
			old.Pool = old.Pool[:1]
			old.LastSwitchAt = time.Now()
			old.ActiveSince = time.Now()
			if mode == "manual-direct" {
				old = directState(old, "control")
			}
			if err = m.store.Update(func(s *model.State) error { *s = old; return nil }); err != nil {
				t.Fatal(err)
			}
			m.bench = fakeBenchmark{results: []model.Measurement{{NodeID: "active", Healthy: true, Score: 100}, {NodeID: "fallback", Healthy: true, Score: 1}}}
			benchmarkMode := "auto"
			if mode == "manual-direct" {
				benchmarkMode = "manual"
			}
			if err = m.RunBenchmark(context.Background(), benchmarkMode, "test"); err != nil {
				t.Fatal(err)
			}
			if mode == "auto" && m.State().ActiveNodeID != "active" {
				t.Fatal("cooldown route discarded")
			}
			if mode == "manual-direct" && !m.State().DirectMode {
				t.Fatal("benchmark bypassed recovery threshold")
			}
			if tun.directCalls != 0 {
				t.Fatal("benchmark enabled direct")
			}
		})
	}
}

func TestRegressionRecoveryCandidateFailureResetsThreshold(t *testing.T) {
	m, tun, _ := fixture(t)
	m.cfg.Health.RecoveryThreshold = 2
	if err := m.store.Update(func(s *model.State) error { *s = directState(*s, "test"); return nil }); err != nil {
		t.Fatal(err)
	}
	var healthy atomic.Bool
	healthy.Store(true)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(204)
		} else {
			w.WriteHeader(503)
		}
	}))
	defer proxy.Close()
	endpoint, _ := url.Parse(proxy.URL)
	tun.proxies = map[int]*url.URL{0: endpoint}
	targets := []config.Target{{ID: "a", URL: "http://one.example/", Policy: "exact:204"}, {ID: "b", URL: "http://two.example/", Policy: "exact:204"}}
	m.checkRecovery(context.Background(), m.State(), targets)
	if m.recoveryCount != 1 || !m.State().DirectMode {
		t.Fatal("recovered below threshold")
	}
	healthy.Store(false)
	m.checkRecovery(context.Background(), m.State(), targets)
	if m.recoveryCount != 0 || m.recoveryNode != "" || !m.State().DirectMode {
		t.Fatal("failed candidate retained success")
	}
	healthy.Store(true)
	m.checkRecovery(context.Background(), m.State(), targets)
	if !m.State().DirectMode {
		t.Fatal("nonconsecutive health authorized VPN")
	}
	m.checkRecovery(context.Background(), m.State(), targets)
	if m.State().DirectMode {
		t.Fatal("stable health failed recovery")
	}
}

func TestRegressionEmergencyRequiresWANQuorum(t *testing.T) {
	m, tun, _ := fixture(t)
	m.cfg.Failover.OverallDeadline = config.Dur(200 * time.Millisecond)
	m.cfg.Failover.ProbeTimeout = config.Dur(200 * time.Millisecond)
	wan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer wan.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer fallback.Close()
	endpoint, _ := url.Parse(fallback.URL)
	tun.proxies = map[int]*url.URL{1: endpoint}
	targets := []config.Target{{ID: "a", URL: wan.URL, Policy: "exact:204"}, {ID: "b", URL: strings.Replace(wan.URL, "127.0.0.1", "localhost", 1), Policy: "exact:204"}}
	m.emergencyFailover(context.Background(), m.State(), targets)
	if m.State().DirectMode || m.State().ActiveNodeID != "active" || tun.directCalls != 0 {
		t.Fatal("uncertain WAN changed route")
	}
}
