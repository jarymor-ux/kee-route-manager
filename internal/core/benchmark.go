package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
)

type benchmarkReservation struct {
	handle                        *operation.Handle
	ctx                           context.Context
	cleanup                       func()
	cancel                        context.CancelFunc
	done                          chan struct{}
	explicitCancel                bool // protected by Manager.mu
	manualVersion, sourcesVersion uint64
}

// reserveBenchmark owns the cancellation handle before returning an accepted ID.
func (m *Manager) reserveBenchmark(ctx context.Context, source string) (*benchmarkReservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.admissionMu.TryLock() {
		return nil, operation.ErrBusy
	}
	defer m.admissionMu.Unlock()
	m.mu.RLock()
	joining := m.benchmark != nil
	m.mu.RUnlock()
	if joining {
		return nil, operation.ErrBusy
	}
	// Capture intent at admission, before exposing the accepted operation ID.
	m.routeMu.Lock()
	manualVersion := m.manualRouteVersion
	m.routeMu.Unlock()
	m.subscriptionMu.Lock()
	sourcesVersion := m.sourcesVersion
	m.subscriptionMu.Unlock()
	h, err := m.ops.StartCompatible("benchmark", source)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	reservation := &benchmarkReservation{handle: h, ctx: runCtx, cancel: cancel, done: done, manualVersion: manualVersion, sourcesVersion: sourcesVersion}
	m.mu.Lock()
	m.benchmark = reservation
	m.mu.Unlock()
	reservation.cleanup = func() {
		cancel()
		m.mu.Lock()
		if m.benchmark == reservation {
			m.benchmark = nil
		}
		close(done)
		m.mu.Unlock()
	}
	return reservation, nil
}
func (m *Manager) RunBenchmark(ctx context.Context, mode, source string) error {
	if mode != "manual" && m.store.State().AutomaticRoutingPaused {
		return nil
	}
	if !m.beginWork() {
		return context.Canceled
	}
	defer m.wg.Done()
	reservation, err := m.reserveBenchmark(ctx, source)
	if err != nil {
		return err
	}
	defer reservation.cleanup()
	return m.runBenchmark(reservation, mode, source)
}
func (m *Manager) RequestBenchmark(ctx context.Context, source string) (*operation.Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.beginWork() {
		return nil, context.Canceled
	}
	runCtx := m.ctx
	if runCtx == nil {
		runCtx = ctx
	}
	reservation, err := m.reserveBenchmark(runCtx, source)
	if err != nil {
		m.wg.Done()
		return nil, err
	}
	op := m.ops.Current()
	go func() {
		defer m.wg.Done()
		defer reservation.cleanup()
		_ = m.runBenchmark(reservation, "manual", source)
	}()
	return op, nil
}

// Cancellation waits for probe workers, temporary Xray batches, and the
// execution reservation to finish before lifecycle actions can take ownership.
func (m *Manager) CancelBenchmark(ctx context.Context) error {
	m.mu.Lock()
	reservation := m.benchmark
	if reservation != nil {
		reservation.explicitCancel = true
	}
	m.mu.Unlock()
	if reservation == nil {
		return nil
	}
	reservation.cancel()
	select {
	case <-reservation.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) runBenchmark(reservation *benchmarkReservation, mode, source string) error {
	ctx, h := reservation.ctx, reservation.handle
	manualVersion, sourcesVersion := reservation.manualVersion, reservation.sourcesVersion
	var err error
	var fetched subscription.Result
	started := time.Now().UTC()
	m.log("info", "benchmark.started", "benchmark started", h.ID(), map[string]any{"mode": mode, "source": source})
	finish := func(runErr error) error {
		m.mu.RLock()
		explicitCancel := reservation.explicitCancel
		m.mu.RUnlock()
		var completionErr error
		if errors.Is(runErr, context.Canceled) && explicitCancel {
			completionErr = errors.Join(runErr, h.Cancel("benchmark canceled"))
			m.log("info", "benchmark.canceled", "benchmark canceled", h.ID(), nil)
			return completionErr
		}
		completionErr = m.finishOperation(h, runErr, "benchmark complete")
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

	_ = h.Update("subscriptions", 0, len(m.SubscriptionSources()), "fetching subscriptions")
	beforeFetch := m.store.State()
	forceFetch := mode != "emergency"
	fetched = m.fetcher.FetchAll(ctx, beforeFetch.Sources, forceFetch)
	m.subscriptionMu.Lock()
	if m.sourcesVersion != sourcesVersion {
		m.subscriptionMu.Unlock()
		return finish(operation.ErrSuperseded)
	}
	err = m.store.Update(func(s *model.State) error { s.Sources = fetched.States; return nil })
	m.subscriptionMu.Unlock()
	if ctx.Err() != nil {
		return finish(ctx.Err())
	}
	if err != nil {
		return finish(err)
	}
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
	m.subscriptionMu.Lock()
	defer m.subscriptionMu.Unlock()
	if ctx.Err() != nil {
		return finish(ctx.Err())
	}
	if m.sourcesVersion != sourcesVersion {
		return finish(operation.ErrSuperseded)
	}
	manualChanged := m.manualRouteVersion != manualVersion
	// Include nodes added or retained by a concurrent manual selection.
	for _, node := range m.store.Nodes() {
		allNodes[node.ID] = node
	}

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
		case manualChanged:
			// An explicit choice made after this test started wins over its ranking.
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
