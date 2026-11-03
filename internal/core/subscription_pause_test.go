package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
)

func TestSubscriptionPauseSurvivesRestartTestsAndRestore(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, "vless://00000000-0000-4000-8000-000000000001@fixture.invalid:443?type=ws&security=tls&path=%2F#saved")
	}))
	defer server.Close()
	m, tun, p := fixture(t)
	dir := t.TempDir()
	st, err := store.New(dir, dir, m.State())
	if err != nil {
		t.Fatal(err)
	}
	if err = st.ReplaceNodes(m.store.Nodes()); err != nil {
		t.Fatal(err)
	}
	if err = st.Flush(); err != nil {
		t.Fatal(err)
	}
	m.store = st
	m.cfg.Subscriptions.CacheEnabled = false
	m.cfg.Subscriptions.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	f := subscription.New(m.cfg.Subscriptions, nil, dir)
	m.fetcher = f
	m.ctx = nil // queue admission is tested separately; explicit calls below own work.
	m.bench = freshSubscriptionBenchmark{}
	if err = m.RefreshSubscriptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = m.SetSubscriptionAutoUpdate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !m.State().SubscriptionUpdatesPaused {
		t.Fatal("pause was not durable")
	}
	reopened, err := store.New(dir, dir, model.NewState("test", m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	f = subscription.New(m.cfg.Subscriptions, nil, dir)
	m = New(m.cfg, "test", reopened, m.ops, p, tun, f, freshSubscriptionBenchmark{})
	for _, mode := range []string{"startup", "scheduled", "manual", "emergency", "recovery", "subscription-change"} {
		if err = m.RunBenchmark(context.Background(), mode, "test"); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	if requests.Load() != 1 || !m.State().SubscriptionUpdatesPaused {
		t.Fatal("restart or benchmark bypassed pause")
	}
	if err = m.RestoreOriginalXray(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !m.State().SubscriptionUpdatesPaused {
		t.Fatal("restore lost subscription pause")
	}
	if err = m.RefreshSubscriptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || !m.State().SubscriptionUpdatesPaused || !m.State().AutomaticRoutingPaused {
		t.Fatal("explicit refresh changed a pause")
	}
	if err = m.SetSubscriptionAutoUpdate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if m.State().SubscriptionUpdatesPaused {
		t.Fatal("resume failed")
	}
}

type sourcePollDuringBenchmark struct {
	manager *Manager
	runs    int
}

func (b *sourcePollDuringBenchmark) Run(ctx context.Context, nodes []model.Node, progress bench.Progress) ([]model.Measurement, error) {
	b.runs++
	if b.runs == 1 {
		b.manager.refreshSources()
	}
	return (freshSubscriptionBenchmark{}).Run(ctx, nodes, progress)
}

func TestExplicitSubscriptionRefreshSourcePollDoesNotDownloadTwice(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, "vless://00000000-0000-4000-8000-000000000001@fixture.invalid:443?type=ws&security=tls#saved")
	}))
	defer server.Close()
	m, _, _ := fixture(t)
	m.cfg.Subscriptions.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	m.fetcher = subscription.New(m.cfg.Subscriptions, nil, t.TempDir())
	runner := &sourcePollDuringBenchmark{manager: m}
	m.bench = runner
	m.benchmarkQueued = false
	if err := m.RefreshSubscriptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.wg.Wait()
	if requests.Load() != 1 || runner.runs != 2 {
		t.Fatalf("source poll caused a second download or lost queued work: requests=%d runs=%d", requests.Load(), runner.runs)
	}
}

