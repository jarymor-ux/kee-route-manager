package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestReplaceSourcesDoesNotWaitForDownloadAndOldCacheCannotCrossSource(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Write([]byte("vless://12345678-1234-1234-1234-123456789abc@old.example:443?security=tls&type=ws&path=%2F#old"))
	}))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "primary", URL: server.URL, Enabled: true}}
	f := New(cfg, nil, t.TempDir())
	f.UseSourceStore(NewSourceStore(t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() { done <- f.FetchAll(ctx, nil, true) }()
	<-started
	changed := cfg.Sources[0]
	changed.URL = "https://new.example/sub"
	saved := make(chan error, 1)
	go func() { saved <- f.ReplaceSources([]config.Source{changed}) }()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		cancel()
		close(release)
		t.Fatal("saving sources waited for a network download")
	}
	close(release)
	result := <-done
	if len(result.Nodes) != 0 || len(result.Errors) == 0 {
		t.Fatalf("obsolete download was not discarded: %+v", result)
	}
	if _, _, err := f.loadSource(changed); err == nil {
		t.Fatal("old in-flight download poisoned new source cache")
	}
	if _, _, err := f.loadSource(cfg.Sources[0]); err == nil {
		t.Fatal("obsolete download was cached")
	}
}

func TestOldDownloadCannotOverwriteNewSourceCache(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			close(started)
			<-release
		}
		w.Write([]byte("vless://12345678-1234-1234-1234-123456789abc@node.example:443?security=tls&type=ws&path=%2F#" + r.URL.Path[1:]))
	}))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "primary", URL: server.URL + "/old", Enabled: true}}
	f := New(cfg, nil, t.TempDir())
	f.UseSourceStore(NewSourceStore(t.TempDir()))
	oldDone := make(chan Result, 1)
	go func() { oldDone <- f.FetchAll(context.Background(), nil, true) }()
	<-started
	changed := cfg.Sources[0]
	changed.URL = server.URL + "/new"
	if err := f.ReplaceSources([]config.Source{changed}); err != nil {
		close(release)
		t.Fatal(err)
	}
	fresh := f.FetchAll(context.Background(), nil, true)
	close(release)
	<-oldDone
	if len(fresh.Nodes) != 1 || fresh.Nodes[0].Label != "new" {
		t.Fatalf("new source fetch failed: %+v", fresh)
	}
	nodes, _, err := f.loadSource(changed)
	if err != nil || len(nodes) != 1 || nodes[0].Label != "new" {
		t.Fatalf("obsolete request replaced newer cache: %+v %v", nodes, err)
	}
}
