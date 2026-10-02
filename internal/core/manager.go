package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
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

func (m *Manager) RunBenchmark(ctx context.Context, mode, source string) error {
	if mode != "manual" && m.store.State().AutomaticRoutingPaused {
		return nil
	}
	if !m.beginWork() {
		return context.Canceled
	}
	defer m.wg.Done()
	h, err := m.ops.Start("benchmark", source)
	if err != nil {
		return err
	}
	return m.runBenchmark(ctx, mode, source, h)
}
func (m *Manager) RequestBenchmark(ctx context.Context, source string) (*operation.Operation, error) {
	if !m.beginWork() {
		return nil, context.Canceled
	}
	h, err := m.ops.Start("benchmark", source)
	if err != nil {
		m.wg.Done()
		return nil, err
	}
	op := m.ops.Current()
	runCtx := m.ctx
	if runCtx == nil {
		runCtx = ctx
	}
	go func() { defer m.wg.Done(); _ = m.runBenchmark(runCtx, "manual", source, h) }()
	return op, nil
}
func (m *Manager) runBenchmark(ctx context.Context, mode, source string, h *operation.Handle) error {
	var err error
	var fetched subscription.Result
	started := time.Now().UTC()
	m.log("info", "benchmark.started", "benchmark started", h.ID(), map[string]any{"mode": mode, "source": source})
	finish := func(runErr error) error {
		completionErr := m.finishOperation(h, runErr, "benchmark complete")
		if completionErr != nil {
			m.log("error", "benchmark.failed", completionErr.Error(), h.ID(), nil)
			return completionErr
		}
		m.log("info", "benchmark.completed", "benchmark complete", h.ID(), nil)
		m.mu.Lock()
		m.benchmarkSourceNodes = append([]model.Node(nil), fetched.Nodes...)
		m.mu.Unlock()
		return nil
	}

	_ = h.Update("subscriptions", 0, len(m.cfg.Subscriptions.Sources), "fetching subscriptions")
	beforeFetch := m.store.State()
	forceFetch := mode != "emergency"
	fetched = m.fetcher.FetchAll(ctx, beforeFetch.Sources, forceFetch)
	_ = m.store.Update(func(s *model.State) error {
		s.Sources = fetched.States
		return nil
	})
	if len(fetched.Nodes) == 0 {
		err = fmt.Errorf("no nodes available from subscriptions or cache")
		return finish(err)
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
		return finish(err)
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
	if mode != "manual" && state.AutomaticRoutingPaused {
		return finish(nil)
	}
	// Choose an authorized replacement before active retention can fill a single slot.
	if state.ActiveNodeID != "" {
		if previous, ok := state.Measurements[state.ActiveNodeID]; ok {
			if _, measuredNow := measurements[state.ActiveNodeID]; !measuredNow {
				measurements[state.ActiveNodeID] = previous
			}
		}
	}
	newSlots := assignSlots(state.Pool, selected, measurements, m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size)
	winner := firstHealthy(newSlots)
	if winner < 0 {
		// Score/target outages are measurement results, never an authorization to
		// enter direct or discard the working pool. Only the failover machine decides.
		retained := retainedNodeList(fetched.Nodes, state.Pool, allNodes)
		keep := map[string]model.Measurement{}
		for _, n := range retained {
			if measurement, ok := measurements[n.ID]; ok {
				keep[n.ID] = measurement
			}
		}
		if err = m.store.ReplaceNodes(retained); err != nil {
			return finish(err)
		}
		err = m.store.Update(func(st *model.State) error {
			st.Measurements = keep
			st.LastBenchmark = model.BenchmarkSummary{OperationID: h.ID(), StartedAt: started, FinishedAt: time.Now().UTC(), Mode: mode, NodeCount: len(fetched.Nodes), TestedCount: len(results), Results: trimResults(results, 100)}
			return nil
		})
		return finish(err)
	}
	desired := -1
	reason := ""
	if winner >= 0 {
		switch {
		case state.DirectMode:
		// Recovery is decided by consecutive failover probes, never a benchmark.
		case state.ActiveNodeID == "" || mode == "manual":
			desired = winner
			reason = "benchmark " + mode
		default:
			activeM, activeKnown := measurements[state.ActiveNodeID]
			winnerM := measurements[newSlots[winner].NodeID]
			activeHealthy := activeKnown && activeM.Healthy
			if index := slotIndexForNode(state.Pool, state.ActiveNodeID); index >= 0 {
				active := state.Pool[index]
				if active.LastVerifiedAt.After(activeM.CheckedAt) {
					activeHealthy = active.Healthy
				}
			}
			if !activeHealthy && mode != "emergency" && mode != "recovery" && mode != "source-refresh" {
				desired = winner
				reason = "active node unhealthy"
			} else if activeKnown && activeM.Healthy && time.Since(state.LastSwitchAt) >= m.cfg.Benchmark.SwitchCooldown.Duration &&
				time.Since(state.ActiveSince) >= m.cfg.Benchmark.StabilityBeforeUpgrade.Duration &&
				improvement(activeM.Score, winnerM.Score) >= float64(m.cfg.Benchmark.MinImprovementPercent) {
				desired = winner
				reason = "better node found"
			}
		}
	}

	// Keep the active route unless the decision above authorizes its replacement.
	// Larger pools still retain the old active node as a fallback.
	desiredNode := ""
	if desired >= 0 {
		desiredNode = newSlots[desired].NodeID
	}
	if m.cfg.Pool.Size > 1 || desiredNode == "" {
		selected = retainActiveNode(selected, state.ActiveNodeID, allNodes, m.cfg.Pool.Size)
		newSlots = assignSlots(state.Pool, selected, measurements, m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size)
		winner = firstHealthy(newSlots)
		if desiredNode != "" {
			desired = slotIndexForNode(newSlots, desiredNode)
		}
	}

	direct := state.DirectMode
	finalSlot := slotIndexForNode(newSlots, state.ActiveNodeID)
	if desired >= 0 {
		finalSlot = desired
		direct = false
	}
	if !direct && finalSlot < 0 {
		finalSlot = winner
		reason = "active node no longer available"
	}
	retainedNodes := retainedNodeList(fetched.Nodes, newSlots, allNodes)
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
	next := state
	update := func(s *model.State) error {
		// Only committing a healthy manually requested pool resumes routing after
		// restore. Failed measurements leave the durable pause untouched.
		if mode == "manual" {
			s.AutomaticRoutingPaused = false
		}
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
			if !state.DirectMode {
				s.LastSwitchAt = now
				s.LastSwitchReason = reason
			}
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
	}
	_ = update(&next)
	err = m.commitRoute(ctx, next, retainedNodes, "pool")
	return finish(err)
}

func (m *Manager) ForceBenchmark(ctx context.Context) error {
	return m.RunBenchmark(ctx, "manual", "web")
}

func (m *Manager) healthLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.cfg.Failover.DetectionInterval.Duration)
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
	// Track the last processed subscription set separately from nodes retained
	// for routing. A removed fallback triggers once; an intentionally retained
	// node does not create a change on every subsequent poll.
	m.mu.RLock()
	previous := m.benchmarkSourceNodes
	m.mu.RUnlock()
	if previous == nil {
		previous = m.store.Nodes()
	}
	if recovered || nodeSetChanged(previous, result.Nodes) {
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
		v.Error = redact.Text(err.Error())
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
		m.clientsError = redact.Text(err.Error())
		return
	}
	m.clients = xs
	m.clientsUpdated = time.Now().UTC()
	m.clientsError = ""
}

