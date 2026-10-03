package core

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

type benchmarkRunner interface {
	Run(context.Context, []model.Node, bench.Progress) ([]model.Measurement, error)
}
type sourceFetcher interface {
	FetchAll(context.Context, map[string]model.SourceState, bool) subscription.Result
}

type Manager struct {
	cfg                  config.Config
	version              string
	store                *store.Store
	ops                  *operation.Coordinator
	platform             platform.Adapter
	xray                 tunnel.TunnelCore
	fetcher              sourceFetcher
	bench                benchmarkRunner
	ctx                  context.Context
	cancel               context.CancelFunc
	wg                   sync.WaitGroup
	mu                   sync.RWMutex
	routeMu              sync.Mutex
	lifeMu               sync.Mutex
	stopping             bool
	metrics              platform.Metrics
	clients              []platform.Client
	clientsUpdated       time.Time
	clientsError         string
	recoveryNode         string
	recoveryCount        int
	benchmarkQueued      bool
	benchmarkSourceNodes []model.Node
	xrayRunning          bool
	reconciled           bool
	readinessErr         error
}

type Status struct {
	Version      string                `json:"version"`
	State        model.State           `json:"state"`
	Operation    *operation.Operation  `json:"operation,omitempty"`
	Capabilities platform.Capabilities `json:"capabilities"`
	Platform     string                `json:"platform"`
	XrayRunning  bool                  `json:"xray_running"`
	ServerTime   time.Time             `json:"server_time"`
}
type ClientsSnapshot struct {
	Value     []platform.Client `json:"value"`
	UpdatedAt time.Time         `json:"updated_at"`
	Stale     bool              `json:"stale"`
	Error     string            `json:"error,omitempty"`
}

func New(c config.Config, version string, st *store.Store, ops *operation.Coordinator, p platform.Adapter, xm tunnel.TunnelCore, fetch sourceFetcher, be benchmarkRunner) *Manager {
	return &Manager{cfg: c, version: version, store: st, ops: ops, platform: p, xray: xm, fetcher: fetch, bench: be}
}
func (m *Manager) Start(parent context.Context) {
	m.ctx, m.cancel = context.WithCancel(parent)
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	m.routeMu.Lock()
	err := m.reconcileStartup(ctx)
	if err != nil && m.store.State().XrayConfigured && m.platform.Capabilities().DirectBypass && m.xray.Ready(ctx) != nil {
		degraded := directState(m.store.State(), "startup: Xray unavailable; independent platform bypass")
		degraded.LastHealthClass = "xray_failed"
		degraded.XrayLastError = err.Error()
		if bypassErr := m.commitRoute(ctx, degraded, m.store.Nodes(), "select"); bypassErr == nil {
			err = nil
		} else {
			err = errors.Join(err, bypassErr)
		}
	}
	m.routeMu.Unlock()
	cancel()
	m.mu.Lock()
	m.reconciled = true
	m.readinessErr = err
	m.mu.Unlock()
	if err != nil {
		m.log("error", "startup.reconciliation_failed", err.Error(), "", nil)
	}
	m.wg.Add(1)
	go m.healthLoop()
	m.wg.Add(1)
	go m.benchmarkLoop()
	m.wg.Add(1)
	go m.platformLoop()
	m.wg.Add(1)
	go m.sourceLoop()
	if err == nil {
		go func() { _ = m.RunBenchmark(m.ctx, "startup", "scheduler") }()
	}
}
func (m *Manager) Stop() {
	m.lifeMu.Lock()
	m.stopping = true
	if m.cancel != nil {
		m.cancel()
	}
	m.lifeMu.Unlock()
	m.wg.Wait()
	_ = m.store.Flush()
}
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.RLock()
	running := m.xrayRunning
	m.mu.RUnlock()
	return Status{m.version, m.store.State(), m.ops.Current(), m.platform.Capabilities(), m.platform.Kind(), running, time.Now().UTC()}
}
func (m *Manager) State() model.State                           { return m.store.State() }
func (m *Manager) Events(after uint64, limit int) []event.Event { return m.store.Events(after, limit) }
func (m *Manager) Nodes() []model.NodeView {
	state := m.store.State()
	pool := map[string]bool{}
	for _, s := range state.Pool {
		if s.NodeID != "" {
			pool[s.NodeID] = true
		}
	}
	out := []model.NodeView{}
	for _, n := range m.store.Nodes() {
		out = append(out, model.NodeView{ID: n.ID, Label: n.Label, Protocol: n.Protocol, Network: n.Network, Security: n.Security, Sources: n.Sources, Measurement: state.Measurements[n.ID], InPool: pool[n.ID], Active: n.ID == state.ActiveNodeID})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Measurement, out[j].Measurement
		if a.Score == b.Score {
			return out[i].Label < out[j].Label
		}
		if a.Score == 0 {
			return false
		}
		if b.Score == 0 {
			return true
		}
		return a.Score < b.Score
	})
	return out
}
func (m *Manager) Metrics() platform.Metrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v := m.metrics
	if v.UpdatedAt.IsZero() || time.Since(v.UpdatedAt) > 15*time.Second {
		v.Stale = true
	}
	return v
}
func (m *Manager) Clients() ClientsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	xs := append([]platform.Client(nil), m.clients...)
	return ClientsSnapshot{xs, m.clientsUpdated, m.clientsError != "" || m.clientsUpdated.IsZero() || time.Since(m.clientsUpdated) > time.Minute, m.clientsError}
}
func (m *Manager) SystemLogs(ctx context.Context, lines int) (string, error) {
	return m.platform.SystemLogs(ctx, lines)
}
func (m *Manager) Diagnostics(ctx context.Context) (string, error) {
	return m.platform.Diagnostics(ctx)
}
