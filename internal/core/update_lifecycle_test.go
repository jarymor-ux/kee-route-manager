package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
)

func TestPrepareUpdateJoinsOperationsAndPermanentlyClosesAdmission(t *testing.T) {
	m, _, _ := fixture(t)
	m.platform = &observerAdapter{fakeAdapter: fakeAdapter{supportsBypass: true}}
	started := make(chan struct{})
	m.bench = fakeBenchmark{start: started, release: make(chan struct{})}
	m.Start(context.Background())
	t.Cleanup(m.Stop)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("benchmark did not start")
	}
	if err := m.PrepareUpdate(); err != nil {
		t.Fatal(err)
	}
	if op := m.ops.Current(); op == nil || op.Status == "running" {
		t.Fatal("prepare left running operation")
	}
	if err := m.RunBenchmark(context.Background(), "manual", "test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("accepted benchmark: %v", err)
	}
	called := false
	if err := m.RunAction(context.Background(), "test", "test", func(context.Context) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatal("accepted action after prepare")
	}
}

func TestPrepareUpdateRefusesUnfinishedRouteJournal(t *testing.T) {
	m, _, _ := fixture(t)
	if err := m.store.Update(func(s *model.State) error { s.AutomaticRoutingPaused = true; return nil }); err != nil {
		t.Fatal(err)
	}
	state := m.State()
	if err := m.store.PrepareTransaction(store.Transaction{ID: "unfinished", Kind: "select", Before: state, Desired: state, Nodes: m.store.Nodes()}); err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareUpdate(); err == nil {
		t.Fatal("accepted interrupted routing operation")
	}
	if tx, err := m.store.PendingTransaction(); err != nil || tx == nil {
		t.Fatal("prepare discarded pending journal")
	}
}