func (m *Manager) checkHealth(ctx context.Context) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	if pending, err := m.store.PendingTransaction(); err != nil || pending != nil {
		repairErr := m.reconcileStartup(ctx)
		m.mu.Lock()
		m.readinessErr = repairErr
		m.mu.Unlock()
		if repairErr != nil {
			return
		}
	}
	state := m.store.State()
	if state.AutomaticRoutingPaused || !state.XrayConfigured {
		return
	}
	if err := m.xray.Ready(ctx); err != nil {
		m.mu.Lock()
		m.xrayRunning = false
		m.readinessErr = err
		m.recoveryNode = ""
		m.recoveryCount = 0
		m.mu.Unlock()
		_ = m.store.Update(func(st *model.State) error {
			st.LastHealthClass = "xray_failed"
			st.XrayLastError = err.Error()
			return nil
		})
		if m.platform.Capabilities().DirectBypass {
			next := directState(state, "Xray unavailable; independent platform bypass")
			next.LastHealthClass = "xray_failed"
			next.XrayLastError = err.Error()
			if err = m.commitRoute(ctx, next, m.store.Nodes(), "select"); err != nil {
				m.log("error", "failover.bypass_failed", err.Error(), "", nil)
			}
		}
		return
	}
	m.mu.Lock()
	m.xrayRunning = true
	m.mu.Unlock()
	targets := m.cfg.TargetsByRole("health")
	if state.DirectMode {
		if m.platform.Capabilities().DirectBypass {
			if err := m.platform.EnterDirectBypass(ctx); err != nil {
				m.mu.Lock()
				m.readinessErr = err
				m.mu.Unlock()
				return
			}
			m.mu.Lock()
			m.readinessErr = nil
			m.mu.Unlock()
		}
		m.checkRecovery(ctx, state, targets)
		return
	}
	if err := m.reconcileSelection(ctx, state); err != nil {
		m.mu.Lock()
		m.readinessErr = err
		m.mu.Unlock()
		return
	}
	if firewallErr := m.platform.EnsureFirewall(ctx); firewallErr != nil {
		m.mu.Lock()
		m.readinessErr = firewallErr
		m.mu.Unlock()
		m.log("error", "firewall.reconcile_failed", firewallErr.Error(), "", nil)
		return
	}
	m.mu.Lock()
	m.readinessErr = nil
	m.mu.Unlock()
	proxy, err := m.xray.HealthEndpoint()
	if err != nil {
		return
	}
	prober := bench.NewProber(m.cfg.Failover.ProbeTimeout.Duration, int64(m.cfg.Health.MaxResponseBytes))
	defer prober.Close()
	probeCtx, cancel := context.WithTimeout(ctx, m.cfg.Failover.OverallDeadline.Duration)
	defer cancel()
	var vpn, wan []bench.ProbeResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _, vpn = prober.CheckMajority(probeCtx, proxy, targets) }()
	go func() { defer wg.Done(); _, _, wan = prober.CheckMajority(probeCtx, nil, targets) }()
	wg.Wait()
	classification := bench.Classify(targets, vpn, wan, m.cfg.Failover.Quorum)
	m.mu.RLock()
	wanConnected := m.metrics.WANConnected
	m.mu.RUnlock()
	if classification == "monitoring_inconclusive" && wanConnected != nil && !*wanConnected {
		classification = "wan_failed"
	}
	now := time.Now().UTC()
	update := func(st *model.State) error {
		st.LastHealthAt = now
		st.LastHealthMessage = classification
		st.LastHealthClass = classification
		switch classification {
		case "healthy":
			if st.ActiveSlot >= 0 && st.ActiveSlot < len(st.Pool) {
				st.Pool[st.ActiveSlot].Healthy = true
				st.Pool[st.ActiveSlot].LastVerifiedAt = now
			}
			st.ConsecutiveFailures = 0
			st.ConsecutiveSuccess++
		case "vpn_path_failed":
			st.ConsecutiveFailures++
			st.ConsecutiveSuccess = 0
		default:
			st.ConsecutiveFailures = 0
		}
		return nil
	}
	if classification == "healthy" {
		_ = m.store.UpdateVolatile(update)
		for _, slot := range m.store.State().Pool {
			if slot.NodeID != "" && (slot.LastVerifiedAt.IsZero() || time.Since(slot.LastVerifiedAt) >= m.cfg.Health.HotPoolFreshness.Duration) {
				m.queueBenchmark("hot-pool-refresh")
				break
			}
		}

		return
	}
	_ = m.store.Update(update)
	state = m.store.State()
	if classification != "vpn_path_failed" || state.ConsecutiveFailures < m.cfg.Failover.FailureThreshold {
		return
	}
	m.emergencyFailover(ctx, state, targets)
}

