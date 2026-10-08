package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
)

type heldHotPoolBenchmark struct {
	started, release chan struct{}
	runs             atomic.Int64
}

func (b *heldHotPoolBenchmark) Run(ctx context.Context, _ []model.Node, _ bench.Progress) ([]model.Measurement, error) {
	if b.runs.Add(1) == 1 {
		close(b.started)
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	now := time.Now().UTC()
	return []model.Measurement{
		{NodeID: "active", Healthy: true, Score: 100, CheckedAt: now},
		{NodeID: "fallback", Healthy: true, Score: 200, CheckedAt: now},
	}, nil
}

func TestHealthyProbeDoesNotQueueRefreshDuringActiveBenchmark(t *testing.T) {
	m, tun, _ := fixture(t)
	m.benchmarkQueued = false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	tun.health, _ = url.Parse(server.URL)
	m.cfg.Targets = []config.Target{
		{ID: "one", Role: "health", URL: server.URL, Policy: "exact:204"},
		{ID: "two", Role: "health", URL: strings.Replace(server.URL, "127.0.0.1", "localhost", 1), Policy: "exact:204"},
	}
	if err := m.store.Update(func(s *model.State) error {
		s.Pool[1].LastVerifiedAt = time.Now().Add(-time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := &heldHotPoolBenchmark{started: make(chan struct{}), release: make(chan struct{})}
	m.bench = runner
	var release sync.Once
	defer func() {
		release.Do(func() { close(runner.release) })
		m.wg.Wait()
	}()
	finished := make(chan error, 1)
	go func() { finished <- m.RunBenchmark(context.Background(), "scheduled", "scheduler") }()
	<-runner.started
	for i := 0; i < 3; i++ {
		m.checkHealth(context.Background())
	}
	m.mu.RLock()
	queued := m.benchmarkQueueVersion
	m.mu.RUnlock()
	if queued != 0 {
		t.Fatal("stale reserves queued a follow-up despite an active benchmark")
	}
	release.Do(func() { close(runner.release) })
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	m.checkHealth(context.Background())
	m.wg.Wait()
	if runner.runs.Load() != 1 {
		t.Fatal("healthy ticks triggered an immediate duplicate benchmark")
	}
}

func TestHealthyNoCacheCadenceDoesNotQueueUnavailableProvider(t *testing.T) {
	m, tun, _ := fixture(t)
	m.benchmarkQueued = false
	var downloads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/subscription" {
			downloads.Add(1)
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	tun.health, _ = url.Parse(server.URL)
	m.cfg.Targets = []config.Target{
		{ID: "one", Role: "health", URL: server.URL, Policy: "exact:204"},
		{ID: "two", Role: "health", URL: strings.Replace(server.URL, "127.0.0.1", "localhost", 1), Policy: "exact:204"},
	}
	m.cfg.Subscriptions.CacheEnabled = false
	m.cfg.Subscriptions.Sources = []config.Source{{ID: "provider", URL: server.URL + "/subscription", Enabled: true}}
	m.fetcher = subscription.New(m.cfg.Subscriptions, nil, t.TempDir())
	m.bench = freshSubscriptionBenchmark{}
	if err := m.store.Update(func(s *model.State) error {
		s.Pool[1].LastVerifiedAt = time.Now().Add(-time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// First let a scheduled download fail, then run healthy-route ticks with a
	// stale reserve. Those ticks must not turn a provider outage into rapid tests.
	if err := m.RunBenchmark(context.Background(), "scheduled", "scheduler"); err == nil {
		t.Fatal("fixture provider did not fail")
	}
	for i := 0; i < 3; i++ {
		m.checkHealth(context.Background())
		m.wg.Wait()
	}
	if m.State().LastHealthClass != "healthy" || downloads.Load() != 1 {
		t.Fatal("healthy-route freshness probes bypassed the no-cache benchmark cadence")
	}
	if m.State().ActiveNodeID != "active" || m.State().DirectMode {
		t.Fatal("provider outage changed the healthy active route")
	}
}

func TestHotPoolRefreshPreservesWithdrawnActiveHealth(t *testing.T) {
	m, tun, _ := fixture(t)
	now := time.Now().UTC()
	oldScore := now.Add(-time.Hour)
	if err := m.store.Update(func(s *model.State) error {
		s.Pool[0].LastVerifiedAt = now
		s.Pool[1].LastVerifiedAt = oldScore
		s.Measurements["active"] = model.Measurement{NodeID: "active", Healthy: true, Score: 100, CheckedAt: oldScore}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fallback, _ := m.store.Node("fallback")
	// A working active route remains available even after its provider withdraws
	// it. The refresh can measure only the current subscription's reserve node.
	m.fetcher = fakeFetcher{nodes: []model.Node{fallback}}
	m.bench = fakeBenchmark{results: []model.Measurement{{NodeID: "fallback", Healthy: true, Score: 200, CheckedAt: now}}}
	if err := m.RunBenchmark(context.Background(), "hot-pool-refresh", "health"); err != nil {
		t.Fatal(err)
	}
	s := m.State()
	if s.ActiveNodeID != "active" || s.DirectMode || tun.directCalls != 0 {
		t.Fatal("refresh changed the healthy retained active route")
	}
	for _, tag := range tun.selected {
		if tag != m.cfg.Xray.SlotTagPrefix+"0" {
			t.Fatal("refresh selected a different route")
		}
	}
	if !s.Pool[0].Healthy || !s.Pool[0].LastVerifiedAt.Equal(now) {
		t.Fatalf("older score measurement erased live route verification: healthy=%t verified=%s want=%s", s.Pool[0].Healthy, s.Pool[0].LastVerifiedAt, now)
	}
	if !s.Measurements["active"].CheckedAt.Equal(oldScore) {
		t.Fatal("health observation fabricated a new score measurement")
	}
	if !s.Pool[1].LastVerifiedAt.Equal(now) {
		t.Fatal("refresh failed to update reserve verification")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	tun.health, _ = url.Parse(server.URL)
	m.cfg.Targets = []config.Target{
		{ID: "one", Role: "health", URL: server.URL, Policy: "exact:204"},
		{ID: "two", Role: "health", URL: strings.Replace(server.URL, "127.0.0.1", "localhost", 1), Policy: "exact:204"},
	}
	m.benchmarkQueued = false
	for i := 0; i < 3; i++ {
		m.checkHealth(context.Background())
		m.wg.Wait()
	}
	after := m.State()
	if after.LastHealthClass != "healthy" || after.LastBenchmark.OperationID != s.LastBenchmark.OperationID {
		t.Fatal("withdrawn active caused repeated refreshes across healthy ticks")
	}
	if !after.Measurements["active"].CheckedAt.Equal(oldScore) {
		t.Fatal("healthy ticks changed historical benchmark timestamp")
	}
}

func TestHealthyProbeRefreshesActiveBeforeCheckingPoolFreshness(t *testing.T) {
	for _, tc := range []struct {
		name         string
		activeZero   bool
		reserveStale bool
		reserveEmpty bool
		wantRefresh  bool
	}{
		{name: "stale-active-fresh-reserve"},
		{name: "unverified-active-fresh-reserve", activeZero: true},
		{name: "stale-reserve-still-refreshes", reserveStale: true, wantRefresh: true},
		{name: "empty-reserve-needs-no-refresh", reserveStale: true, reserveEmpty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, tun, _ := fixture(t)
			m.benchmarkQueued = false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			defer server.Close()
			tun.health, _ = url.Parse(server.URL)
			m.cfg.Targets = []config.Target{
				{ID: "one", Role: "health", URL: server.URL, Policy: "exact:204"},
				{ID: "two", Role: "health", URL: strings.Replace(server.URL, "127.0.0.1", "localhost", 1), Policy: "exact:204"},
			}
			now := time.Now().UTC()
			if err := m.store.Update(func(s *model.State) error {
				s.Pool[0].LastVerifiedAt = now.Add(-time.Hour)
				if tc.activeZero {
					s.Pool[0].LastVerifiedAt = time.Time{}
				}
				s.Pool[1].LastVerifiedAt = now
				if tc.reserveStale {
					s.Pool[1].LastVerifiedAt = now.Add(-time.Hour)
				}
				if tc.reserveEmpty {
					s.Pool[1].NodeID = ""
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			m.bench = fakeBenchmark{results: []model.Measurement{
				{NodeID: "active", Healthy: true, Score: 100, CheckedAt: now},
				{NodeID: "fallback", Healthy: true, Score: 200, CheckedAt: now},
			}}
			m.checkHealth(context.Background())
			m.wg.Wait()
			s := m.State()
			if s.LastHealthClass != "healthy" || s.Pool[0].LastVerifiedAt.Before(now) {
				t.Fatalf("fixture failed to verify active route: class=%s verified=%s", s.LastHealthClass, s.Pool[0].LastVerifiedAt)
			}
			if refreshed := s.LastBenchmark.OperationID != ""; refreshed != tc.wantRefresh {
				t.Fatalf("healthy tick queued refresh=%t, want %t (mode %s)", refreshed, tc.wantRefresh, s.LastBenchmark.Mode)
			}
			firstOperation := s.LastBenchmark.OperationID
			for i := 0; i < 2; i++ {
				m.checkHealth(context.Background())
				m.wg.Wait()
			}
			if m.State().LastBenchmark.OperationID != firstOperation {
				t.Fatal("fresh hot pool retriggered benchmark on following healthy ticks")
			}
		})
	}
}

func TestSlotRefreshUsesNewestHealthEvidenceForSameNode(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name        string
		nodeID      string
		measurement model.Measurement
		wantHealthy bool
		wantTime    time.Time
	}{
		{name: "older-failed-score", nodeID: "same", measurement: model.Measurement{Healthy: false, CheckedAt: now.Add(-time.Minute)}, wantHealthy: true, wantTime: now},
		{name: "missing-score", nodeID: "same", wantHealthy: true, wantTime: now},
		{name: "newer-failed-score", nodeID: "same", measurement: model.Measurement{Healthy: false, CheckedAt: now.Add(time.Minute)}, wantTime: now.Add(time.Minute)},
		{name: "replacement-never-inherits", nodeID: "replacement", measurement: model.Measurement{Healthy: false, CheckedAt: now.Add(-time.Minute)}, wantTime: now.Add(-time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := []model.Slot{{Index: 0, NodeID: "same", Healthy: true, LastVerifiedAt: now}}
			selected := []model.Node{{ID: tc.nodeID, Label: "current label"}}
			tc.measurement.Score = 123
			slots := assignSlots(old, selected, map[string]model.Measurement{tc.nodeID: tc.measurement}, "slot-", 1)
			if slots[0].Healthy != tc.wantHealthy || !slots[0].LastVerifiedAt.Equal(tc.wantTime) {
				t.Fatalf("incorrect health evidence: %+v", slots[0])
			}
			if slots[0].Score != 123 || slots[0].Label != "current label" {
				t.Fatal("retaining live evidence lost current score or node metadata")
			}
		})
	}
}

func TestAutomaticRefreshUsesLiveActiveHealthWithoutInventingScore(t *testing.T) {
	for _, tc := range []struct {
		name             string
		known            bool
		freshMeasurement bool
		scoreHealthy     bool
		cooldownElapsed  bool
		wantNode         string
	}{
		{name: "old-failed-score-cooldown", known: true, wantNode: "active"},
		{name: "old-failed-score-not-improvement", known: true, cooldownElapsed: true, wantNode: "active"},
		{name: "unknown-score-live-health", cooldownElapsed: true, wantNode: "active"},
		{name: "new-failed-score-still-replaces", known: true, freshMeasurement: true, wantNode: "fallback"},
		{name: "new-healthy-score-still-compares", known: true, freshMeasurement: true, scoreHealthy: true, cooldownElapsed: true, wantNode: "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := fixture(t)
			now := time.Now().UTC()
			old := model.Measurement{NodeID: "active", Healthy: tc.scoreHealthy, Score: 999999, CheckedAt: now.Add(-time.Hour)}
			if tc.scoreHealthy {
				old.Score = 1000
			}
			if err := m.store.Update(func(s *model.State) error {
				s.Pool[0].Healthy = true
				s.Pool[0].LastVerifiedAt = now
				if tc.known {
					s.Measurements["active"] = old
				}
				if tc.cooldownElapsed {
					s.LastSwitchAt, s.ActiveSince = now.Add(-time.Hour), now.Add(-time.Hour)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			fallback, _ := m.store.Node("fallback")
			nodes := []model.Node{fallback}
			results := []model.Measurement{{NodeID: "fallback", Healthy: true, Score: 200, CheckedAt: now.Add(time.Second)}}
			if tc.freshMeasurement {
				active, _ := m.store.Node("active")
				nodes = append(nodes, active)
				measured := old
				measured.CheckedAt = now.Add(time.Second)
				results = append(results, measured)
			}
			m.fetcher = fakeFetcher{nodes: nodes}
			m.bench = fakeBenchmark{results: results}
			if err := m.RunBenchmark(context.Background(), "hot-pool-refresh", "health"); err != nil {
				t.Fatal(err)
			}
			s := m.State()
			if s.ActiveNodeID != tc.wantNode || s.DirectMode {
				t.Fatalf("fresh healthy route replaced using obsolete/unknown score: active=%s want=%s reason=%s", s.ActiveNodeID, tc.wantNode, s.LastSwitchReason)
			}
			if !tc.freshMeasurement {
				measurement, known := s.Measurements["active"]
				if known != tc.known || (known && measurement != old) {
					t.Fatal("live health changed historical score evidence")
				}
			}
		})
	}
}
