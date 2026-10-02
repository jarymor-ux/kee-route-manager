package core

import (
	"context"
	"os"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
)

func TestRestorePauseSurvivesPrimaryStateCorruption(t *testing.T) {
	m, xm, next, node, _ := transactionFileFixture(t)
	ctx := context.Background()
	var err error
	m.ops, err = operation.New(m.cfg.Paths.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.commitRoute(ctx, next, []model.Node{node}, "pool"); err != nil {
		t.Fatal(err)
	}
	if err = m.RestoreOriginalXray(ctx); err != nil {
		t.Fatal(err)
	}
	journal, err := os.ReadFile(m.store.Path("transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(m.store.Path("state.json"), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	m.store, err = store.New(m.cfg.Paths.StateDir, m.cfg.Paths.CacheDir, model.NewState("review", m.cfg.Xray.SlotTagPrefix, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.reconcileStartup(ctx); err != nil {
		t.Fatalf("completed restore was not recovered: %v", err)
	}
	m.fetcher = fakeFetcher{[]model.Node{node}}
	m.bench = fakeBenchmark{results: []model.Measurement{{NodeID: node.ID, Healthy: true, Score: 1}}}
	for _, mode := range []string{"startup", "scheduled", "source-refresh"} {
		if err = m.RunBenchmark(ctx, mode, "scheduler"); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := xm.ActualState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Configured || m.State().XrayConfigured || !m.State().AutomaticRoutingPaused {
		t.Fatal("automatic work reinstalled routing after restored state recovery")
	}
	after, err := os.ReadFile(m.store.Path("transaction.json"))
	if err != nil || string(after) != string(journal) {
		t.Fatal("state recovery changed the completed journal")
	}
	if err = m.RunBenchmark(ctx, "manual", "api"); err != nil {
		t.Fatal(err)
	}
	if m.State().AutomaticRoutingPaused || !m.State().XrayConfigured {
		t.Fatal("recovered restore could not be resumed explicitly")
	}
	m.store, err = store.New(m.cfg.Paths.StateDir, m.cfg.Paths.CacheDir, model.NewState("review", m.cfg.Xray.SlotTagPrefix, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.reconcileStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if m.State().AutomaticRoutingPaused || !m.State().XrayConfigured {
		t.Fatal("a subsequent manual resume was overwritten by restore recovery")
	}
}
