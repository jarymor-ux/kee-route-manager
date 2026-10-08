package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
)

func (m *Manager) queueBenchmark(mode string) {
	m.mu.Lock()
	m.benchmarkQueueVersion++
	m.benchmarkQueueMode = mode
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
		for {
			m.mu.RLock()
			version, currentMode := m.benchmarkQueueVersion, m.benchmarkQueueMode
			m.mu.RUnlock()
			err := m.RunBenchmark(m.ctx, currentMode, "health")
			if !errors.Is(err, operation.ErrBusy) && !errors.Is(err, operation.ErrSuperseded) {
				m.mu.Lock()
				if version == m.benchmarkQueueVersion {
					m.benchmarkQueued = false
					m.mu.Unlock()
					return
				}
				m.mu.Unlock()
				continue
			}
			select {
			case <-m.ctx.Done():
				m.mu.Lock()
				m.benchmarkQueued = false
				m.mu.Unlock()
				return
			case <-time.After(time.Second):
			}
		}
	}()
}
func (m *Manager) RunAction(ctx context.Context, kind, source string, fn func(context.Context) error) error {
	if !m.beginWork() {
		return context.Canceled
	}
	defer m.wg.Done()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.admissionMu.TryLock() {
		return operation.ErrBusy
	}
	// Destructive actions stop a test cleanly. Holding admission prevents a
	// scheduled replacement test from slipping in while cancellation is joined.
	switch kind {
	case "xray-restore-original", "xray-restart", "router-reboot":
		if err := m.CancelBenchmark(ctx); err != nil {
			m.admissionMu.Unlock()
			return err
		}
	}
	h, err := m.ops.StartCompatible(kind, source)
	m.admissionMu.Unlock()
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
		if err := m.commitRoute(c, vpnState(state, state.Pool[index], "manual switch"), m.store.Nodes(), "select"); err != nil {
			return err
		}
		m.manualRouteVersion++
		return nil
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
		if err := m.commitRoute(c, directState(state, "manual direct"), m.store.Nodes(), "select"); err != nil {
			return err
		}
		m.manualRouteVersion++
		return nil
	})
}
func (m *Manager) log(level, kind, message, op string, fields map[string]any) {
	_, _ = m.store.Append(event.Event{Level: level, Type: kind, Message: message, OperationID: op, Fields: fields})
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
