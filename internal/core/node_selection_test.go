package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
)

func TestNodeAutoSelectRejectsActiveAndUnknownNodes(t *testing.T) {
	m, tun, _ := fixture(t)
	before := m.State()
	for _, id := range []string{"active", "missing", ""} {
		if err := m.SetNodeAutoSelect(context.Background(), id, false); err == nil {
			t.Fatalf("disabling %q was accepted", id)
		}
	}
	if after := m.State(); !reflect.DeepEqual(before, after) || len(tun.selected) != 0 {
		t.Fatal("rejected exclusion changed state or route")
	}
	if err := m.SetNodeAutoSelect(context.Background(), "active", true); err != nil {
		t.Fatalf("enabling active node should be an idempotent no-op: %v", err)
	}
}

func TestNodeAutoSelectPersistsAcrossRefreshRestoreAndRestart(t *testing.T) {
	m, _, _ := fixture(t)
	if err := m.SetNodeAutoSelect(context.Background(), "fallback", false); err != nil {
		t.Fatal(err)
	}
	// A provider rename and node-cache replacement must not erase user preferences.
	nodes := m.store.Nodes()
	for i := range nodes {
		if nodes[i].ID == "fallback" {
			nodes[i].Label = "Renamed by provider"
			nodes[i].Sources = []string{"one", "two"}
		}
	}
	if err := m.store.ReplaceNodes(nodes); err != nil {
		t.Fatal(err)
	}
	for _, view := range m.Nodes() {
		if view.AutoSelectEnabled != (view.ID != "fallback") {
			t.Fatalf("incorrect eligibility for %q", view.ID)
		}
	}
	if err := m.RestoreOriginalXray(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(m.store.Path(""), m.store.CachePath(""), model.NewState("test", m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	if !st.State().ExcludedNodeIDs["fallback"] {
		t.Fatal("restore/restart discarded exclusion")
	}
	// Even when a provider temporarily removes the node, its exclusion survives.
	m.store = st
	if err = m.SetNodeAutoSelect(context.Background(), "fallback", true); err != nil {
		t.Fatal(err)
	}
	if len(m.State().ExcludedNodeIDs) != 0 {
		t.Fatal("re-enabling an absent node did not clear its exclusion")
	}
}

type eligibilityBenchmark struct{ tested []string }

func (b *eligibilityBenchmark) Run(_ context.Context, nodes []model.Node, _ bench.Progress) ([]model.Measurement, error) {
	var results []model.Measurement
	for _, node := range nodes {
		b.tested = append(b.tested, node.ID)
		results = append(results, model.Measurement{NodeID: node.ID, Healthy: true, Score: 100, CheckedAt: time.Now()})
	}
	return results, nil
}

func TestNodeAutoSelectSkipsBenchmarkAndKeepsNodesVisible(t *testing.T) {
	for _, mode := range []string{"manual", "scheduled", "startup", "source-refresh", "emergency", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			m, _, _ := fixture(t)
			if err := m.SetNodeAutoSelect(context.Background(), "fallback", false); err != nil {
				t.Fatal(err)
			}
			runner := &eligibilityBenchmark{}
			m.bench = runner
			if err := m.RunBenchmark(context.Background(), mode, "test"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(runner.tested, []string{"active"}) {
				t.Fatalf("excluded node was tested: %v", runner.tested)
			}
			state := m.State()
			if state.ActiveNodeID != "active" || slotIndexForNode(state.Pool, "fallback") >= 0 || len(m.Nodes()) != 2 {
				t.Fatal("benchmark selected excluded node or removed its visible record")
			}
			if state.LastBenchmark.NodeCount != 1 || state.LastBenchmark.TestedCount != 1 {
				t.Fatal("benchmark summary includes excluded nodes in its test count")
			}
		})
	}
}

func TestNodeAutoSelectChangedDuringBenchmarkCannotWin(t *testing.T) {
	m, _, _ := fixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	m.bench = fakeBenchmark{start: started, release: release, results: []model.Measurement{
		{NodeID: "fallback", Healthy: true, Score: 1}, {NodeID: "active", Healthy: true, Score: 100},
	}}
	done := make(chan error, 1)
	go func() { done <- m.RunBenchmark(context.Background(), "manual", "test") }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("benchmark never started")
	}
	err := m.SetNodeAutoSelect(context.Background(), "fallback", false)
	close(release)
	if runErr := <-done; runErr != nil {
		t.Fatal(runErr)
	}
	if err != nil {
		t.Fatalf("exclusion was blocked by benchmark: %v", err)
	}
	if state := m.State(); state.ActiveNodeID != "active" || slotIndexForNode(state.Pool, "fallback") >= 0 || state.LastBenchmark.WinnerID == "fallback" || !state.ExcludedNodeIDs["fallback"] {
		t.Fatal("in-flight benchmark ignored newer exclusion")
	}
}

func TestNodeAutoSelectSerializesWithManualActivation(t *testing.T) {
	for i := 0; i < 12; i++ {
		m, _, _ := fixture(t)
		start := make(chan struct{})
		selected, excluded := make(chan error, 1), make(chan error, 1)
		go func() { <-start; selected <- m.SwitchSlot(context.Background(), 1) }()
		go func() { <-start; excluded <- m.SetNodeAutoSelect(context.Background(), "fallback", false) }()
		close(start)
		selectErr, excludeErr := <-selected, <-excluded
		if selectErr == nil && excludeErr == nil {
			t.Fatal("simultaneous activation and exclusion were both accepted")
		}
		state := m.State()
		if state.ExcludedNodeIDs[state.ActiveNodeID] {
			t.Fatal("concurrent request excluded an active node")
		}
	}
}

func TestNodeAutoSelectRejectsManualSlotUntilEnabled(t *testing.T) {
	m, _, _ := fixture(t)
	if err := m.SetNodeAutoSelect(context.Background(), "fallback", false); err != nil {
		t.Fatal(err)
	}
	if err := m.SwitchSlot(context.Background(), 1); err == nil || m.State().ActiveNodeID != "active" {
		t.Fatal("manual switch activated excluded node")
	}
	if err := m.SetNodeAutoSelect(context.Background(), "fallback", true); err != nil {
		t.Fatal(err)
	}
	if err := m.SwitchSlot(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := m.SetNodeAutoSelect(context.Background(), "fallback", false); err == nil {
		t.Fatal("newly active node could be disabled")
	}
}

func TestNodeAutoSelectSkipsFailoverAndRecoveryCandidates(t *testing.T) {
	m, tun, _ := fixture(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer proxy.Close()
	endpoint, _ := url.Parse(proxy.URL)
	tun.proxies = map[int]*url.URL{1: endpoint}
	targets := []config.Target{{ID: "a", URL: "http://one.example/", Policy: "exact:204"}, {ID: "b", URL: "http://two.example/", Policy: "exact:204"}}
	if _, ok := m.emergencyCandidate(context.Background(), m.State(), targets, false); !ok {
		t.Fatal("test setup did not produce a healthy fallback")
	}
	if err := m.SetNodeAutoSelect(context.Background(), "fallback", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.emergencyCandidate(context.Background(), m.State(), targets, false); ok {
		t.Fatal("excluded fallback remained an emergency candidate")
	}
	if err := m.SwitchDirect(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.cfg.Health.RecoveryThreshold = 1
	m.checkRecovery(context.Background(), m.State(), targets)
	if !m.State().DirectMode {
		t.Fatal("recovery selected excluded node")
	}
}

func TestNodeAutoSelectWithoutEligibleNodesPreservesRoute(t *testing.T) {
	m, tun, _ := fixture(t)
	if err := m.SwitchDirect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"active", "fallback"} {
		if err := m.SetNodeAutoSelect(context.Background(), id, false); err != nil {
			t.Fatal(err)
		}
	}
	runner := &eligibilityBenchmark{}
	m.bench = runner
	before := m.State()
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if state := m.State(); !state.DirectMode || !reflect.DeepEqual(before.Pool, state.Pool) || len(runner.tested) != 0 || len(m.Nodes()) != 2 || tun.directCalls != 0 {
		t.Fatal("empty eligibility set changed routing or hid excluded nodes")
	}
}

func TestNodeAutoSelectRejectsPendingRoutingJournal(t *testing.T) {
	m, tun, _ := fixture(t)
	tun.failSelect = true
	if err := m.SwitchSlot(context.Background(), 1); err == nil {
		t.Fatal("expected interrupted selection")
	}
	if err := m.SetNodeAutoSelect(context.Background(), "fallback", false); err == nil {
		t.Fatal("exclusion was accepted while stale routing intent could select the node")
	}
	if err := m.SetNodeAutoSelect(canceledContext(), "fallback", false); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled exclusion request was accepted")
	}
}
