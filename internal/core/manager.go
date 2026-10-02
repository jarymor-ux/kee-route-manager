package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
)

type Manager struct {
	cfg             config.Config
	version         string
	store           *store.Store
	ops             *operation.Coordinator
	platform        platform.Adapter
	xray            *xray.Manager
	fetcher         *subscription.Fetcher
	bench           *bench.Engine
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	mu              sync.RWMutex
	routeMu         sync.Mutex
	metrics         platform.Metrics
	clients         []platform.Client
	clientsUpdated  time.Time
	clientsError    string
	recoveryNode    string
	recoveryCount   int
	benchmarkQueued bool
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

func New(c config.Config, version string, st *store.Store, ops *operation.Coordinator, p platform.Adapter, xm *xray.Manager, fetch *subscription.Fetcher, be *bench.Engine) *Manager {
	return &Manager{cfg: c, version: version, store: st, ops: ops, platform: p, xray: xm, fetcher: fetch, bench: be}
}
func (m *Manager) Start(parent context.Context) {
	m.ctx, m.cancel = context.WithCancel(parent)
	if m.store.State().XrayConfigured {
		if err := m.platform.EnsureFirewall(m.ctx); err != nil {
			m.log("error", "firewall.ensure_failed", err.Error(), "", nil)
		}
		ctx, cancel := context.WithTimeout(m.ctx, 20*time.Second)
		m.routeMu.Lock()
		if err := m.reconcileSelection(ctx, m.store.State()); err != nil {
			m.log("error", "xray.selection_reconcile_failed", err.Error(), "", nil)
		}
		m.routeMu.Unlock()
		cancel()
	}
	m.wg.Add(1)
	go m.healthLoop()
	m.wg.Add(1)
	go m.benchmarkLoop()
	m.wg.Add(1)
	go m.platformLoop()
	m.wg.Add(1)
	go m.sourceLoop()
	go func() { _ = m.RunBenchmark(m.ctx, "startup", "scheduler") }()
}
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	_ = m.store.Flush()
}
func (m *Manager) Status(ctx context.Context) Status {
	return Status{m.version, m.store.State(), m.ops.Current(), m.platform.Capabilities(), m.platform.Kind(), m.platform.XrayRunning(ctx), time.Now().UTC()}
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
	if !v.UpdatedAt.IsZero() && time.Since(v.UpdatedAt) > 15*time.Second {
		v.Stale = true
	}
	return v
}
func (m *Manager) Clients() ClientsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	xs := append([]platform.Client(nil), m.clients...)
	return ClientsSnapshot{xs, m.clientsUpdated, m.clientsUpdated.IsZero() || time.Since(m.clientsUpdated) > time.Minute, m.clientsError}
}
func (m *Manager) SystemLogs(ctx context.Context, lines int) (string, error) {
	return m.platform.SystemLogs(ctx, lines)
}
func (m *Manager) Diagnostics(ctx context.Context) (string, error) {
	return m.platform.Diagnostics(ctx)
}

