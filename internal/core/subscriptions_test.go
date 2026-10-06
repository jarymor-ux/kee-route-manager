package core

import (
	"context"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
)

type fakeManagedFetcher struct {
	sources []config.Source
}

func (f *fakeManagedFetcher) FetchAll(context.Context, map[string]model.SourceState, bool) subscription.Result {
	return subscription.Result{}
}
func (f *fakeManagedFetcher) Sources() []config.Source {
	return cloneSubscriptionSources(f.sources)
}
func (f *fakeManagedFetcher) ReplaceSources(sources []config.Source) error {
	f.sources = cloneSubscriptionSources(sources)
	return nil
}

func subscriptionManagerConfig() config.Config {
	c := config.Default()
	c.Platform.Kind = "linux-systemd"
	c.ApplyPlatformDefaults()
	c.Subscriptions.Sources = []config.Source{{
		ID: "primary", Name: "Primary", URL: "https://primary.example.invalid/sub", Enabled: true,
	}}
	c.Targets = []config.Target{
		{ID: "score", Name: "Score", URL: "https://score.example.invalid/ping", Role: "score", Weight: 1, Policy: "2xx3xx", MaxResponseBytes: 1024},
		{ID: "health", Name: "Health", URL: "https://health.example.invalid/ping", Role: "health", Weight: 1, Policy: "2xx3xx", MaxResponseBytes: 1024},
	}
	return c
}

func TestSubscriptionCRUDUsesValidatedLiveSources(t *testing.T) {
	cfg := subscriptionManagerConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("fixture config invalid: %v", err)
	}
	fetcher := &fakeManagedFetcher{sources: cloneSubscriptionSources(cfg.Subscriptions.Sources)}
	m := New(cfg, "test", nil, nil, nil, nil, fetcher, nil)

	err := m.SaveSubscription(context.Background(), config.Source{
		ID: "backup", Name: "Backup", URL: "https://backup.example.invalid/sub", Enabled: true,
		Headers: map[string]string{"Authorization": "Bearer test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.SubscriptionSources(); len(got) != 2 || got[1].ID != "backup" || got[1].Headers["Authorization"] != "Bearer test" {
		t.Fatalf("sources after add = %#v", got)
	}

	if err := m.SaveSubscription(context.Background(), config.Source{ID: "backup", Name: "Disabled", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if got := m.SubscriptionSources(); len(got) != 2 || got[1].Enabled || got[1].Name != "Disabled" {
		t.Fatalf("sources after edit = %#v", got)
	}

	if err := m.DeleteSubscription(context.Background(), "backup"); err != nil {
		t.Fatal(err)
	}
	if got := m.SubscriptionSources(); len(got) != 1 || got[0].ID != "primary" {
		t.Fatalf("sources after delete = %#v", got)
	}
	if err := m.DeleteSubscription(context.Background(), "primary"); err == nil {
		t.Fatal("deleting the last subscription was accepted")
	}
}

func TestSubscriptionCRUDRejectsInvalidInput(t *testing.T) {
	cfg := subscriptionManagerConfig()
	fetcher := &fakeManagedFetcher{sources: cloneSubscriptionSources(cfg.Subscriptions.Sources)}
	m := New(cfg, "test", nil, nil, nil, nil, fetcher, nil)

	if err := m.SaveSubscription(context.Background(), config.Source{
		ID: "invalid", Name: "Invalid", URL: "https://user:secret@example.invalid/sub", Enabled: true,
	}); err == nil {
		t.Fatal("URL credentials were accepted")
	}
	if got := m.SubscriptionSources(); len(got) != 1 {
		t.Fatalf("invalid mutation changed sources: %#v", got)
	}
}
