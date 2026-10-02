package core

import (
	"context"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
	"time"
)

func nodeMap(nodes []model.Node) map[string]model.Node {
	out := make(map[string]model.Node, len(nodes))
	for _, n := range nodes {
		out[n.ID] = n
	}
	return out
}
func (m *Manager) selection(state model.State) tunnel.Selection {
	if state.DirectMode {
		return tunnel.Selection{Tag: m.cfg.Xray.ManagedDirectTag}
	}
	if state.ActiveSlot >= 0 && state.ActiveSlot < len(state.Pool) {
		return tunnel.Selection{Tag: state.Pool[state.ActiveSlot].Tag}
	}
	return tunnel.Selection{}
}

// commitRoute writes desired state before touching tunnel or firewall. Any crash
// leaves a replayable transaction; startup reads actual state before forward repair.
func (m *Manager) commitRoute(ctx context.Context, next model.State, nodes []model.Node, kind string) error {
	before := m.store.State()
	actual, err := m.xray.ActualState(ctx)
	if err != nil {
		return err
	}
	if actual.Drift != "" {
		return fmt.Errorf("managed tunnel routing drift detected: %s", actual.Drift)
	}
	if before.XrayConfigHash != "" && actual.Configured && before.XrayConfigHash != actual.ConfigHash {
		return fmt.Errorf("managed tunnel configuration drift detected; review changes before routing mutation")
	}
	tx := store.Transaction{ID: fmt.Sprintf("route-%d", time.Now().UnixNano()), Kind: kind, Before: m.store.State(), Desired: next, Nodes: nodes}
	if guard, ok := m.xray.(tunnel.ReplayGuard); ok {
		desired := tunnel.DesiredPool{Previous: before.Pool, Slots: next.Pool, Nodes: nodeMap(nodes), ActiveSlot: next.ActiveSlot, Selection: m.selection(next)}
		tx.Replay, err = guard.PrepareReplay(ctx, desired, kind == "restore")
		if err != nil {
			return err
		}
	}
	if err := m.store.PrepareTransaction(tx); err != nil {
		return err
	}
	err = m.applyTransaction(ctx, tx)
	m.mu.Lock()
	m.readinessErr = err
	m.mu.Unlock()
	return err
}
func (m *Manager) applyTransaction(ctx context.Context, tx store.Transaction) error {
	next := tx.Desired
	actual, err := m.xray.ActualState(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.xrayRunning = actual.Running
	m.mu.Unlock()
	if err = m.store.AdvanceTransaction(store.XrayFilesStaged); err != nil {
		return err
	}
	if tx.Kind == "restore" {
		if err = m.xray.Restore(ctx); err != nil {
			return err
		}
	} else if next.XrayConfigured && tx.Kind != "select" && !(next.DirectMode && m.platform.Capabilities().DirectBypass && !actual.Running) {
		desired := tunnel.DesiredPool{Previous: tx.Before.Pool, Slots: next.Pool, Nodes: nodeMap(tx.Nodes), ActiveSlot: next.ActiveSlot, Selection: m.selection(next)}
		// Re-bootstrap from validated on-disk files on replay, so stale runtime slots
		// cannot be mistaken for already-applied updates after an interrupted API call.
		if !actual.Configured || tx.Kind == "reconcile" {
			err = m.xray.Bootstrap(ctx, desired)
		} else {
			err = m.xray.ApplyPool(ctx, desired)
		}
		if err != nil {
			return err
		}
	}
	if err = m.store.AdvanceTransaction(store.XrayRuntimeApplied); err != nil {
		return err
	}
	if next.XrayConfigured && tx.Kind != "restore" {
		if next.DirectMode {
			// Supported platform bypass survives an unavailable tunnel. Xray freedom is
			// used only for platforms that explicitly report no independent bypass.
			if !m.platform.Capabilities().DirectBypass {
				err = m.xray.EnterDirect(ctx)
			}
		} else {
			err = m.xray.Select(ctx, m.selection(next))
		}
		if err != nil {
			return err
		}
	}
	switch {
	case tx.Kind == "restore":
		err = m.platform.RemoveFirewall(ctx)
	case next.DirectMode && m.platform.Capabilities().DirectBypass:
		err = m.platform.EnterDirectBypass(ctx)
	case next.XrayConfigured:
		err = m.platform.LeaveDirectBypass(ctx)
	}
	if err != nil {
		return err
	}
	if err = m.store.AdvanceTransaction(store.FirewallApplied); err != nil {
		return err
	}
	if next.XrayConfigured {
		actual, err = m.xray.ActualState(ctx)
		if err != nil {
			return err
		}
		next.XrayConfigHash = actual.ConfigHash
		m.mu.Lock()
		m.xrayRunning = actual.Running
		m.mu.Unlock()
	}
	if err = m.store.AdvanceTransaction(store.SelectionApplied); err != nil {
		return err
	}
	if err = m.store.ReplaceNodes(tx.Nodes); err != nil {
		return err
	}
	if err = m.store.Update(func(state *model.State) error { *state = next; return nil }); err != nil {
		return err
	}
	if err = m.store.AdvanceTransaction(store.StateCommitted); err != nil {
		return err
	}
	return m.store.AdvanceTransaction(store.Done)
}
func (m *Manager) reconcileStartup(ctx context.Context) error {
	pending, err := m.store.PendingTransaction()
	if err != nil {
		return err
	}
	if pending != nil {
		if pending.Replay != nil {
			guard, ok := m.xray.(tunnel.ReplayGuard)
			if !ok {
				return fmt.Errorf("tunnel backend cannot validate pending replay proof")
			}
			if err = guard.ValidateReplay(ctx, pending.Replay); err != nil {
				return err
			}
		} else {
			// Legacy journals have no per-file proof. Refuse observed drift rather than
			// guessing that an operator edit was an interrupted managed write.
			if _, persistent := m.xray.(tunnel.ReplayGuard); persistent && pending.Before.XrayConfigHash == "" {
				return fmt.Errorf("legacy journal lacks verifiable tunnel identity; operator reconciliation required")
			}
			actual, observeErr := m.xray.ActualState(ctx)
			if observeErr != nil {
				return observeErr
			}
			if actual.Drift != "" {
				return fmt.Errorf("managed tunnel routing drift detected before journal replay: %s", actual.Drift)
			}
			if pending.Before.XrayConfigHash != "" && (!actual.Configured || pending.Before.XrayConfigHash != actual.ConfigHash) {
				return fmt.Errorf("managed tunnel configuration drift detected before legacy journal replay")
			}
		}
		if _, err = m.platform.DirectBypassActive(ctx); err != nil {
			return err
		}
		if err = m.store.RestartTransaction(*pending); err != nil {
			return err
		}
		if pending.Kind != "restore" {
			pending.Kind = "reconcile"
		}
		return m.applyTransaction(ctx, *pending)
	}
	state := m.store.State()
	actual, err := m.xray.ActualState(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.xrayRunning = actual.Running
	m.mu.Unlock()
	if actual.Drift != "" {
		return fmt.Errorf("managed tunnel routing drift detected: %s", actual.Drift)
	}
	if state.XrayConfigHash != "" && actual.Configured && state.XrayConfigHash != actual.ConfigHash {
		return fmt.Errorf("managed tunnel configuration drift detected during startup")
	}
	if !state.XrayConfigured {
		if actual.Configured {
			return fmt.Errorf("managed Xray files exist without valid state; restore or operator reconciliation required")
		}
		return nil
	}
	if state.DirectMode && m.platform.Capabilities().DirectBypass {
		return m.platform.EnterDirectBypass(ctx)
	}
	if err = m.reconcileSelection(ctx, state); err != nil {
		return err
	}
	return m.platform.LeaveDirectBypass(ctx)
}

// Readiness is cached. HTTP polling never launches commands or probes targets.
func (m *Manager) Readiness() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.reconciled {
		return fmt.Errorf("startup reconciliation incomplete")
	}
	return m.readinessErr
}
