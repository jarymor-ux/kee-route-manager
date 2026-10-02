package platform

import (
	"context"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestGenericManagedDirectBypassMarker(t *testing.T) {
	cfg := config.Default()
	cfg.Paths.RunDir = t.TempDir()
	cfg.Platform.Kind = "linux-systemd"
	cfg.Platform.Linux.FirewallMode = "existing"
	g := newGeneric(cfg, Runner{}, "linux-systemd", false, false).(*generic)
	if err := g.EnterDirectBypass(context.Background()); err != ErrDirectBypassUnsupported {
		t.Fatalf("error = %v", err)
	}
}
