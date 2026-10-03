package core

import (
	"context"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
)

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
