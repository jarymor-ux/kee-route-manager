package core

import (
	"context"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestRetainedActiveNodeDoesNotRetriggerUnchangedSource(t *testing.T) {
	m, _, _ := fixture(t)
	m.benchmarkQueued = false
	if err := m.store.Update(func(s *model.State) error {
		s.Measurements["active"] = model.Measurement{NodeID: "active", Healthy: true, Score: 100, CheckedAt: time.Now()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fallback, _ := m.store.Node("fallback")
	m.fetcher = fakeFetcher{nodes: []model.Node{fallback}}
	m.bench = fakeBenchmark{results: []model.Measurement{{NodeID: "fallback", Healthy: true, Score: 200, CheckedAt: time.Now()}}}
	if err := m.RunBenchmark(context.Background(), "source-refresh", "health"); err != nil {
		t.Fatal(err)
	}
	initial := m.State().XrayGeneration
	if m.State().ActiveNodeID != "active" || len(m.store.Nodes()) != 2 {
		t.Fatal("fixture did not retain disappeared active node")
	}
	for i := 0; i < 3; i++ {
		m.refreshSources()
		m.wg.Wait()
	}
	if m.State().XrayGeneration != initial {
		t.Fatalf("unchanged sources retrigger benchmark: %d -> %d", initial, m.State().XrayGeneration)
	}
	// A genuinely new node still triggers one benchmark and then settles.
	m.fetcher = fakeFetcher{nodes: []model.Node{fallback, {ID: "new", Label: "new"}}}
	m.refreshSources()
	m.wg.Wait()
	if m.State().XrayGeneration != initial+1 {
		t.Fatal("new subscription node did not trigger benchmark")
	}
	m.refreshSources()
	m.wg.Wait()
	if m.State().XrayGeneration != initial+1 {
		t.Fatal("new subscription set did not settle")
	}
	// Removing a node outside the retained pool must also trigger a refresh.
	m.fetcher = fakeFetcher{nodes: []model.Node{fallback}}
	m.refreshSources()
	m.wg.Wait()
	if m.State().XrayGeneration != initial+2 {
		t.Fatal("removed nonpool node did not trigger benchmark")
	}
}

func TestRemovedFallbackTriggersOnceEvenWhenPoolIsRetained(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		t.Run(map[bool]string{false: "retain-unhealthy-pool", true: "remove-fallback"}[healthy], func(t *testing.T) {
			m, _, _ := fixture(t)
			m.benchmarkQueued = false
			active, _ := m.store.Node("active")
			m.fetcher = fakeFetcher{nodes: []model.Node{active}}
			m.bench = fakeBenchmark{results: []model.Measurement{{NodeID: "active", Healthy: healthy, Score: 100}}}
			m.refreshSources()
			m.wg.Wait()
			if m.State().LastBenchmark.OperationID == "" {
				t.Fatal("fallback removal did not trigger benchmark")
			}
			operationID := m.State().LastBenchmark.OperationID
			if healthy && m.State().Pool[1].NodeID != "" {
				t.Fatal("withdrawn fallback remains in refreshed pool")
			}
			if !healthy && m.State().Pool[1].NodeID != "fallback" {
				t.Fatal("inconclusive benchmark discarded working pool")
			}
			for i := 0; i < 3; i++ {
				m.refreshSources()
				m.wg.Wait()
			}
			if m.State().LastBenchmark.OperationID != operationID {
				t.Fatal("unchanged subscription retriggered benchmark")
			}
		})
	}
}
