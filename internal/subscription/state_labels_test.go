package subscription

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestMissingCacheClearsPreviouslyCachedState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider")
	if err := os.WriteFile(path, []byte(reality), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "provider", URL: "file://" + path, Enabled: true}}
	f := New(cfg, nil, t.TempDir())
	now := time.Now()
	f.now = func() time.Time { return now }
	initial := f.FetchAll(context.Background(), nil, true)
	cached := f.FetchAll(context.Background(), initial.States, false)
	if !cached.States["provider"].UsingCache || len(cached.Nodes) != 1 {
		t.Fatalf("cache not primed: %+v", cached.States)
	}
	for _, p := range []string{path, filepath.Join(f.dir, "provider.json")} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(cfg.RefreshInterval.Duration)
	failed := f.FetchAll(context.Background(), cached.States, false)
	state := failed.States["provider"]
	if len(failed.Errors) != 1 || len(failed.Nodes) != 0 || state.Status != "unavailable" || state.NodeCount != 0 || state.UsingCache || !state.CacheExpiresAt.IsZero() {
		t.Fatalf("missing cache reported as available: %+v", state)
	}
}

func TestVLESSLabelsDecodedOnceAndTruncatedAtRuneBoundary(t *testing.T) {
	base := strings.Split(reality, "#")[0] + "#"
	for _, tc := range []struct{ raw, want string }{
		{"percent%2520literal", "percent%20literal"},
		{"discount%25", "discount%"},
		{strings.Repeat("я", 127) + "界", strings.Repeat("я", 127)},
	} {
		node, err := ParseVLESS(base+tc.raw, "provider")
		if err != nil {
			t.Fatal(err)
		}
		if node.Label != tc.want || !utf8.ValidString(node.Label) || len(node.Label) > 256 {
			t.Errorf("label %q became %q, want %q", tc.raw, node.Label, tc.want)
		}
	}
}
