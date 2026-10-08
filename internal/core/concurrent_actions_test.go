package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

type policyAdapter struct {
	*fakeAdapter
	policies int
}

func (p *policyAdapter) SetClientPolicy(context.Context, string, string) error {
	p.policies++
	return nil
}
func (p *policyAdapter) Wake(context.Context, string) error { return nil }
func (p *policyAdapter) RestartXray(context.Context) error  { return nil }

func startHeldBenchmark(t *testing.T, m *Manager) chan struct{} {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.bench = fakeBenchmark{start: started, release: release, results: []model.Measurement{{NodeID: "active", Healthy: true, Score: 1, CheckedAt: time.Now()}, {NodeID: "fallback", Healthy: true, Score: 100, CheckedAt: time.Now()}}}
	if _, err := m.RequestBenchmark(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("benchmark did not start")
	}
	t.Cleanup(func() { m.Stop() })
	return release
}

func TestPolicyAndWakeRunDuringBenchmark(t *testing.T) {
	m, _, p := fixture(t)
	adapter := &policyAdapter{fakeAdapter: p}
	m.platform = adapter
	release := startHeldBenchmark(t, m)
	if err := m.SetPolicy(context.Background(), "02:00:00:00:00:01", "xkeen"); err != nil {
		t.Fatalf("policy blocked by benchmark: %v", err)
	}
	if err := m.Wake(context.Background(), "02:00:00:00:00:01"); err != nil {
		t.Fatalf("WOL blocked: %v", err)
	}
	if adapter.policies != 1 {
		t.Fatal("policy not executed")
	}
	close(release)
	m.wg.Wait()
}

func TestManualSelectionSurvivesConcurrentBenchmark(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "vpn", true: "direct"}[direct], func(t *testing.T) {
			m, _, _ := fixture(t)
			release := startHeldBenchmark(t, m)
			var err error
			if direct {
				err = m.SwitchDirect(context.Background())
			} else {
				err = m.SwitchSlot(context.Background(), 1)
			}
			if err != nil {
				t.Fatalf("manual selection blocked: %v", err)
			}
			close(release)
			m.wg.Wait()
			state := m.State()
			if state.DirectMode != direct || (!direct && state.ActiveNodeID != "fallback") {
				t.Fatalf("benchmark overwrote manual route: %+v", state)
			}
		})
	}
}

func TestRestartCancelsAndJoinsBenchmark(t *testing.T) {
	m, _, p := fixture(t)
	m.platform = &policyAdapter{fakeAdapter: p}
	startHeldBenchmark(t, m)
	if err := m.RestartXray(context.Background()); err != nil {
		t.Fatal(err)
	}
	if op := m.ops.Current(); op == nil || op.Status == "running" {
		t.Fatal("restart left benchmark running")
	}
	if !errors.Is(m.RunBenchmark(canceledContext(), "manual", "test"), context.Canceled) {
		t.Fatal("canceled request accepted")
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestSubscriptionChangeDiscardsInFlightBenchmark(t *testing.T) {
	m, _, _ := fixture(t)
	fetcher := &fakeManagedFetcher{sources: subscriptionManagerConfig().Subscriptions.Sources}
	// Keep the existing node fetcher for this held test. Changing a source updates
	// the durable configuration revision even though measurements are underway.
	release := startHeldBenchmark(t, m)
	m.fetcher = fetcher
	cfg := subscriptionManagerConfig()
	m.cfg = cfg
	if err := m.SaveSubscription(context.Background(), cfg.Subscriptions.Sources[0]); err != nil {
		t.Fatal(err)
	}
	close(release)
	m.wg.Wait()
	if m.State().LastBenchmark.OperationID != "" {
		t.Fatal("outdated subscription benchmark was committed")
	}
	if op := m.ops.Current(); op.Status != "failed" || op.Error == "" {
		t.Fatalf("stale result not reported: %+v", op)
	}
}

type queueAgainBenchmark struct {
	manager *Manager
	runs    int
}

func (b *queueAgainBenchmark) Run(context.Context, []model.Node, bench.Progress) ([]model.Measurement, error) {
	b.runs++
	if b.runs == 1 {
		b.manager.queueBenchmark("subscription-change")
	}
	return healthyReviewBenchmark().results, nil
}
func TestQueuedChangesAreNotLostWhenBenchmarkCompletes(t *testing.T) {
	m, _, _ := fixture(t)
	m.ctx, m.cancel = context.WithCancel(context.Background())
	defer m.Stop()
	m.benchmarkQueued = false
	runner := &queueAgainBenchmark{manager: m}
	m.bench = runner
	m.queueBenchmark("source-refresh")
	m.wg.Wait()
	if runner.runs != 2 || m.State().LastBenchmark.Mode != "subscription-change" {
		t.Fatalf("pending change dropped: runs=%d mode=%s", runner.runs, m.State().LastBenchmark.Mode)
	}
}

func TestAdmissionSnapshotPreservesImmediateManualSelection(t *testing.T) {
	m, _, _ := fixture(t)
	reservation, err := m.reserveBenchmark(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.cleanup()
	if err = m.SwitchSlot(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	m.bench = healthyReviewBenchmark()
	if err = m.runBenchmark(reservation, "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if m.State().ActiveNodeID != "fallback" {
		t.Fatal("selection after acceptance but before workers was overwritten")
	}
}

func TestExplicitCancelReportsCanceledAndRetainsRoute(t *testing.T) {
	m, _, _ := fixture(t)
	startHeldBenchmark(t, m)
	before := m.State()
	if err := m.CancelBenchmark(context.Background()); err != nil {
		t.Fatal(err)
	}
	if op := m.ops.Current(); op.Status != "canceled" {
		t.Fatalf("explicit cancellation reported as failure: %+v", op)
	}
	if state := m.State(); state.XrayGeneration != before.XrayGeneration || state.ActiveNodeID != before.ActiveNodeID {
		t.Fatal("canceled benchmark changed route")
	}
}