func (m *Manager) RunBenchmark(ctx context.Context, mode, source string) error {
	h, err := m.ops.Start("benchmark", source)
	if err != nil {
		return err
	}
	started := time.Now().UTC()
	m.log("info", "benchmark.started", "benchmark started", h.ID(), map[string]any{"mode": mode, "source": source})
	finish := func(runErr error) {
		if runErr != nil {
			_ = h.Fail(runErr)
			m.log("error", "benchmark.failed", runErr.Error(), h.ID(), nil)
			return
		}
		_ = h.Success("benchmark complete")
		m.log("info", "benchmark.completed", "benchmark complete", h.ID(), nil)
	}

	_ = h.Update("subscriptions", 0, len(m.cfg.Subscriptions.Sources), "fetching subscriptions")
	beforeFetch := m.store.State()
	forceFetch := mode != "emergency"
	fetched := m.fetcher.FetchAll(ctx, beforeFetch.Sources, forceFetch)
	_ = m.store.Update(func(s *model.State) error {
		s.Sources = fetched.States
		return nil
	})
	if len(fetched.Nodes) == 0 {
		err = fmt.Errorf("no nodes available from subscriptions or cache")
		finish(err)
		return err
	}

	oldNodes := m.store.Nodes()
	allNodes := make(map[string]model.Node, len(oldNodes)+len(fetched.Nodes))
	for _, n := range oldNodes {
		allNodes[n.ID] = n
	}
	for _, n := range fetched.Nodes {
		allNodes[n.ID] = n
	}

	_ = h.Update("benchmark", 0, len(fetched.Nodes), "testing nodes")
	results, err := m.bench.Run(ctx, fetched.Nodes, func(stage string, current, total int, msg string) {
		_ = h.Update(stage, current, total, msg)
	})
	if err != nil {
		finish(err)
		return err
	}

	// Preserve prior measurements for a currently active node that disappeared from a
	// refreshed subscription. It remains usable until a safe switch has completed.
	measurements := make(map[string]model.Measurement, len(results)+len(beforeFetch.Measurements))
	for id, measurement := range beforeFetch.Measurements {
		measurements[id] = measurement
	}
	for _, result := range results {
		measurements[result.NodeID] = result
	}
	selected := m.selectPool(fetched.Nodes, results)

	_ = h.Update("apply", 0, m.cfg.Pool.Size, "applying hot pool")
	m.routeMu.Lock()
	defer m.routeMu.Unlock()

	// Health failover may have changed the active slot while the long benchmark was
	// running. Re-read state under the routing mutation lock and retain that node.
	state := m.store.State()
	selected = retainActiveNode(selected, state.ActiveNodeID, allNodes, m.cfg.Pool.Size)
	if state.ActiveNodeID != "" {
		if previous, ok := state.Measurements[state.ActiveNodeID]; ok {
			if _, measuredNow := measurements[state.ActiveNodeID]; !measuredNow {
				measurements[state.ActiveNodeID] = previous
			}
		}
	}
	newSlots := assignSlots(state.Pool, selected, measurements, m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size)
	winner := firstHealthy(newSlots)
	initialTag := m.cfg.Xray.ManagedDirectTag
	if state.DirectMode {
		initialTag = m.cfg.Xray.ManagedDirectTag
	} else if activeIndex := slotIndexForNode(newSlots, state.ActiveNodeID); activeIndex >= 0 {
		initialTag = newSlots[activeIndex].Tag
	} else if winner >= 0 {
		initialTag = newSlots[winner].Tag
	}

	if !state.XrayConfigured {
		err = m.xray.Bootstrap(ctx, newSlots, allNodes, initialTag)
	} else {
		err = m.xray.ApplyPool(ctx, state.Pool, newSlots, allNodes, state.ActiveSlot)
	}
	if err != nil {
		_ = m.store.Update(func(s *model.State) error { s.XrayLastError = err.Error(); return nil })
		finish(err)
		return err
	}
	if err = m.platform.EnsureFirewall(ctx); err != nil {
		_ = m.store.Update(func(s *model.State) error { s.XrayLastError = err.Error(); return nil })
		finish(err)
		return err
	}

	desired := -1
	reason := ""
	if winner >= 0 {
		switch {
		case state.DirectMode || state.ActiveNodeID == "" || mode == "startup" || mode == "manual" || mode == "emergency":
			desired = winner
			reason = "benchmark " + mode
		default:
			activeM, activeKnown := measurements[state.ActiveNodeID]
			winnerM := measurements[newSlots[winner].NodeID]
			if !activeKnown || !activeM.Healthy {
				desired = winner
				reason = "active node unhealthy"
			} else if time.Since(state.LastSwitchAt) >= m.cfg.Benchmark.SwitchCooldown.Duration &&
				time.Since(state.ActiveSince) >= m.cfg.Benchmark.StabilityBeforeUpgrade.Duration &&
				improvement(activeM.Score, winnerM.Score) >= float64(m.cfg.Benchmark.MinImprovementPercent) {
				desired = winner
				reason = "better node found"
			}
		}
	}

	direct := false
	finalSlot := -1
	switch {
	case winner < 0:
		if err = m.xray.Direct(ctx); err != nil {
			finish(err)
			return err
		}
		direct = true
		reason = "no healthy VPN nodes"
	case desired >= 0:
		if err = m.xray.Switch(ctx, newSlots[desired].Tag); err != nil {
			finish(err)
			return err
		}
		finalSlot = desired
	default:
		// Dynamic API may be disabled, in which case ApplyPool restarted Xray and
		// cleared the in-memory balancer override. Always reassert the selection.
		finalSlot = slotIndexForNode(newSlots, state.ActiveNodeID)
		if finalSlot < 0 {
			finalSlot = winner
			reason = "active node no longer available"
		}
		if err = m.xray.Switch(ctx, newSlots[finalSlot].Tag); err != nil {
			finish(err)
			return err
		}
	}

	retainedNodes := retainedNodeList(fetched.Nodes, newSlots, allNodes)
	if err = m.store.ReplaceNodes(retainedNodes); err != nil {
		finish(err)
		return err
	}
	filteredMeasurements := make(map[string]model.Measurement, len(retainedNodes))
	for _, node := range retainedNodes {
		if measurement, ok := measurements[node.ID]; ok {
			filteredMeasurements[node.ID] = measurement
		}
	}

	now := time.Now().UTC()
	summary := model.BenchmarkSummary{OperationID: h.ID(), StartedAt: started, FinishedAt: now, Mode: mode, NodeCount: len(fetched.Nodes), TestedCount: len(results), Results: trimResults(results, 100)}
	if winner >= 0 {
		summary.WinnerID = newSlots[winner].NodeID
		summary.WinnerLabel = newSlots[winner].Label
	}
	err = m.store.Update(func(s *model.State) error {
		s.Measurements = filteredMeasurements
		s.Pool = newSlots
		s.Sources = fetched.States
		s.LastBenchmark = summary
		s.XrayConfigured = true
		s.XrayGeneration++
		s.XrayLastError = ""
		if direct {
			s.DirectMode = true
			s.ActiveSlot = -1
			s.ActiveNodeID = ""
			s.ActiveSince = time.Time{}
			s.LastSwitchAt = now
			s.LastSwitchReason = reason
		} else {
			previousNode := s.ActiveNodeID
			s.DirectMode = false
			s.ActiveSlot = newSlots[finalSlot].Index
			s.ActiveNodeID = newSlots[finalSlot].NodeID
			if previousNode != s.ActiveNodeID || s.ActiveSince.IsZero() {
				s.ActiveSince = now
			}
			if reason != "" {
				s.LastSwitchAt = now
				s.LastSwitchReason = reason
			}
		}
		return nil
	})
	finish(err)
	return err
}

