package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
)

func automaticRoutingPaused(t *testing.T, state model.State) bool {
	t.Helper()
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	paused, _ := fields["automatic_routing_paused"].(bool)
	return paused
}

func healthyReviewBenchmark() fakeBenchmark {
	return fakeBenchmark{results: []model.Measurement{{NodeID: "active", Healthy: true, Score: 1}, {NodeID: "fallback", Healthy: true, Score: 2}}}
}

func TestReviewRestoreBlocksAutomaticRouting(t *testing.T) {
	for _, trigger := range []string{"scheduled", "startup", "source-refresh", "source-loop"} {
		t.Run(trigger, func(t *testing.T) {
			m, tun, _ := fixture(t)
			m.bench = healthyReviewBenchmark()
			m.benchmarkQueued = false
			if err := m.RestoreOriginalXray(context.Background()); err != nil {
				t.Fatal(err)
			}
			if trigger == "source-loop" {
				m.refreshSources()
				m.wg.Wait()
			} else if err := m.RunBenchmark(context.Background(), trigger, "scheduler"); err != nil {
				t.Fatal(err)
			}
			if m.State().XrayConfigured || tun.configured || tun.bootstrapCalls != 0 || !automaticRoutingPaused(t, m.State()) {
				t.Fatalf("automatic %s undid restore: configured=%t bootstraps=%d paused=%t", trigger, m.State().XrayConfigured, tun.bootstrapCalls, automaticRoutingPaused(t, m.State()))
			}
		})
	}
}

func TestReviewRestorePauseSurvivesReloadAndStartup(t *testing.T) {
	m, tun, p := fixture(t)
	if err := m.RestoreOriginalXray(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(m.store.Path(""), m.store.CachePath(""), model.NewState("rc2", m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(m.cfg, "rc2", st, m.ops, p, tun, m.fetcher, healthyReviewBenchmark())
	restarted.ctx = context.Background()
	if err = restarted.reconcileStartup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = restarted.RunBenchmark(context.Background(), "startup", "scheduler"); err != nil {
		t.Fatal(err)
	}
	if !automaticRoutingPaused(t, restarted.State()) || restarted.State().XrayConfigured || tun.bootstrapCalls != 0 {
		t.Fatal("restart lost the durable restore pause")
	}
}

func TestReviewManualBenchmarkResumesOnlyWithHealthyPool(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-healthy-pool", true: "healthy-pool"}[healthy], func(t *testing.T) {
			m, tun, _ := fixture(t)
			if err := m.RestoreOriginalXray(context.Background()); err != nil {
				t.Fatal(err)
			}
			m.bench = fakeBenchmark{}
			if healthy {
				m.bench = healthyReviewBenchmark()
			}
			if _, err := m.RequestBenchmark(context.Background(), "api"); err != nil {
				t.Fatal(err)
			}
			m.wg.Wait()
			if automaticRoutingPaused(t, m.State()) == healthy || m.State().XrayConfigured != healthy || tun.configured != healthy {
				t.Fatalf("manual benchmark incorrectly changed restore pause: healthy=%t configured=%t paused=%t", healthy, m.State().XrayConfigured, automaticRoutingPaused(t, m.State()))
			}
		})
	}
}

func TestReviewFailedManualBenchmarkKeepsRestorePause(t *testing.T) {
	m, tun, _ := fixture(t)
	if err := m.RestoreOriginalXray(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.fetcher = fakeFetcher{}
	if err := m.RunBenchmark(context.Background(), "manual", "api"); err == nil {
		t.Fatal("empty subscriptions should fail")
	}
	if !automaticRoutingPaused(t, m.State()) || m.State().XrayConfigured || tun.configured {
		t.Fatal("failed manual benchmark resumed automatic routing")
	}
}

func TestReviewPausedRoutingReconcilesPendingManualResume(t *testing.T) {
	m, tun, _ := fixture(t)
	desired, nodes := m.State(), m.store.Nodes()
	if err := m.RestoreOriginalXray(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A manual benchmark can leave its authorized intent pending before the
	// pause has been cleared in state. Health must finish that journal first.
	desired.AutomaticRoutingPaused = false
	tx := store.Transaction{ID: "manual-resume", Kind: "pool", Before: m.State(), Desired: desired, Nodes: nodes}
	if err := m.store.PrepareTransaction(tx); err != nil {
		t.Fatal(err)
	}
	m.checkHealth(context.Background())
	pending, err := m.store.PendingTransaction()
	if err != nil || pending != nil {
		t.Fatalf("pause blocked journal reconciliation: %v %+v", err, pending)
	}
	if !m.State().XrayConfigured || m.State().AutomaticRoutingPaused || tun.bootstrapCalls != 1 {
		t.Fatal("authorized manual resume was not reconciled")
	}
}

func TestReviewXrayOutageBreaksConsecutiveRecovery(t *testing.T) {
	m, tun, _ := fixture(t)
	m.cfg.Health.RecoveryThreshold = 2
	m.cfg.Targets = []config.Target{{ID: "a", Role: "health", URL: "http://one.example/", Policy: "exact:204"}, {ID: "b", Role: "health", URL: "http://two.example/", Policy: "exact:204"}}
	if err := m.store.Update(func(s *model.State) error { *s = directState(*s, "test"); return nil }); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer proxy.Close()
	endpoint, _ := url.Parse(proxy.URL)
	tun.proxies = map[int]*url.URL{0: endpoint}
	m.checkHealth(context.Background())
	if m.recoveryCount != 1 || !m.State().DirectMode {
		t.Fatal("first success should not recover")
	}
	tun.running = false
	m.checkHealth(context.Background())
	if m.recoveryCount != 0 || m.recoveryNode != "" {
		t.Error("Xray outage retained an earlier recovery success")
	}
	if m.State().XrayLastError != "tunnel unavailable" {
		t.Errorf("bypass erased outage diagnosis: %q", m.State().XrayLastError)
	}
	tun.running = true
	m.checkHealth(context.Background())
	if !m.State().DirectMode {
		t.Fatal("success/outage/success incorrectly met consecutive recovery threshold")
	}
	m.checkHealth(context.Background())
	if m.State().DirectMode {
		t.Fatal("two successes after outage should recover")
	}
}
