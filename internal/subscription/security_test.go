package subscription

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFairMergePreservesSmallProviderAndAllSources(t *testing.T) {
	sources := []string{"large", "small"}
	groups := map[string][]model.Node{"large": {{ID: "a", Label: "a", Sources: []string{"large"}}, {ID: "b", Label: "b", Sources: []string{"large"}}, {ID: "c", Label: "c", Sources: []string{"large"}}}, "small": {{ID: "z", Label: "z", Sources: []string{"small"}}}}
	xs := fairMerge(groups, sources, 2, 3)
	if len(xs) != 2 || xs[0].ID != "a" || xs[1].ID != "z" {
		t.Fatalf("small source starved: %+v", xs)
	}
	groups["small"] = []model.Node{{ID: "a", Label: "a", Sources: []string{"small"}}, {ID: "z", Label: "z", Sources: []string{"small"}}}
	xs = fairMerge(groups, sources, 2, 3)
	if len(xs) != 2 || len(xs[0].Sources) != 2 {
		t.Fatalf("dedup lost attribution or quota: %+v", xs)
	}
}
func TestFetcherRedactsSubscriptionErrors(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:0/fail?token=super-secret", http.StatusFound)
	}))
	defer s.Close()
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "provider", URL: s.URL + "/?token=another-secret", Enabled: true}}
	r := New(cfg, nil, t.TempDir()).FetchAll(context.Background(), nil, true)
	if len(r.Errors) == 0 {
		t.Fatal("expected network failure")
	}
	if strings.Contains(r.Errors[0].Error(), "super-secret") || strings.Contains(r.States["provider"].LastError, "super-secret") {
		t.Fatal("subscription credentials exposed")
	}
}
