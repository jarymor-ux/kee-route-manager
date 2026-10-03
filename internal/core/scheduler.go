package core

import (
	"context"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
)

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
