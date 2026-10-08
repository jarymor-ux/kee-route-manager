package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
)

type freshSubscriptionBenchmark struct{}

func (freshSubscriptionBenchmark) Run(_ context.Context, nodes []model.Node, _ bench.Progress) ([]model.Measurement, error) {
	results := make([]model.Measurement, len(nodes))
	for i, node := range nodes {
		results[i] = model.Measurement{NodeID: node.ID, Healthy: true, Score: 100, CheckedAt: time.Now().UTC()}
	}
	return results, nil
}

func TestDisabledCacheDownloadsOnlyAtBenchmarkCadence(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := requests.Add(1)
		_, _ = fmt.Fprintf(w, "vless://00000000-0000-4000-8000-%012d@fixture.invalid:443?type=ws&security=tls&path=%%2F#rotating", n)
	}))
	defer server.Close()
	m, _, _ := fixture(t)
	m.cfg.Subscriptions.CacheEnabled = false
	m.cfg.Subscriptions.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	m.fetcher = subscription.New(m.cfg.Subscriptions, nil, t.TempDir())
	m.bench = freshSubscriptionBenchmark{}
	m.benchmarkQueued = false
	for i := 0; i < 3; i++ {
		m.refreshSources()
		m.wg.Wait()
	}
	if requests.Load() != 0 || m.State().LastBenchmark.OperationID != "" {
		t.Fatal("source polling downloaded subscriptions before benchmark admission")
	}
	for i, mode := range []string{"scheduled", "manual", "emergency", "subscription-change", "recovery"} {
		if err := m.RunBenchmark(context.Background(), mode, "test"); err != nil {
			t.Fatal(err)
		}
		if requests.Load() != int64(i+1) {
			t.Fatalf("%s did not download exactly once", mode)
		}
		operationID := m.State().LastBenchmark.OperationID
		for j := 0; j < 3; j++ {
			m.refreshSources()
			m.wg.Wait()
		}
		if requests.Load() != int64(i+1) || m.State().LastBenchmark.OperationID != operationID {
			t.Fatal("source polling bypassed benchmark cadence")
		}
	}
}

func TestDisabledCacheFailedSubscriptionPreservesActiveRoute(t *testing.T) {
	var requests atomic.Int64
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, "vless://00000000-0000-4000-8000-000000000001@fixture.invalid:443?type=ws&security=tls&path=%2F#historical")
	}))
	defer server.Close()
	m, tun, _ := fixture(t)
	m.cfg.Subscriptions.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	cacheDir := t.TempDir()
	seed := subscription.New(m.cfg.Subscriptions, nil, cacheDir).FetchAll(context.Background(), nil, true)
	if len(seed.Nodes) != 1 {
		t.Fatal("could not seed historical subscription cache")
	}
	if err := m.store.Update(func(s *model.State) error { s.Sources = seed.States; return nil }); err != nil {
		t.Fatal(err)
	}
	unavailable.Store(true)
	m.cfg.Subscriptions.CacheEnabled = false
	m.fetcher = subscription.New(m.cfg.Subscriptions, []config.Duration{config.Dur(time.Hour)}, cacheDir)
	m.bench = freshSubscriptionBenchmark{}
	before := m.State()
	nodes := m.store.Nodes()
	for i, mode := range []string{"scheduled", "manual", "emergency"} {
		if err := m.RunBenchmark(context.Background(), mode, "test"); err == nil {
			t.Fatal("provider failure was reported as a successful benchmark")
		}
		after := m.State()
		if requests.Load() != int64(i+2) {
			t.Fatalf("%s skipped a fresh attempt because of prior failure backoff", mode)
		}
		if after.DirectMode || after.ActiveNodeID != before.ActiveNodeID || after.ActiveSlot != before.ActiveSlot || !reflect.DeepEqual(after.Pool, before.Pool) || !reflect.DeepEqual(m.store.Nodes(), nodes) || tun.directCalls != 0 || after.XrayGeneration != before.XrayGeneration {
			t.Fatal("failed fresh subscription download changed the working route or pool")
		}
		if after.Sources["provider"].UsingCache || after.Sources["provider"].Status != "unavailable" {
			t.Fatal("provider failure incorrectly advertised cached subscription availability")
		}
	}
}
