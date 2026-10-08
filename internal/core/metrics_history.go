package core

import "github.com/jarymor-ux/kee-route-manager/internal/platform"

const metricsHistoryLimit = 720

// metricsHistory is protected by the manager mutex, alongside the live snapshot.
// Samples stay in memory: polling never adds writes to router flash.
type metricsHistory struct {
	samples [metricsHistoryLimit]platform.Metrics
	next    int
	size    int
}

func (h *metricsHistory) add(sample platform.Metrics) {
	if sample.Stale || sample.Error != "" || sample.UpdatedAt.IsZero() {
		return
	}
	if h.size > 0 {
		last := (h.next + metricsHistoryLimit - 1) % metricsHistoryLimit
		if !sample.UpdatedAt.After(h.samples[last].UpdatedAt) {
			return
		}
	}
	h.samples[h.next] = cloneMetrics(sample)
	h.next = (h.next + 1) % metricsHistoryLimit
	if h.size < metricsHistoryLimit {
		h.size++
	}
}

func (h *metricsHistory) snapshot() []platform.Metrics {
	if h == nil {
		return []platform.Metrics{}
	}
	result := make([]platform.Metrics, h.size)
	start := (h.next + metricsHistoryLimit - h.size) % metricsHistoryLimit
	for i := range result {
		result[i] = cloneMetrics(h.samples[(start+i)%metricsHistoryLimit])
	}
	return result
}

func (m *Manager) MetricsHistory() []platform.Metrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metricHistory.snapshot()
}

func cloneMetrics(sample platform.Metrics) platform.Metrics {
	cloneFloat := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		copy := *value
		return &copy
	}
	sample.CPUPercent = cloneFloat(sample.CPUPercent)
	sample.RAMPercent = cloneFloat(sample.RAMPercent)
	sample.TemperatureC = cloneFloat(sample.TemperatureC)
	if sample.WANConnected != nil {
		value := *sample.WANConnected
		sample.WANConnected = &value
	}
	if sample.Ports != nil {
		ports := make([]platform.Port, len(sample.Ports))
		for i, port := range sample.Ports {
			port.Link = cloneMetricValue(port.Link)
			port.Speed = cloneMetricValue(port.Speed)
			ports[i] = port
		}
		sample.Ports = ports
	}
	return sample
}

func cloneMetricValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(value))
		for key, item := range value {
			copy[key] = cloneMetricValue(item)
		}
		return copy
	case []any:
		copy := make([]any, len(value))
		for i, item := range value {
			copy[i] = cloneMetricValue(item)
		}
		return copy
	default:
		return value
	}
}
