package core

import (
	"context"
	"fmt"

	"github.com/jarymor-ux/kee-route-manager/internal/operation"
)

// StartBenchmark reserves the web-triggered benchmark slot before returning 202.
// The operation coordinator still provides the final cross-action exclusion.
func (m *Manager) StartBenchmark(mode, source string) error {
	if op := m.ops.Current(); op != nil && op.Status == "running" {
		return operation.ErrBusy
	}
	m.mu.Lock()
	if m.benchmarkQueued {
		m.mu.Unlock()
		return operation.ErrBusy
	}
	m.benchmarkQueued = true
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			m.benchmarkQueued = false
			m.mu.Unlock()
		}()
		if err := m.RunBenchmark(m.ctxOrBackground(), mode, source); err != nil && err != context.Canceled {
			m.log("error", "benchmark.async_failed", err.Error(), "", map[string]any{"mode": mode, "source": source})
		}
	}()
	return nil
}

func (m *Manager) Ready() bool {
	state := m.store.State()
	if state.DirectMode {
		return true
	}
	if !state.XrayConfigured {
		return false
	}
	ctx := m.ctxOrBackground()
	return m.platform.XrayRunning(ctx)
}

func (m *Manager) ctxOrBackground() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m *Manager) EnsureStoppedForStandalone() error {
	if m.ctx != nil {
		return fmt.Errorf("controller is running")
	}
	return nil
}
