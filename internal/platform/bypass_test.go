package platform

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeFirewall(t *testing.T) (*generic, string) {
	t.Helper()
	d := t.TempDir()
	bin := filepath.Join(d, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	nft := `#!/bin/sh
printf '%s\n' "$*" >> "$KRM_TEST_ROOT/nft.log"
case "$1" in
 list) if [ -f "$KRM_TEST_ROOT/table" ]; then printf '{}\n'; else printf 'No such file or directory\n' >&2; exit 1; fi ;;
 delete) rm -f "$KRM_TEST_ROOT/table" ;;
 -c) exit 0 ;;
 -f) cp "$2" "$KRM_TEST_ROOT/applied.nft"; touch "$KRM_TEST_ROOT/table" ;;
esac
`
	ip := `#!/bin/sh
printf '%s\n' "$*" >> "$KRM_TEST_ROOT/ip.log"
case "$*" in
 '-j rule show') if [ -f "$KRM_TEST_ROOT/conflict" ]; then printf '[{"table":100,"fwmark":123}]\n'; elif [ -f "$KRM_TEST_ROOT/policy" ]; then printf '[{"table":100,"fwmark":255}]\n'; else printf '[]\n'; fi ;;
 '-j route show table 100') if [ -f "$KRM_TEST_ROOT/route" ]; then printf '[{"type":"local","dst":"default","dev":"lo"}]\n'; else printf '[]\n'; fi ;;
 'rule add fwmark 255 table 100') touch "$KRM_TEST_ROOT/policy" ;;
 'route replace local 0.0.0.0/0 dev lo table 100') touch "$KRM_TEST_ROOT/route" ;;
 'rule del fwmark 255 table 100') rm -f "$KRM_TEST_ROOT/policy" ;;
 'route del local 0.0.0.0/0 dev lo table 100') rm -f "$KRM_TEST_ROOT/route" ;;
 *) exit 2 ;;
esac
`
	for name, script := range map[string]string{"nft": nft, "ip": ip} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("KRM_TEST_ROOT", d)
	c := config.Default()
	c.Paths.RunDir = filepath.Join(d, "run")
	c.Paths.StateDir = filepath.Join(d, "state")
	c.Platform.Linux.FirewallMode = "managed"
	c.Platform.Linux.Mark = 255
	c.Platform.Linux.RouteTable = 100
	return &generic{cfg: c, r: Runner{}, kind: "linux-systemd", managed: true}, d
}
func TestManagedFirewallUsesSingleReplaceTransactionAndBypass(t *testing.T) {
	g, d := fakeFirewall(t)
	ctx := context.Background()
	if err := g.EnsureFirewall(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.EnsureFirewall(ctx); err != nil {
		t.Fatal(err)
	}
	batch, err := os.ReadFile(filepath.Join(d, "applied.nft"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(batch), "delete table inet krm\ntable inet krm") || strings.Contains(string(batch), "flush ruleset") {
		t.Fatalf("replacement is not a scoped single transaction: %s", batch)
	}
	if err = g.EnterDirectBypass(ctx); err != nil {
		t.Fatal(err)
	}
	if active, err := g.DirectBypassActive(ctx); err != nil || !active {
		t.Fatal("bypass not active")
	}
	if err = g.EnsureFirewall(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(d, "table")); !os.IsNotExist(err) {
		t.Fatal("periodic firewall reconciliation undid direct bypass")
	}
	if err = g.LeaveDirectBypass(ctx); err != nil {
		t.Fatal(err)
	}
	if active, err := g.DirectBypassActive(ctx); err != nil || active {
		t.Fatal("bypass not left")
	}
	log, _ := os.ReadFile(filepath.Join(d, "nft.log"))
	if strings.Count(string(log), "delete table inet krm") != 1 {
		t.Fatalf("nft replacement used separate destructive delete command: %s", log)
	}
	if err = g.RemoveFirewall(ctx); err != nil {
		t.Fatal(err)
	}
	if err = g.EnsureFirewall(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestFirewallRefusesUnownedTableAndPolicyConflict(t *testing.T) {
	for _, name := range []string{"table", "conflict"} {
		t.Run(name, func(t *testing.T) {
			g, d := fakeFirewall(t)
			if err := os.WriteFile(filepath.Join(d, name), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := g.EnsureFirewall(context.Background()); err == nil {
				t.Fatal("foreign firewall resources overwritten")
			}
			if _, err := os.Stat(filepath.Join(d, "applied.nft")); !os.IsNotExist(err) {
				t.Fatal("firewall mutated despite conflict")
			}
		})
	}
}
func TestKeeneticAdvertisesNoUntestedBypass(t *testing.T) {
	p := newKeenetic(config.Default(), Runner{})
	if p.Capabilities().DirectBypass {
		t.Fatal("untested Keenetic bypass advertised")
	}
	if err := p.EnterDirectBypass(context.Background()); err == nil {
		t.Fatal("unsupported bypass reported success")
	}
}
