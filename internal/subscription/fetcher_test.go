package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestFetcherCacheBackoffAndRecovery(t *testing.T) {
	available := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(reality))
	}))
	defer server.Close()

	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "provider", Name: "Provider", URL: server.URL, Enabled: true}}
	cfg.RefreshInterval = config.Dur(time.Minute)
	cfg.CacheTTL = config.Dur(24 * time.Hour)
	fetcher := New(cfg, []config.Duration{config.Dur(15 * time.Second), config.Dur(time.Minute)}, t.TempDir())
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	fetcher.now = func() time.Time { return now }

	first := fetcher.FetchAll(context.Background(), nil, true)
	if len(first.Nodes) != 1 || first.States["provider"].Status != "healthy" {
		t.Fatalf("first fetch: %#v %#v", first.Nodes, first.States)
	}

	available = false
	now = now.Add(2 * time.Minute)
	second := fetcher.FetchAll(context.Background(), first.States, true)
	state := second.States["provider"]
	if len(second.Nodes) != 1 || state.Status != "degraded" || !state.UsingCache {
		t.Fatalf("cache fallback: %#v %#v", second.Nodes, state)
	}
	if got := state.NextRetryAt.Sub(now); got != 15*time.Second {
		t.Fatalf("first backoff = %v", got)
	}

	available = true
	now = state.NextRetryAt.Add(time.Second)
	state.Status = "unavailable"
	third := fetcher.FetchAll(context.Background(), map[string]model.SourceState{"provider": state}, true)
	if third.States["provider"].Status != "recovering" || third.States["provider"].UsingCache {
		t.Fatalf("recovery: %#v", third.States["provider"])
	}
}

func TestAllProvidersOutageHourUsesEmergencyCacheAndBackoff(t *testing.T) {
	var available atomic.Bool
	available.Store(true)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !available.Load() {
			http.Error(w, "provider outage", http.StatusServiceUnavailable)
			return
		}
		payload := reality
		if r.URL.Path == "/b" {
			payload = ws
		}
		w.Write([]byte(payload))
	}))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "a", URL: server.URL + "/a", Enabled: true}, {ID: "b", URL: server.URL + "/b", Enabled: true}}
	cfg.CacheTTL = config.Dur(30 * time.Minute)
	f := New(cfg, []config.Duration{config.Dur(time.Minute), config.Dur(5 * time.Minute)}, t.TempDir())
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return now }
	initial := f.FetchAll(context.Background(), nil, true)
	if len(initial.Nodes) != 2 {
		t.Fatalf("initial nodes=%d", len(initial.Nodes))
	}
	available.Store(false)
	now = now.Add(time.Hour)
	outage := f.FetchAll(context.Background(), initial.States, true)
	if len(outage.Nodes) != 2 {
		t.Fatalf("expired emergency caches lost: %d", len(outage.Nodes))
	}
	for id, st := range outage.States {
		if st.Status != "unavailable" || !st.UsingCache || st.NextRetryAt.Sub(now) != time.Minute {
			t.Fatalf("%s outage state %+v", id, st)
		}
	}
	before := requests.Load()
	now = now.Add(30 * time.Second)
	backoff := f.FetchAll(context.Background(), outage.States, true)
	if requests.Load() != before || len(backoff.Nodes) != 2 {
		t.Fatal("backoff made requests or discarded emergency cache")
	}
	available.Store(true)
	now = now.Add(time.Minute)
	recovered := f.FetchAll(context.Background(), backoff.States, true)
	for id, st := range recovered.States {
		if st.Status != "recovering" || st.UsingCache || st.ConsecutiveFailures != 0 {
			t.Fatalf("%s recovery state %+v", id, st)
		}
	}
}