// emergencyCandidate probes slots concurrently, cancelling losers after the
// first independent health quorum. The total wait is bounded separately.
func (m *Manager) emergencyCandidate(ctx context.Context, state model.State, targets []config.Target, includeActive bool) (model.Slot, bool) {
	deadline, cancel := context.WithTimeout(ctx, m.cfg.Failover.OverallDeadline.Duration)
	defer cancel()
	prober := bench.NewProber(m.cfg.Failover.ProbeTimeout.Duration, int64(m.cfg.Health.MaxResponseBytes))
	defer prober.Close()
	results := make(chan model.Slot, len(state.Pool))
	var wg sync.WaitGroup
	for _, slot := range state.Pool {
		if slot.NodeID == "" || (!includeActive && slot.Index == state.ActiveSlot) {
			continue
		}
		wg.Add(1)
		go func(slot model.Slot) {
			defer wg.Done()
			proxy, err := m.xray.ProbeEndpoint(slot.Index)
			if err != nil {
				return
			}
			_, _, checks := prober.CheckMajority(deadline, proxy, targets)
			if bench.Quorum(targets, checks, m.cfg.Failover.Quorum) {
				results <- slot
			}
		}(slot)
	}
	complete := make(chan struct{})
	go func() { wg.Wait(); close(complete) }()
	var winner model.Slot
	found := false
	select {
	case winner = <-results:
		found = true
	case <-complete:
		select {
		case winner = <-results:
			found = true
		default:
		}
	case <-deadline.Done():
	}
	cancel()
	<-complete
	return winner, found
}
func vpnState(state model.State, slot model.Slot, reason string) model.State {
	now := time.Now().UTC()
	slot.Healthy = true
	slot.LastVerifiedAt = now
	if slot.Index >= 0 && slot.Index < len(state.Pool) {
		state.Pool[slot.Index] = slot
	}
	state.ActiveSlot = slot.Index
	state.ActiveNodeID = slot.NodeID
	state.ActiveSince = now
	state.DirectMode = false
	state.ConsecutiveFailures = 0
	state.LastSwitchAt = now
	state.LastSwitchReason = reason
	return state
}
func directState(state model.State, reason string) model.State {
	now := time.Now().UTC()
	state.DirectMode = true
	state.ActiveSlot = -1
	state.ActiveNodeID = ""
	state.ActiveSince = time.Time{}
	state.LastSwitchAt = now
	state.LastSwitchReason = reason
	return state
}
func (m *Manager) emergencyFailover(ctx context.Context, state model.State, targets []config.Target) {
	emergencyCtx, cancel := context.WithTimeout(ctx, m.cfg.Failover.OverallDeadline.Duration)
	defer cancel()
	ctx = emergencyCtx
	// Confirm WAN in parallel, leaving part of the overall budget for the route
	// transaction. A hanging fallback must not consume the confirmation budget.
	wanResult := make(chan bool, 1)
	go func() {
		prober := bench.NewProber(m.cfg.Failover.ProbeTimeout.Duration, int64(m.cfg.Health.MaxResponseBytes))
		defer prober.Close()
		_, _, checks := prober.CheckMajority(ctx, nil, targets)
		wanResult <- bench.Quorum(targets, checks, m.cfg.Failover.Quorum)
	}()
	fallbackCtx, stopFallback := context.WithTimeout(ctx, m.cfg.Failover.OverallDeadline.Duration*3/4)
	defer stopFallback()
	if slot, ok := m.emergencyCandidate(fallbackCtx, state, targets, false); ok {
		if err := m.commitRoute(ctx, vpnState(state, slot, "emergency failover"), m.store.Nodes(), "select"); err == nil {
			m.log("warning", "failover.vpn", "switched to confirmed fallback VPN", "", nil)
			m.queueBenchmark("emergency")
			return
		} else {
			m.log("error", "failover.switch_failed", err.Error(), "", nil)
			return
		}
	}
	// Confirm ordinary WAN independently; endpoint/WAN outages cannot authorize a
	// VPN-to-direct switch even when every slot probe has failed.
	select {
	case healthy := <-wanResult:
		if !healthy {
			return
		}
	case <-ctx.Done():
		return
	}
	if ctx.Err() != nil {
		return
	}
	next := directState(state, "all VPN paths unavailable; WAN quorum confirmed")
	if err := m.commitRoute(ctx, next, m.store.Nodes(), "select"); err != nil {
		m.log("error", "failover.direct_failed", err.Error(), "", nil)
		return
	}
	m.log("error", "failover.direct", "all VPN paths unavailable; direct route applied", "", nil)
	m.queueBenchmark("emergency")
}
func (m *Manager) checkRecovery(ctx context.Context, state model.State, targets []config.Target) {
	m.mu.RLock()
	candidate := m.recoveryNode
	m.mu.RUnlock()
	probeState := state
	if candidate != "" {
		// Consecutive success belongs to one path, independent of race ordering.
		probeState.Pool = nil
		for _, slot := range state.Pool {
			if slot.NodeID == candidate {
				probeState.Pool = append(probeState.Pool, slot)
			}
		}
	}
	slot, ok := m.emergencyCandidate(ctx, probeState, targets, true)
	m.mu.Lock()
	if !ok {
		m.recoveryNode = ""
		m.recoveryCount = 0
		m.mu.Unlock()
		return
	}
	if slot.NodeID == m.recoveryNode {
		m.recoveryCount++
	} else {
		m.recoveryNode = slot.NodeID
		m.recoveryCount = 1
	}
	count := m.recoveryCount
	m.mu.Unlock()
	if count < m.cfg.Health.RecoveryThreshold {
		return
	}
	if err := m.commitRoute(ctx, vpnState(state, slot, "VPN recovered"), m.store.Nodes(), "select"); err != nil {
		return
	}
	m.mu.Lock()
	m.recoveryNode = ""
	m.recoveryCount = 0
	m.mu.Unlock()
	m.log("info", "recovery.vpn", "VPN connectivity recovered", "", nil)
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
	if !m.beginWork() {
		m.mu.Lock()
		m.benchmarkQueued = false
		m.mu.Unlock()
		return
	}
	go func() {
		defer m.wg.Done()
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
	if !m.beginWork() {
		return context.Canceled
	}
	defer m.wg.Done()
	h, err := m.ops.Start(kind, source)
	if err != nil {
		return err
	}
	m.log("info", "action.started", kind+" started", h.ID(), nil)
	_ = h.Update("running", 0, 1, kind)
	err = fn(ctx)
	err = m.finishOperation(h, err, kind+" complete")
	if err != nil {
		m.log("error", "action.failed", err.Error(), h.ID(), map[string]any{"type": kind})
		return err
	}
	m.log("info", "action.completed", kind+" complete", h.ID(), nil)
	return nil
}

func (m *Manager) finishOperation(h *operation.Handle, runErr error, message string) error {
	var err error
	if runErr != nil {
		err = h.Fail(runErr)
	} else {
		err = h.Success(message)
	}
	if err != nil {
		m.log("error", "operation.persistence_failed", err.Error(), h.ID(), nil)
	}
	return errors.Join(runErr, err)
}
func (m *Manager) RestoreOriginalXray(ctx context.Context) error {
	return m.RunAction(ctx, "xray-restore-original", "cli", func(c context.Context) error {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		next := model.NewState(m.version, m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size)
		next.AutomaticRoutingPaused = true
		next.Pool = []model.Slot{}
		next.Sources = m.store.State().Sources
		return m.commitRoute(c, next, nil, "restore")
	})
}

func (m *Manager) RestartXray(ctx context.Context) error {
	return m.RunAction(ctx, "xray-restart", "web", func(c context.Context) error {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		if err := m.platform.RestartXray(c); err != nil {
			return err
		}
		if err := m.xray.Ready(c); err != nil {
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
		return m.commitRoute(c, vpnState(state, state.Pool[index], "manual switch"), m.store.Nodes(), "select")
	})
}
func (m *Manager) SwitchDirect(ctx context.Context) error {
	return m.RunAction(ctx, "switch-direct", "web", func(c context.Context) error {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		state := m.store.State()
		if !state.XrayConfigured {
			return fmt.Errorf("tunnel has not been configured")
		}
		return m.commitRoute(c, directState(state, "manual direct"), m.store.Nodes(), "select")
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
		allowed := true
		for _, source := range n.Sources {
			if counts[source] >= max {
				allowed = false
			}
		}
		if !allowed {
			continue
		}
		out = append(out, n)
		used[n.ID] = true
		for _, source := range n.Sources {
			counts[source]++
		}
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
				// A retained node may be absent from the current subscription. Its
				// historical score must not erase newer live health verification.
				if s.LastVerifiedAt.After(mm.CheckedAt) {
					out[s.Index].Healthy = s.Healthy
					out[s.Index].LastVerifiedAt = s.LastVerifiedAt
				}
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
	actual, err := m.xray.ActualState(ctx)
	if err != nil {
		return err
	}
	if !actual.Configured || actual.Drift != "" || (state.XrayConfigHash != "" && state.XrayConfigHash != actual.ConfigHash) {
		return fmt.Errorf("managed tunnel drift requires reconciliation")
	}

	if err := m.xray.Ready(ctx); err != nil {
		return err
	}
	if state.DirectMode && m.platform.Capabilities().DirectBypass {
		return m.platform.EnterDirectBypass(ctx)
	}
	if state.DirectMode {
		return m.xray.EnterDirect(ctx)
	}
	if state.ActiveSlot >= 0 && state.ActiveSlot < len(state.Pool) {
		slot := state.Pool[state.ActiveSlot]
		if slot.NodeID != "" && (state.ActiveNodeID == "" || slot.NodeID == state.ActiveNodeID) {
			return m.xray.Select(ctx, tunnel.Selection{Tag: slot.Tag})
		}
	}
	if index := slotIndexForNode(state.Pool, state.ActiveNodeID); index >= 0 {
		return m.xray.Select(ctx, tunnel.Selection{Tag: state.Pool[index].Tag})
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

func (m *Manager) beginWork() bool {
	m.lifeMu.Lock()
	defer m.lifeMu.Unlock()
	if m.stopping {
		return false
	}
	m.wg.Add(1)
	return true
}
