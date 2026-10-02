package subscription

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestNormalCacheShortcutRespectsTTL(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "expired"}[expired], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subscription")
			if err := os.WriteFile(path, []byte(reality), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default().Subscriptions
			cfg.CacheTTL = config.Dur(30 * time.Second)
			cfg.RefreshInterval = config.Dur(time.Minute)
			cfg.Sources = []config.Source{{ID: "provider", URL: "file://" + path, Enabled: true}}
			f := New(cfg, nil, t.TempDir())
			now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
			f.now = func() time.Time { return now }
			initial := f.FetchAll(context.Background(), nil, true)
			if len(initial.Nodes) != 1 {
				t.Fatal("initial subscription missing")
			}
			if err := os.WriteFile(path, []byte(ws), 0600); err != nil {
				t.Fatal(err)
			}
			now = now.Add(15 * time.Second)
			if expired {
				now = now.Add(15 * time.Second)
			}
			result := f.FetchAll(context.Background(), initial.States, false)
			state := result.States["provider"]
			expectedID := initial.Nodes[0].ID
			if expired {
				node, err := ParseVLESS(ws, "provider")
				if err != nil {
					t.Fatal(err)
				}
				expectedID = node.ID
			}
			if len(result.Errors) != 0 || len(result.Nodes) != 1 || result.Nodes[0].ID != expectedID || state.UsingCache == expired || state.Status != "healthy" || !now.Before(state.CacheExpiresAt) {
				t.Fatalf("expired=%t nodes=%+v state=%+v errors=%v", expired, result.Nodes, state, result.Errors)
			}
		})
	}
}

func TestExpiredCacheRemainsEmergencyFallbackOnly(t *testing.T) {
	for _, availableProvider := range []bool{false, true} {
		t.Run(map[bool]string{false: "all-providers-unavailable", true: "another-provider-available"}[availableProvider], func(t *testing.T) {
			dir := t.TempDir()
			unavailable := filepath.Join(dir, "unavailable")
			available := filepath.Join(dir, "available")
			if err := os.WriteFile(unavailable, []byte(reality), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default().Subscriptions
			cfg.CacheTTL = config.Dur(30 * time.Second)
			cfg.RefreshInterval = config.Dur(time.Minute)
			cfg.Sources = []config.Source{{ID: "old", URL: "file://" + unavailable, Enabled: true}}
			if availableProvider {
				if err := os.WriteFile(available, []byte(ws), 0600); err != nil {
					t.Fatal(err)
				}
				cfg.Sources = append(cfg.Sources, config.Source{ID: "fresh", URL: "file://" + available, Enabled: true})
			}
			f := New(cfg, nil, t.TempDir())
			now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
			f.now = func() time.Time { return now }
			initial := f.FetchAll(context.Background(), nil, true)
			if err := os.Remove(unavailable); err != nil {
				t.Fatal(err)
			}
			now = now.Add(45 * time.Second)
			result := f.FetchAll(context.Background(), initial.States, false)
			state := result.States["old"]
			if state.Status != "unavailable" || !state.UsingCache || len(result.Errors) != 1 || len(result.Nodes) != 1 {
				t.Fatalf("expired emergency cache classification: state=%+v nodes=%+v errors=%v", state, result.Nodes, result.Errors)
			}
			expectedSource := "old"
			if availableProvider {
				expectedSource = "fresh"
			}
			if len(result.Nodes[0].Sources) != 1 || result.Nodes[0].Sources[0] != expectedSource {
				t.Fatalf("expected %s candidate, got %+v", expectedSource, result.Nodes)
			}
		})
	}
}
