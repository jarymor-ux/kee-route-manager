package platform

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"os"
	"testing"
	"time"
)

// Run only in an explicitly isolated Linux network namespace/container with
// NET_ADMIN. This test deliberately changes the namespace's nft/ip resources.
func TestRealNftManagedFirewallLifecycle(t *testing.T) {
	if os.Getenv("KRM_TEST_REAL_NFT") != "1" {
		t.Skip("opt-in isolated NET_ADMIN namespace required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := config.Default()
	c.Paths.RunDir = t.TempDir()
	c.Paths.StateDir = t.TempDir()
	c.Platform.Linux.FirewallMode = "managed"
	c.Platform.Linux.LANInterfaces = []string{"eth0"}
	c.Platform.Linux.Mark = 255
	c.Platform.Linux.RouteTable = 100
	g := &generic{cfg: c, r: Runner{}, kind: "linux-systemd", managed: true}
	if exists, err := g.tableExists(ctx); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("test namespace already contains inet krm; refusing to mutate")
	}
	if _, err := g.r.Run(ctx, []string{"nft", "add", "table", "inet", "krm_test_foreign"}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = g.r.Run(context.Background(), []string{"nft", "delete", "table", "inet", "krm_test_foreign"})
	}()
	defer g.RemoveFirewall(context.Background())
	for i := 0; i < 2; i++ {
		if err := g.EnsureFirewall(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.EnterDirectBypass(ctx); err != nil {
		t.Fatal(err)
	}
	if active, err := g.DirectBypassActive(ctx); err != nil || !active {
		t.Fatalf("bypass active=%v err=%v", active, err)
	}
	if err := g.EnsureFirewall(ctx); err != nil {
		t.Fatal(err)
	}
	if active, err := g.DirectBypassActive(ctx); err != nil || !active {
		t.Fatal("reconciliation undid direct bypass")
	}
	if err := g.LeaveDirectBypass(ctx); err != nil {
		t.Fatal(err)
	}
	if active, err := g.DirectBypassActive(ctx); err != nil || active {
		t.Fatal("recovery did not leave bypass")
	}
	if err := g.RemoveFirewall(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := g.r.Run(ctx, []string{"nft", "list", "table", "inet", "krm_test_foreign"}); err != nil {
		t.Fatal("foreign table was removed")
	}
	if err := g.EnsureFirewall(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.RemoveFirewall(ctx); err != nil {
		t.Fatal(err)
	}
}
