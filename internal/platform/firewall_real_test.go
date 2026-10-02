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

// This regression uses the same opt-in disposable NET_ADMIN boundary as the
// lifecycle test. It never runs kernel routing operations by default.
func TestRealNftPolicySelectorDriftPreserved(t *testing.T) {
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
	if err := g.EnsureFirewall(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := g.firewallConfig()
	driftCommand := func(action string) []string {
		return []string{"ip", "rule", action, "priority", "10000", "from", "192.0.2.0/24", "to", "all", "fwmark", "255/0xffffffff", "table", "100"}
	}
	drifted := false
	defer func() {
		recovery, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if drifted {
			_, _ = g.r.Run(recovery, driftCommand("del"))
			_, _ = g.r.Run(recovery, managedPolicyCommand("add", cfg, managedRulePriority))
		}
		_ = g.RemoveFirewall(recovery)
	}()
	if _, err := g.r.Run(ctx, managedPolicyCommand("del", cfg, managedRulePriority)); err != nil {
		t.Fatal(err)
	}
	drifted = true
	if _, err := g.r.Run(ctx, driftCommand("add")); err != nil {
		t.Fatal(err)
	}
	before, err := g.r.Run(ctx, []string{"ip", "-j", "rule", "show"})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []struct {
		name string
		run  func(context.Context) error
	}{{"ensure", g.EnsureFirewall}, {"remove", g.RemoveFirewall}} {
		if err = operation.run(ctx); err == nil {
			t.Fatalf("%s accepted kernel policy selector drift", operation.name)
		}
		after, err := g.r.Run(ctx, []string{"ip", "-j", "rule", "show"})
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("%s changed drifted kernel rule: before=%s after=%s", operation.name, before, after)
		}
		if exists, err := g.tableExists(ctx); err != nil || !exists {
			t.Fatalf("%s removed interception before refusing drift: exists=%v err=%v", operation.name, exists, err)
		}
	}
	if _, err = g.r.Run(ctx, driftCommand("del")); err != nil {
		t.Fatal(err)
	}
	if _, err = g.r.Run(ctx, managedPolicyCommand("add", cfg, managedRulePriority)); err != nil {
		t.Fatal(err)
	}
	drifted = false
	if err = g.RemoveFirewall(ctx); err != nil {
		t.Fatalf("restored exact rule cleanup failed: %v", err)
	}
}
