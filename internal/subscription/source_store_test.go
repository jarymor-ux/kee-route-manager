package subscription

import (
	"os"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestSourceStoreRoundTrip(t *testing.T) {
	store := NewSourceStore(t.TempDir())
	if sources, ok, err := store.Load(); err != nil || ok || sources != nil {
		t.Fatalf("initial load = %#v, %t, %v", sources, ok, err)
	}

	want := []config.Source{{
		ID:      "primary",
		Name:    "Primary",
		URL:     "https://example.invalid/subscription",
		Enabled: true,
		Headers: map[string]string{"Authorization": "Bearer test"},
	}}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}

	got, ok, err := store.Load()
	if err != nil || !ok {
		t.Fatalf("load = %#v, %t, %v", got, ok, err)
	}
	if len(got) != 1 || got[0].ID != want[0].ID || got[0].URL != want[0].URL || got[0].Headers["Authorization"] != want[0].Headers["Authorization"] {
		t.Fatalf("sources = %#v", got)
	}
	got[0].Headers["Authorization"] = "changed"
	reloaded, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded[0].Headers["Authorization"] != "Bearer test" {
		t.Fatal("returned headers alias stored data")
	}
}

func TestSourceStoreRejectsInsecureFile(t *testing.T) {
	store := NewSourceStore(t.TempDir())
	if err := os.WriteFile(store.path, []byte(\`{"schema":1,"sources":[]}\`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); err == nil {
		t.Fatal("insecure source store was accepted")
	}
}
