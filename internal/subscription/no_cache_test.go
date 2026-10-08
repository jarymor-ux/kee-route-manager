package subscription

import (
	"bytes"
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

func TestDisabledCacheAlwaysDownloadsWithoutReadingOrWritingCache(t *testing.T) {
	for _, ttl := range []time.Duration{time.Nanosecond, 24 * time.Hour} {
		t.Run(ttl.String(), func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := requests.Add(1)
				_, _ = fmt.Fprintf(w, "vless://00000000-0000-4000-8000-%012d@fixture.invalid:443?type=ws&security=tls&path=%%2F#rotating", n)
			}))
			defer server.Close()
			cfg := config.Default().Subscriptions
			cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
			cfg.CacheTTL, cfg.RefreshInterval = config.Dur(ttl), config.Dur(24*time.Hour)
			dir := t.TempDir()
			seed := New(cfg, nil, dir).FetchAll(context.Background(), nil, true)
			if len(seed.Nodes) != 1 {
				t.Fatal("seed cache was not created")
			}
			path := filepath.Join(dir, "subscriptions", "provider.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg.CacheEnabled = false
			f := New(cfg, nil, dir)
			previous := seed
			for _, force := range []bool{false, false, true} {
				result := f.FetchAll(context.Background(), previous.States, force)
				st := result.States["provider"]
				if len(result.Nodes) != 1 || len(result.Errors) != 0 || result.Nodes[0].ID == previous.Nodes[0].ID || st.UsingCache || !st.CacheExpiresAt.IsZero() {
					t.Fatal("disabled cache did not return a fresh provider response")
				}
				previous = result
			}
			if requests.Load() != 4 {
				t.Fatalf("downloads=%d, want seed plus three fresh responses", requests.Load())
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("disabled cache rewrote existing poisoned cache")
			}
		})
	}
}

func TestDisabledCacheFailureAlwaysRetriesWithoutFallback(t *testing.T) {
	var unavailable atomic.Bool
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(reality))
	}))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	dir := t.TempDir()
	seed := New(cfg, nil, dir).FetchAll(context.Background(), nil, true)
	if len(seed.Nodes) != 1 {
		t.Fatal("seed cache was not created")
	}
	cfg.CacheEnabled = false
	f := New(cfg, []config.Duration{config.Dur(time.Minute)}, dir)
	prev := seed.States
	// Try failure with a fresh historical cache, an expired historical cache,
	// and then a future retry deadline. No case may return cached nodes.
	unavailable.Store(true)
	for _, stage := range []string{"fresh-cache", "expired-cache", "backoff"} {
		if stage == "expired-cache" {
			f.cfg.CacheTTL = config.Dur(time.Nanosecond)
			st := prev["provider"]
			st.NextRetryAt = time.Time{}
			prev = map[string]model.SourceState{"provider": st}
		}
		result := f.FetchAll(context.Background(), prev, false)
		st := result.States["provider"]
		if len(result.Nodes) != 0 || len(result.Errors) != 1 || st.Status != "unavailable" || st.UsingCache || !st.CacheExpiresAt.IsZero() || st.NodeCount != 0 {
			t.Fatalf("%s used historical cache or misclassified failure", stage)
		}
		prev = result.States
	}
	if requests.Load() != 4 {
		t.Fatalf("failure backoff skipped a required fresh download: requests=%d, want 4", requests.Load())
	}
}

func TestDisabledCacheDoesNotCreateCacheDirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(reality)) }))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.CacheEnabled = false
	cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	dir := t.TempDir()
	result := New(cfg, nil, dir).FetchAll(context.Background(), nil, false)
	if len(result.Nodes) != 1 || len(result.Errors) != 0 {
		t.Fatal("fresh download failed")
	}
	if _, err := os.Stat(filepath.Join(dir, "subscriptions")); !os.IsNotExist(err) {
		t.Fatal("disabled cache created cache storage")
	}
}
