package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
)

type observerAdapter struct {
	fakeAdapter
	metrics platform.Metrics
	clients []platform.Client
	err     error
	polled  chan struct{}
}

func (p *observerAdapter) Capabilities() platform.Capabilities {
	caps := p.fakeAdapter.Capabilities()
	caps.Clients = true
	return caps
}
func (p *observerAdapter) Metrics(context.Context) (platform.Metrics, error) {
	if p.polled != nil {
		select {
		case p.polled <- struct{}{}:
		default:
		}
	}
	return p.metrics, p.err
}
func (p *observerAdapter) Clients(context.Context) ([]platform.Client, error) {
	return p.clients, p.err
}

func TestPlatformSnapshotsExposeStalenessAndRedactFailures(t *testing.T) {
	m, _, _ := fixture(t)
	p := &observerAdapter{metrics: platform.Metrics{RAMUsedMB: 12, UpdatedAt: time.Now()}, clients: []platform.Client{{MAC: "02:00:00:00:00:01", Name: "router"}}}
	m.platform = p
	if !m.Metrics().Stale || !m.Clients().Stale {
		t.Error("unpolled snapshots must be stale")
	}
	m.pollMetrics()
	m.pollClients()
	if m.Metrics().Stale || m.Clients().Stale {
		t.Fatal("successful polls remain stale")
	}
	clients := m.Clients()
	clients.Value[0].Name = "changed"
	if m.Clients().Value[0].Name != "router" {
		t.Fatal("client snapshot aliases cache")
	}
	p.err = errors.New("adapter failed token=private-value")
	m.pollMetrics()
	m.pollClients()
	if got := m.Metrics(); !got.Stale || got.RAMUsedMB != 12 || got.Error == "" || strings.Contains(got.Error, "private-value") {
		t.Errorf("bad failed metrics snapshot: %+v", got)
	}
	if got := m.Clients(); !got.Stale || len(got.Value) != 1 || got.Error == "" || strings.Contains(got.Error, "private-value") {
		t.Errorf("bad failed clients snapshot: %+v", got)
	}
	p.err = nil
	m.pollMetrics()
	m.pollClients()
	if m.Metrics().Stale || m.Metrics().Error != "" || m.Clients().Stale || m.Clients().Error != "" {
		t.Fatal("successful retry did not clear failure")
	}
	m.metrics.UpdatedAt = time.Now().Add(-time.Minute)
	m.clientsUpdated = time.Now().Add(-2 * time.Minute)
	if !m.Metrics().Stale || !m.Clients().Stale {
		t.Fatal("expired observations remain fresh")
	}
}

func TestStartStopReconcilesAndJoinsBackgroundWork(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "benchmark", true: "restored-pause"}[paused], func(t *testing.T) {
			m, _, _ := fixture(t)
			p := &observerAdapter{fakeAdapter: fakeAdapter{supportsBypass: true}, polled: make(chan struct{}, 1)}
			m.platform = p
			m.cfg.Failover.DetectionInterval = config.Dur(time.Hour)
			if paused {
				if err := m.RestoreOriginalXray(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			started := make(chan struct{})
			m.bench = fakeBenchmark{start: started, release: make(chan struct{})}
			if err := m.Readiness(); err == nil {
				t.Fatal("readiness succeeded before reconciliation")
			}
			m.Start(context.Background())
			t.Cleanup(m.Stop)
			if err := m.Readiness(); err != nil {
				t.Fatalf("startup reconciliation failed: %v", err)
			}
			select {
			case <-p.polled:
			case <-time.After(3 * time.Second):
				t.Fatal("platform loop never polled")
			}
			if !paused {
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("startup benchmark never started")
				}
			}
			stopped := make(chan struct{})
			go func() { m.Stop(); close(stopped) }()
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("Stop failed to cancel and join background workers")
			}
			if paused {
				select {
				case <-started:
					t.Fatal("startup benchmark ignored restore pause")
				default:
				}
			} else if op := m.ops.Current(); op.Status != "failed" {
				t.Fatalf("canceled startup benchmark status: %+v", op)
			}
			if err := m.RunBenchmark(context.Background(), "manual", "test"); !errors.Is(err, context.Canceled) {
				t.Fatalf("work accepted after Stop: %v", err)
			}
		})
	}
}

func TestNodeViewsExposeRankedMeasurementsWithoutMutatingStore(t *testing.T) {
	m, _, _ := fixture(t)
	nodes := m.store.Nodes()
	nodes[0].Sources = []string{"provider"}
	nodes = append(nodes, model.Node{ID: "unmeasured", Label: "A"})
	if err := m.store.ReplaceNodes(nodes); err != nil {
		t.Fatal(err)
	}
	if err := m.store.Update(func(state *model.State) error {
		state.Measurements["active"] = model.Measurement{NodeID: "active", Score: 20}
		state.Measurements["fallback"] = model.Measurement{NodeID: "fallback", Score: 10}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	views := m.Nodes()
	if len(views) != 3 || views[0].ID != "fallback" || views[1].ID != "active" || views[2].ID != "unmeasured" {
		t.Fatalf("incorrect measured ranking: %+v", views)
	}
	if !views[0].InPool || views[0].Active || !views[1].Active || views[2].InPool {
		t.Fatal("node selection flags are wrong")
	}
	views[1].Sources[0] = "changed"
	if node, _ := m.store.Node("active"); node.Sources[0] != "provider" {
		t.Fatal("public node view mutates private cache")
	}
}

func TestScheduledUpgradeHonorsCooldownAndImprovement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		score   float64
		want    string
	}{
		{"cooldown", time.Second, 10, "active"},
		{"insufficient-improvement", 24 * time.Hour, 99, "active"},
		{"confirmed-improvement", 24 * time.Hour, 10, "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := fixture(t)
			if err := m.store.Update(func(state *model.State) error {
				state.LastSwitchAt = time.Now().Add(-tc.elapsed)
				state.ActiveSince = state.LastSwitchAt
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			m.bench = fakeBenchmark{results: []model.Measurement{{NodeID: "active", Healthy: true, Score: 100}, {NodeID: "fallback", Healthy: true, Score: tc.score}}}
			if err := m.RunBenchmark(context.Background(), "scheduled", "test"); err != nil {
				t.Fatal(err)
			}
			if got := m.State().ActiveNodeID; got != tc.want {
				t.Fatalf("active=%s, want %s", got, tc.want)
			}
		})
	}
}

func TestManualDirectRequiresConfiguredTunnel(t *testing.T) {
	m, tunnel, adapter := fixture(t)
	if err := m.SwitchDirect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !m.State().DirectMode || !adapter.bypass || tunnel.directCalls != 0 {
		t.Fatal("explicit direct control did not select independent bypass")
	}
	if err := m.RestoreOriginalXray(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.SwitchDirect(context.Background()); err == nil {
		t.Fatal("direct switch accepted after tunnel restore")
	}
	if m.State().DirectMode || adapter.bypass {
		t.Fatal("invalid direct switch changed routing")
	}
}