func (m *Manager) ForceBenchmark(ctx context.Context) error {
	return m.RunBenchmark(ctx, "manual", "web")
}

func (m *Manager) healthLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.cfg.Health.Interval.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.checkHealth(m.ctx)
		}
	}
}
func (m *Manager) benchmarkLoop() {
	defer m.wg.Done()
	d := m.cfg.Benchmark.FullInterval.Duration
	if d < time.Minute {
		d = time.Minute
	}
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			go func() { _ = m.RunBenchmark(m.ctx, "scheduled", "scheduler") }()
		}
	}
}
func (m *Manager) sourceLoop() {
	defer m.wg.Done()
	interval := 15 * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.refreshSources()
		}
	}
}
func (m *Manager) refreshSources() {
	before := m.store.State()
	result := m.fetcher.FetchAll(m.ctx, before.Sources, false)
	_ = m.store.Update(func(s *model.State) error { s.Sources = result.States; return nil })
	if len(result.Nodes) == 0 {
		return
	}
	recovered := false
	for id, now := range result.States {
		old := before.Sources[id]
		if !now.UsingCache && (old.Status == "unavailable" || old.Status == "degraded" || old.Status == "") && (now.Status == "recovering" || now.Status == "healthy") {
			recovered = true
			break
		}
	}
	if recovered || nodeSetChanged(m.store.Nodes(), result.Nodes) {
		m.queueBenchmark("source-refresh")
	}
}

