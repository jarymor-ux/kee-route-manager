package subscription

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

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestPausedUpdatesUseAllSavedSourcesPastTTL(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		payload := reality
		if r.URL.Path == "/b" {
			payload = ws
		}
		_, _ = fmt.Fprint(w, payload)
	}))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.CacheTTL = config.Dur(time.Minute)
	cfg.Sources = []config.Source{{ID: "a", URL: server.URL, Enabled: true}, {ID: "b", URL: server.URL + "/b", Enabled: true}}
	dir := t.TempDir()
	f := New(cfg, nil, dir)
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return now }
	seed := f.FetchAll(context.Background(), nil, true)
	if len(seed.Nodes) != 2 {
		t.Fatal("seed failed")
	}
	// Both a fresh and an expired source remain normal cache-only candidates.
	now = now.Add(2 * time.Minute)
	_, fresh, err := f.load("b")
	if err != nil {
		t.Fatal(err)
	}
	fresh.FetchedAt = now
	if err := f.save(fresh); err != nil {
		t.Fatal(err)
	}
	cfg.CacheEnabled = false
	f = New(cfg, nil, dir)
	f.now = func() time.Time { return now }
	f.SetAutoUpdatePaused(true)
	for _, force := range []bool{false, true} {
		result := f.FetchAll(context.Background(), nil, force)
		if len(result.Nodes) != 2 || len(result.Errors) != 0 || requests.Load() != 2 {
			t.Fatalf("paused fetch downloaded or lost saved nodes: requests=%d nodes=%d errors=%v", requests.Load(), len(result.Nodes), result.Errors)
		}
		for _, st := range result.States {
			if st.Status != "cached" || !st.UsingCache || st.CacheFetchedAt.IsZero() || !st.LastAttemptAt.IsZero() {
				t.Fatalf("cache classified as a live provider: %+v", st)
			}
		}
	}
	f.SetAutoUpdatePaused(false)
	f.FetchAll(context.Background(), nil, true)
	if requests.Load() != 4 {
		t.Fatal("resuming did not restore ordinary fetching")
	}
}

func TestPausedUpdatesMissingOrChangedCacheNeverDownloads(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); _, _ = fmt.Fprint(w, reality) }))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	f := New(cfg, nil, t.TempDir())
	f.SetAutoUpdatePaused(true)
	result := f.FetchAll(context.Background(), nil, true)
	if requests.Load() != 0 || len(result.Nodes) != 0 || len(result.Errors) != 1 || result.States["provider"].Status != "unavailable" {
		t.Fatal("missing cache did not remain offline")
	}
	f.SetAutoUpdatePaused(false)
	f.FetchAll(context.Background(), nil, true)
	f.cfg.Sources[0].Headers = map[string]string{"X-HWID": "changed"}
	f.SetAutoUpdatePaused(true)
	result = f.FetchAll(context.Background(), nil, true)
	if requests.Load() != 1 || len(result.Nodes) != 0 || len(result.Errors) != 1 {
		t.Fatal("changed source reused another identity or downloaded")
	}
}

func TestExplicitRefreshSavesWithoutEnablingAutomaticUpdates(t *testing.T) {
	var requests atomic.Int64
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, ws)
	}))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.CacheEnabled = false
	cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	dir := t.TempDir()
	f := New(cfg, []config.Duration{config.Dur(time.Hour)}, dir)
	f.SetAutoUpdatePaused(true)
	old := map[string]model.SourceState{"provider": {NextRetryAt: time.Now().Add(time.Hour)}}
	result := f.RefreshAll(context.Background(), old)
	if requests.Load() != 1 || len(result.Nodes) != 1 || len(result.Errors) != 0 {
		t.Fatal("explicit refresh honored automatic backoff or failed to fetch")
	}
	cachePath := filepath.Join(dir, "subscriptions", "provider.json")
	before, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	unavailable.Store(true)
	failed := f.RefreshAll(context.Background(), result.States)
	after, err := os.ReadFile(cachePath)
	if err != nil || string(before) != string(after) || len(failed.Errors) != 1 {
		t.Fatal("failed refresh replaced saved copy or hid failure")
	}
	cached := f.FetchAll(context.Background(), failed.States, true)
	if requests.Load() != 2 || len(cached.Nodes) != 1 || cached.States["provider"].Status != "cached" {
		t.Fatal("explicit refresh unpaused updates or lost saved nodes")
	}
}

func TestPausingCancelsInFlightAutomaticDownload(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	dir := t.TempDir()
	f := New(cfg, nil, dir)
	done := make(chan Result, 1)
	go func() { done <- f.FetchAll(context.Background(), nil, true) }()
	<-started
	f.SetAutoUpdatePaused(true)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("automatic request was not canceled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fetch did not finish after pause")
	}
	if _, err := os.Stat(filepath.Join(dir, "subscriptions", "provider.json")); !os.IsNotExist(err) {
		t.Fatal("obsolete automatic download wrote cache")
	}
}
