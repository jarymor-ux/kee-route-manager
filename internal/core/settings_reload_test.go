package core

import (
	"context"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestSettingsReloadDoesNotStartExtraBenchmark(t *testing.T) {
	m, _, _ := fixture(t)
	started := make(chan struct{})
	m.bench = fakeBenchmark{start: started, release: make(chan struct{})}
	m.cfg.Subscriptions.CacheEnabled = false
	m.cfg.Failover.DetectionInterval = config.Dur(time.Hour)
	p := &observerAdapter{fakeAdapter: fakeAdapter{supportsBypass: true}, polled: make(chan struct{}, 1)}
	m.platform = p
	m.StartReload(context.Background())
	t.Cleanup(m.Stop)
	if err := m.Readiness(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.polled:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not start")
	}
	select {
	case <-started:
		t.Fatal("settings reload queued an extra subscription download")
	case <-time.After(100 * time.Millisecond):
	}
	m.Stop()
	select {
	case <-started:
		t.Fatal("settings reload benchmark survived shutdown")
	default:
	}
}