func (m *Manager) platformLoop() {
	defer m.wg.Done()
	metrics := time.NewTicker(5 * time.Second)
	clients := time.NewTicker(20 * time.Second)
	defer metrics.Stop()
	defer clients.Stop()
	m.pollMetrics()
	m.pollClients()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-metrics.C:
			m.pollMetrics()
		case <-clients.C:
			m.pollClients()
		}
	}
}
func (m *Manager) pollMetrics() {
	ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
	defer cancel()
	v, err := m.platform.Metrics(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		v = m.metrics
		v.Stale = true
		v.Error = err.Error()
	} else {
		v.Error = ""
		v.Stale = false
	}
	m.metrics = v
}
func (m *Manager) pollClients() {
	if !m.platform.Capabilities().Clients {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
	defer cancel()
	xs, err := m.platform.Clients(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.clientsError = err.Error()
		return
	}
	m.clients = xs
	m.clientsUpdated = time.Now().UTC()
	m.clientsError = ""
}

func (m *Manager) checkHealth(ctx context.Context) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	state := m.store.State()
	if !state.XrayConfigured {
		return
	}
	if err := m.reconcileSelection(ctx, state); err != nil {
		m.log("error", "xray.selection_reconcile_failed", err.Error(), "", nil)
		_ = m.store.Update(func(s *model.State) error { s.XrayLastError = err.Error(); return nil })
		return
	}
	targets := m.cfg.TargetsByRole("health")
	if state.DirectMode {
		m.checkRecovery(ctx, state, targets)
		return
	}
	proxy, _ := url.Parse(m.xray.HealthProxy())
	passed, total, results := bench.NewProber(m.cfg.Health.RequestTimeout.Duration, int64(m.cfg.Health.MaxResponseBytes)).CheckMajority(ctx, proxy, targets)
	healthy := bench.Majority(passed, total)
	msg := fmt.Sprintf("%d/%d health targets", passed, total)
	if !healthy && len(results) > 0 {
		msg = results[0].Error
	}
	now := time.Now().UTC()
	updateHealth := func(s *model.State) error {
		s.LastHealthAt = now
		s.LastHealthMessage = msg
		if healthy {
			s.ConsecutiveFailures = 0
			s.ConsecutiveSuccess++
		} else {
			s.ConsecutiveFailures++
			s.ConsecutiveSuccess = 0
		}
		return nil
	}
	if healthy {
		_ = m.store.UpdateVolatile(updateHealth)
	} else {
		_ = m.store.Update(updateHealth)
	}
	state = m.store.State()
	if healthy {
		return
	}
	if state.ConsecutiveFailures < m.cfg.Health.FailureThreshold {
		return
	}
	m.emergencyFailover(ctx, state, targets)
}
func (m *Manager) emergencyFailover(ctx context.Context, state model.State, targets []config.Target) {
	prober := bench.NewProber(m.cfg.Health.RequestTimeout.Duration, int64(m.cfg.Health.MaxResponseBytes))
	order := append([]model.Slot(nil), state.Pool...)
	sort.SliceStable(order, func(i, j int) bool {
		ai, aj := order[i].LastVerifiedAt, order[j].LastVerifiedAt
		if ai.Equal(aj) {
			return order[i].Score < order[j].Score
		}
		return ai.After(aj)
	})
	for _, s := range order {
		if s.NodeID == "" || s.Index == state.ActiveSlot {
			continue
		}
		proxy, _ := url.Parse(m.xray.SlotProxy(s.Index))
		passed, total, _ := prober.CheckMajority(ctx, proxy, targets)
		if bench.Majority(passed, total) {
			if err := m.xray.Switch(ctx, s.Tag); err == nil {
				now := time.Now().UTC()
				_ = m.store.Update(func(st *model.State) error {
					st.ActiveSlot = s.Index
					st.ActiveNodeID = s.NodeID
					st.ActiveSince = now
					st.DirectMode = false
					st.ConsecutiveFailures = 0
					st.LastSwitchAt = now
					st.LastSwitchReason = "emergency failover"
					return nil
				})
				m.log("warning", "failover.vpn", "switched to fallback VPN node", "", map[string]any{"node": s.Label})
				m.queueBenchmark("emergency")
				return
			}
		}
	}
	if err := m.xray.Direct(ctx); err == nil {
		now := time.Now().UTC()
		_ = m.store.Update(func(st *model.State) error {
			st.DirectMode = true
			st.ActiveSlot = -1
			st.ActiveNodeID = ""
			st.LastSwitchAt = now
			st.LastSwitchReason = "all VPN nodes unavailable"
			return nil
		})
		m.log("error", "failover.direct", "all VPN nodes unavailable; traffic is direct", "", nil)
		m.queueBenchmark("emergency")
	}
}
func (m *Manager) checkRecovery(ctx context.Context, state model.State, targets []config.Target) {
	prober := bench.NewProber(m.cfg.Health.RequestTimeout.Duration, int64(m.cfg.Health.MaxResponseBytes))
	candidate := ""
	index := -1
	for _, s := range state.Pool {
		if s.NodeID == "" {
			continue
		}
		proxy, _ := url.Parse(m.xray.SlotProxy(s.Index))
		passed, total, _ := prober.CheckMajority(ctx, proxy, targets)
		if bench.Majority(passed, total) {
			candidate = s.NodeID
			index = s.Index
			break
		}
	}
	m.mu.Lock()
	if candidate == "" {
		m.recoveryNode = ""
		m.recoveryCount = 0
		m.mu.Unlock()
		return
	}
	if candidate == m.recoveryNode {
		m.recoveryCount++
	} else {
		m.recoveryNode = candidate
		m.recoveryCount = 1
	}
	count := m.recoveryCount
	m.mu.Unlock()
	if count < m.cfg.Health.RecoveryThreshold {
		return
	}
	slot := state.Pool[index]
	if err := m.xray.Switch(ctx, slot.Tag); err != nil {
		return
	}
	now := time.Now().UTC()
	_ = m.store.Update(func(st *model.State) error {
		st.DirectMode = false
		st.ActiveSlot = index
		st.ActiveNodeID = slot.NodeID
		st.ActiveSince = now
		st.LastSwitchAt = now
		st.LastSwitchReason = "VPN recovered"
		st.ConsecutiveFailures = 0
		st.ConsecutiveSuccess = 0
		return nil
	})
	m.mu.Lock()
	m.recoveryNode = ""
	m.recoveryCount = 0
	m.mu.Unlock()
	m.log("info", "recovery.vpn", "VPN connectivity recovered", "", map[string]any{"node": slot.Label})
	m.queueBenchmark("recovery")
}
func (m *Manager) queueBenchmark(mode string) {
	m.mu.Lock()
	if m.benchmarkQueued {
		m.mu.Unlock()
		return
	}
	m.benchmarkQueued = true
	m.mu.Unlock()
	go func() {
		defer func() { m.mu.Lock(); m.benchmarkQueued = false; m.mu.Unlock() }()
		for i := 0; i < 6; i++ {
			err := m.RunBenchmark(m.ctx, mode, "health")
			if !errors.Is(err, operation.ErrBusy) {
				return
			}
			select {
			case <-m.ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
	}()
}

func (m *Manager) RunAction(ctx context.Context, kind, source string, fn func(context.Context) error) error {
	h, err := m.ops.Start(kind, source)
	if err != nil {
		return err
	}
	m.log("info", "action.started", kind+" started", h.ID(), nil)
	_ = h.Update("running", 0, 1, kind)
	err = fn(ctx)
	if err != nil {
		_ = h.Fail(err)
		m.log("error", "action.failed", err.Error(), h.ID(), map[string]any{"type": kind})
		return err
	}
	_ = h.Success(kind + " complete")
	m.log("info", "action.completed", kind+" complete", h.ID(), nil)
	return nil
}
func (m *Manager) RestoreOriginalXray(ctx context.Context) error {
	return m.RunAction(ctx, "xray-restore-original", "cli", func(c context.Context) error {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		if err := m.platform.RemoveFirewall(c); err != nil {
			return err
		}
		return m.xray.RestoreOriginal(c)
	})
}

func (m *Manager) RestartXray(ctx context.Context) error {
	return m.RunAction(ctx, "xray-restart", "web", func(c context.Context) error {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		if err := m.platform.RestartXray(c); err != nil {
			return err
		}
		if err := m.xray.WaitReady(c, 20*time.Second); err != nil {
			return err
		}
		return m.reconcileSelection(c, m.store.State())
	})
}
func (m *Manager) Reboot(ctx context.Context) error {
	return m.RunAction(ctx, "router-reboot", "web", m.platform.Reboot)
}
func (m *Manager) Wake(ctx context.Context, mac string) error {
	return m.RunAction(ctx, "wake-on-lan", "web", func(c context.Context) error { return m.platform.Wake(c, mac) })
}
func (m *Manager) SetPolicy(ctx context.Context, mac, policy string) error {
	return m.RunAction(ctx, "client-policy", "web", func(c context.Context) error { return m.platform.SetClientPolicy(c, mac, policy) })
}
func (m *Manager) SwitchSlot(ctx context.Context, index int) error {
	state := m.store.State()
	if index < 0 || index >= len(state.Pool) || state.Pool[index].NodeID == "" {
		return fmt.Errorf("invalid slot")
	}
	return m.RunAction(ctx, "switch-slot", "web", func(c context.Context) error {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		state = m.store.State()
		if index < 0 || index >= len(state.Pool) || state.Pool[index].NodeID == "" {
			return fmt.Errorf("invalid slot")
		}
		if err := m.xray.Switch(c, state.Pool[index].Tag); err != nil {
			return err
		}
		now := time.Now().UTC()
		return m.store.Update(func(s *model.State) error {
			s.DirectMode = false
			s.ActiveSlot = index
			s.ActiveNodeID = s.Pool[index].NodeID
			s.ActiveSince = now
			s.LastSwitchAt = now
			s.LastSwitchReason = "manual switch"
			return nil
		})
	})
}
func (m *Manager) SwitchDirect(ctx context.Context) error {
	return m.RunAction(ctx, "switch-direct", "web", func(c context.Context) error {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		if err := m.xray.Direct(c); err != nil {
			return err
		}
		now := time.Now().UTC()
		return m.store.Update(func(s *model.State) error {
			s.DirectMode = true
			s.ActiveSlot = -1
			s.ActiveNodeID = ""
			s.LastSwitchAt = now
			s.LastSwitchReason = "manual direct"
			return nil
		})
	})
}
func (m *Manager) log(level, kind, message, op string, fields map[string]any) {
	_, _ = m.store.Append(event.Event{Level: level, Type: kind, Message: message, OperationID: op, Fields: fields})
}

func (m *Manager) selectPool(nodes []model.Node, results []model.Measurement) []model.Node {
	byID := map[string]model.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	candidates := []model.Node{}
	for _, r := range results {
		if r.Healthy {
			if n, ok := byID[r.NodeID]; ok {
				candidates = append(candidates, n)
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return scoreOf(results, candidates[i].ID) < scoreOf(results, candidates[j].ID) })
	if !m.cfg.Pool.ProviderDiversity.Enabled {
		return take(candidates, m.cfg.Pool.Size)
	}
	out := []model.Node{}
	counts := map[string]int{}
	used := map[string]bool{}
	max := m.cfg.Pool.ProviderDiversity.MaxPerProvider
	if max < 1 {
		max = 1
	}
	for _, n := range candidates {
		p := firstSource(n)
		if counts[p] >= max {
			continue
		}
		out = append(out, n)
		used[n.ID] = true
		counts[p]++
		if len(out) >= m.cfg.Pool.Size {
			return out
		}
	}
	for _, n := range candidates {
		if used[n.ID] {
			continue
		}
		out = append(out, n)
		if len(out) >= m.cfg.Pool.Size {
			break
		}
	}
	return out
}
func assignSlots(old []model.Slot, selected []model.Node, measurements map[string]model.Measurement, prefix string, size int) []model.Slot {
	out := make([]model.Slot, size)
	selectedMap := map[string]model.Node{}
	for _, n := range selected {
		selectedMap[n.ID] = n
	}
	used := map[string]bool{}
	for i := 0; i < size; i++ {
		out[i] = model.Slot{Index: i, Tag: fmt.Sprintf("%s%d", prefix, i)}
	}
	for _, s := range old {
		if s.Index >= 0 && s.Index < size {
			if n, ok := selectedMap[s.NodeID]; ok {
				mm := measurements[n.ID]
				out[s.Index] = model.Slot{Index: s.Index, Tag: fmt.Sprintf("%s%d", prefix, s.Index), NodeID: n.ID, Label: n.Label, Sources: n.Sources, Healthy: mm.Healthy, LastVerifiedAt: mm.CheckedAt, Score: mm.Score}
				used[n.ID] = true
			}
		}
	}
	next := 0
	for _, n := range selected {
		if used[n.ID] {
			continue
		}
		for next < size && out[next].NodeID != "" {
			next++
		}
		if next >= size {
			break
		}
		mm := measurements[n.ID]
		out[next] = model.Slot{Index: next, Tag: fmt.Sprintf("%s%d", prefix, next), NodeID: n.ID, Label: n.Label, Sources: n.Sources, Healthy: mm.Healthy, LastVerifiedAt: mm.CheckedAt, Score: mm.Score}
		used[n.ID] = true
	}
	return out
}
func firstHealthy(slots []model.Slot) int {
	best := -1
	for i, s := range slots {
		if s.NodeID == "" || !s.Healthy {
			continue
		}
		if best < 0 || s.Score < slots[best].Score {
			best = i
		}
	}
	return best
}
func improvement(current, next float64) float64 {
	if current <= 0 || next <= 0 || next >= current {
		return 0
	}
	return 100 * (current - next) / current
}
func scoreOf(rs []model.Measurement, id string) float64 {
	for _, r := range rs {
		if r.NodeID == id {
			return r.Score
		}
	}
	return 999999
}
func take(xs []model.Node, n int) []model.Node {
	if len(xs) > n {
		return append([]model.Node(nil), xs[:n]...)
	}
	return append([]model.Node(nil), xs...)
}
func firstSource(n model.Node) string {
	if len(n.Sources) == 0 {
		return "unknown"
	}
	return n.Sources[0]
}
func nodeSetChanged(a, b []model.Node) bool {
	if len(a) != len(b) {
		return true
	}
	set := map[string]bool{}
	for _, n := range a {
		set[n.ID] = true
	}
	for _, n := range b {
		if !set[n.ID] {
			return true
		}
	}
	return false
}

func trimResults(xs []model.Measurement, n int) []model.Measurement {
	if len(xs) > n {
		return append([]model.Measurement(nil), xs[:n]...)
	}
	return append([]model.Measurement(nil), xs...)
}

func (m *Manager) reconcileSelection(ctx context.Context, state model.State) error {
	if !state.XrayConfigured {
		return nil
	}
	if err := m.xray.WaitReady(ctx, 3*time.Second); err != nil {
		return err
	}
	if state.DirectMode {
		return m.xray.Direct(ctx)
	}
	if state.ActiveSlot >= 0 && state.ActiveSlot < len(state.Pool) {
		slot := state.Pool[state.ActiveSlot]
		if slot.NodeID != "" && (state.ActiveNodeID == "" || slot.NodeID == state.ActiveNodeID) {
			return m.xray.Switch(ctx, slot.Tag)
		}
	}
	if index := slotIndexForNode(state.Pool, state.ActiveNodeID); index >= 0 {
		return m.xray.Switch(ctx, state.Pool[index].Tag)
	}
	return fmt.Errorf("active VPN selection is not present in the hot pool")
}

func retainActiveNode(selected []model.Node, activeID string, all map[string]model.Node, size int) []model.Node {
	if activeID == "" || size < 1 {
		return take(selected, size)
	}
	for _, node := range selected {
		if node.ID == activeID {
			return take(selected, size)
		}
	}
	active, ok := all[activeID]
	if !ok {
		return take(selected, size)
	}
	out := append([]model.Node(nil), selected...)
	if len(out) >= size {
		out = out[:size-1]
	}
	out = append(out, active)
	return out
}

func slotIndexForNode(slots []model.Slot, nodeID string) int {
	if nodeID == "" {
		return -1
	}
	for i, slot := range slots {
		if slot.NodeID == nodeID {
			return i
		}
	}
	return -1
}

func retainedNodeList(fetched []model.Node, slots []model.Slot, all map[string]model.Node) []model.Node {
	byID := make(map[string]model.Node, len(fetched)+len(slots))
	for _, node := range fetched {
		byID[node.ID] = node
	}
	for _, slot := range slots {
		if slot.NodeID == "" {
			continue
		}
		if node, ok := all[slot.NodeID]; ok {
			byID[node.ID] = node
		}
	}
	out := make([]model.Node, 0, len(byID))
	for _, node := range byID {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
