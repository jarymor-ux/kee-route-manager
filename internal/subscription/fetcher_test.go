package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
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
