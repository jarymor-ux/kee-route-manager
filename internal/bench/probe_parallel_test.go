package bench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestCheckMajorityRunsTargetsConcurrently(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	targets := []config.Target{
		{ID: "a", URL: server.URL, Policy: "2xx3xx"},
		{ID: "b", URL: server.URL, Policy: "2xx3xx"},
		{ID: "c", URL: server.URL, Policy: "2xx3xx"},
	}
	started := time.Now()
	passed, total, _ := NewProber(time.Second, 1024).CheckMajority(context.Background(), nil, targets)
	if passed != total || total != 3 {
		t.Fatalf("passed/total = %d/%d", passed, total)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("probes were not concurrent: %s", elapsed)
	}
}
