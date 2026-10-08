package core

import (
	"errors"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/platform"
)

func TestMetricsHistoryRecordsSuccessfulPollsOnly(t *testing.T) {
	m, _, _ := fixture(t)
	start := time.Now().UTC()
	p := &observerAdapter{metrics: platform.Metrics{UpdatedAt: start, RAMUsedMB: 12}}
	m.platform = p
	m.pollMetrics()
	if got := m.MetricsHistory(); len(got) != 1 || got[0].RAMUsedMB != 12 {
		t.Fatalf("successful poll missing from history: %+v", got)
	}
	p.err = errors.New("sample failed")
	m.pollMetrics()
	if got := m.MetricsHistory(); len(got) != 1 || !m.Metrics().Stale {
		t.Fatal("failed poll became a fresh history sample")
	}
	p.err = nil
	p.metrics.UpdatedAt = start.Add(5 * time.Second)
	p.metrics.RAMUsedMB = 13
	m.pollMetrics()
	if got := m.MetricsHistory(); len(got) != 2 || got[1].RAMUsedMB != 13 || got[1].Stale {
		t.Fatalf("poll recovery missing from history: %+v", got)
	}
}

func TestMetricsHistoryBoundedAndChronological(t *testing.T) {
	h := &metricsHistory{}
	start := time.Now().UTC()
	for i := 0; i < metricsHistoryLimit+5; i++ {
		h.add(platform.Metrics{UpdatedAt: start.Add(time.Duration(i) * time.Second), RAMUsedMB: int64(i)})
	}
	samples := h.snapshot()
	if len(samples) != metricsHistoryLimit || samples[0].RAMUsedMB != 5 || samples[len(samples)-1].RAMUsedMB != metricsHistoryLimit+4 {
		t.Fatalf("bad history bounds: len=%d first=%d last=%d", len(samples), samples[0].RAMUsedMB, samples[len(samples)-1].RAMUsedMB)
	}
	for i := 1; i < len(samples); i++ {
		if !samples[i].UpdatedAt.After(samples[i-1].UpdatedAt) {
			t.Fatal("history reordered samples")
		}
	}
	for _, sample := range []platform.Metrics{
		{}, {UpdatedAt: start.Add(time.Hour), Stale: true}, {UpdatedAt: start.Add(time.Hour), Error: "failed"},
		{UpdatedAt: samples[len(samples)-1].UpdatedAt}, {UpdatedAt: start},
	} {
		h.add(sample)
	}
	if got := h.snapshot(); len(got) != len(samples) || got[0].UpdatedAt != samples[0].UpdatedAt || got[len(got)-1].UpdatedAt != samples[len(samples)-1].UpdatedAt {
		t.Fatal("invalid or duplicate observations entered history")
	}
}

func TestMetricsHistorySnapshotsOwnMutableValues(t *testing.T) {
	cpu, ram, temperature, connected := 10.0, 20.0, 30.0, true
	sample := platform.Metrics{
		UpdatedAt: time.Now().UTC(), CPUPercent: &cpu, RAMPercent: &ram, TemperatureC: &temperature, WANConnected: &connected,
		Ports: []platform.Port{{ID: "p1", Link: map[string]any{"state": []any{"up"}}, Speed: "1000"}},
	}
	h := &metricsHistory{}
	h.add(sample)
	cpu, ram, temperature, connected = 99, 99, 99, false
	sample.Ports[0].ID = "changed"
	sample.Ports[0].Link.(map[string]any)["state"].([]any)[0] = "down"
	first := h.snapshot()[0]
	if *first.CPUPercent != 10 || *first.RAMPercent != 20 || *first.TemperatureC != 30 || !*first.WANConnected || first.Ports[0].ID != "p1" || first.Ports[0].Link.(map[string]any)["state"].([]any)[0] != "up" {
		t.Fatal("history aliases its input")
	}
	*first.CPUPercent = 88
	*first.WANConnected = false
	first.Ports[0].Link.(map[string]any)["state"].([]any)[0] = "changed"
	second := h.snapshot()[0]
	if *second.CPUPercent != 10 || !*second.WANConnected || second.Ports[0].Link.(map[string]any)["state"].([]any)[0] != "up" {
		t.Fatal("history aliases its output")
	}
	m := &Manager{}
	if samples := m.MetricsHistory(); samples == nil || len(samples) != 0 {
		t.Fatal("empty history must serialize as an empty array")
	}
}