func TestSubscriptionPauseRefusesUnfinishedRoutingJournal(t *testing.T) {
	m, _, _ := fixture(t)
	m.fetcher = subscription.New(m.cfg.Subscriptions, nil, t.TempDir())
	state := m.State()
	if err := m.store.PrepareTransaction(store.Transaction{ID: "pending", Kind: "select", Before: state, Desired: state, Nodes: m.store.Nodes()}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetSubscriptionAutoUpdate(context.Background(), false); err == nil {
		t.Fatal("pause accepted before pending replay")
	}
	if m.State().SubscriptionUpdatesPaused || !m.SubscriptionUpdateStatus(context.Background()).AutoUpdateEnabled {
		t.Fatal("refused pause changed live policy")
	}
}

func TestSubscriptionPauseFailedPersistenceDoesNotChangeFetcher(t *testing.T) {
	m, _, _ := fixture(t)
	dir := t.TempDir()
	st, err := store.New(dir, dir, model.NewState("test", m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	m.store = st
	m.fetcher = subscription.New(m.cfg.Subscriptions, nil, t.TempDir())
	// Force atomic state replacement to fail without changing filesystem rights.
	if err = os.Mkdir(filepath.Join(dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = m.SetSubscriptionAutoUpdate(context.Background(), false); err == nil {
		t.Fatal("persistence failure hidden")
	}
	if m.State().SubscriptionUpdatesPaused {
		t.Fatal("failed write changed live pause")
	}
	if !m.SubscriptionUpdateStatus(context.Background()).AutoUpdateEnabled {
		t.Fatal("failed write disabled downloads")
	}
}

func TestSubscriptionPauseInterruptsRunningBenchmarkDownload(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	m, tun, _ := fixture(t)
	m.cfg.Subscriptions.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	m.fetcher = subscription.New(m.cfg.Subscriptions, nil, t.TempDir())
	before := m.State()
	done := make(chan error, 1)
	go func() { done <- m.RunBenchmark(context.Background(), "manual", "test") }()
	<-started
	if err := m.SetSubscriptionAutoUpdate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("obsolete benchmark succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not cancel download")
	}
	if m.State().ActiveNodeID != before.ActiveNodeID || tun.directCalls != 0 {
		t.Fatal("pausing changed active route")
	}
}

func TestExplicitSubscriptionRefreshFailurePreservesRouteAndGoodCache(t *testing.T) {
	var failed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failed.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, "vless://00000000-0000-4000-8000-000000000001@fixture.invalid:443?type=ws&security=tls#saved")
	}))
	defer server.Close()
	m, tun, _ := fixture(t)
	m.cfg.Subscriptions.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	m.fetcher = subscription.New(m.cfg.Subscriptions, nil, t.TempDir())
	m.ctx = nil
	if err := m.RefreshSubscriptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.SetSubscriptionAutoUpdate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	before := m.State()
	failed.Store(true)
	if err := m.RefreshSubscriptions(context.Background()); err == nil {
		t.Fatal("provider failure hidden")
	}
	status := m.SubscriptionUpdateStatus(context.Background())
	if status.AutoUpdateEnabled || status.States["provider"].CacheFetchedAt.IsZero() || m.State().ActiveNodeID != before.ActiveNodeID || tun.directCalls != 0 {
		t.Fatal("failure lost cache, pause or working route")
	}
}

func TestExplicitSubscriptionRefreshQueueDoesNotDownloadTwice(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, "vless://00000000-0000-4000-8000-000000000001@fixture.invalid:443?type=ws&security=tls#saved")
	}))
	defer server.Close()
	m, _, _ := fixture(t)
	m.cfg.Subscriptions.CacheEnabled = false
	m.cfg.Subscriptions.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	m.fetcher = subscription.New(m.cfg.Subscriptions, nil, t.TempDir())
	m.bench = freshSubscriptionBenchmark{}
	m.ctx = nil
	if err := m.RefreshSubscriptions(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.ctx = context.Background()
	m.benchmarkQueued = false
	// Admission stays closed while independent health work joins the queue.
	m.admissionMu.Lock()
	m.queueBenchmark("subscription-refresh")
	m.queueBenchmark("emergency")
	m.queueBenchmark("recovery")
	m.admissionMu.Unlock()
	m.wg.Wait()
	if requests.Load() != 1 || m.State().LastBenchmark.Mode != "subscription-refresh" {
		t.Fatalf("coalesced test downloaded again: requests=%d mode=%s", requests.Load(), m.State().LastBenchmark.Mode)
	}
}
