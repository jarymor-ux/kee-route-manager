package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type firewallConfig struct {
	Mode             string
	Interfaces       []string
	TCPPort, UDPPort int
	Mark, Table      int
	Bypass           []string
}

func (g *generic) firewallConfig() firewallConfig {
	if g.kind == "openwrt" {
		v := g.cfg.Platform.OpenWrt
		return firewallConfig{v.FirewallMode, v.LANInterfaces, v.TCPRedirectPort, v.UDPTProxyPort, v.Mark, v.RouteTable, v.BypassCIDRs}
	}
	v := g.cfg.Platform.Linux
	return firewallConfig{v.FirewallMode, v.LANInterfaces, v.TCPRedirectPort, v.UDPTProxyPort, v.Mark, v.RouteTable, v.BypassCIDRs}
}

type firewallOwnership struct {
	Mark     int  `json:"mark"`
	Table    int  `json:"table"`
	Bypass   bool `json:"bypass"`
	Priority int  `json:"priority,omitempty"`
}

func (g *generic) ownershipPath() string {
	return filepath.Join(g.cfg.Paths.StateDir, "firewall-owner.json")
}
func (g *generic) ownership() (firewallOwnership, bool, error) {
	var owner firewallOwnership
	b, err := os.ReadFile(g.ownershipPath())
	if errors.Is(err, os.ErrNotExist) {
		return owner, false, nil
	}
	if err != nil {
		return owner, false, err
	}
	if err = json.Unmarshal(b, &owner); err != nil {
		return owner, false, err
	}
	c := g.firewallConfig()
	if owner.Mark != c.Mark || owner.Table != c.Table {
		return owner, false, fmt.Errorf("firewall ownership differs from configuration")
	}
	return owner, true, nil
}
func (g *generic) saveOwnership(owner firewallOwnership) error {
	if err := os.MkdirAll(g.cfg.Paths.StateDir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(g.cfg.Paths.StateDir, ".firewall-owner-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), g.ownershipPath()); err != nil {
		return err
	}
	dir, err := os.Open(g.cfg.Paths.StateDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (g *generic) tableExists(ctx context.Context) (bool, error) {
	_, err := g.r.Run(ctx, []string{"nft", "list", "table", "inet", "krm"})
	if err == nil {
		return true, nil
	}
	if isMissingRuleError(err) {
		return false, nil
	}
	return false, err
}
func (g *generic) EnsureFirewall(ctx context.Context) error {
	g.firewallMu.Lock()
	defer g.firewallMu.Unlock()
	c := g.firewallConfig()
	if c.Mode != "managed" {
		return nil
	}
	owner, owned, err := g.ownership()
	if err != nil {
		return err
	}
	if owned && owner.Bypass {
		exists, err := g.tableExists(ctx)
		if err != nil {
			return err
		}
		if exists {
			_, err = g.r.Run(ctx, []string{"nft", "delete", "table", "inet", "krm"})
		}
		return err
	}
	return g.ensureFirewall(ctx, c, owner, owned)
}
func (g *generic) ensureFirewall(ctx context.Context, c firewallConfig, owner firewallOwnership, owned bool) error {
	exists, err := g.tableExists(ctx)
	if err != nil {
		return err
	}
	if exists && !owned {
		return fmt.Errorf("refusing to replace an unowned inet krm table")
	}
	policyExists, _, err := g.checkPolicyResources(ctx, c, owner, owned)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(g.cfg.Paths.RunDir, 0700); err != nil {
		return err
	}
	transaction := renderFirewall(c)
	if exists {
		transaction = "delete table inet krm\n" + transaction
	}
	path := filepath.Join(g.cfg.Paths.RunDir, "krm-firewall.nft")
	if err = os.WriteFile(path, []byte(transaction), 0600); err != nil {
		return err
	}
	if _, err = g.r.Run(ctx, []string{"nft", "-c", "-f", path}); err != nil {
		return fmt.Errorf("validate managed nftables transaction: %w", err)
	}
	if err = g.saveOwnership(firewallOwnership{Mark: c.Mark, Table: c.Table, Priority: managedRulePriority}); err != nil {
		return err
	}
	if _, err = g.r.Run(ctx, []string{"ip", "route", "replace", "local", "0.0.0.0/0", "dev", "lo", "table", strconv.Itoa(c.Table)}); err != nil {
		return err
	}
	if !policyExists {
		if _, err = g.r.Run(ctx, managedPolicyCommand("add", c, managedRulePriority)); err != nil {
			return err
		}
	}
	if _, err = g.r.Run(ctx, []string{"nft", "-f", path}); err != nil {
		return fmt.Errorf("apply managed nftables transaction: %w", err)
	}
	return nil
}
func (g *generic) EnterDirectBypass(ctx context.Context) error {
	g.firewallMu.Lock()
	defer g.firewallMu.Unlock()
	c := g.firewallConfig()
	if c.Mode != "managed" {
		return fmt.Errorf("platform direct bypass requires managed firewall mode")
	}
	owner, owned, err := g.ownership()
	if err != nil {
		return err
	}
	exists, err := g.tableExists(ctx)
	if err != nil {
		return err
	}
	if exists && !owned {
		return fmt.Errorf("refusing to bypass an unowned firewall table")
	}
	// Persist desired bypass before deleting interception so restart cannot silently re-enable it.
	if err = g.saveOwnership(firewallOwnership{Mark: c.Mark, Table: c.Table, Bypass: true, Priority: owner.Priority}); err != nil {
		return err
	}
	if exists {
		_, err = g.r.Run(ctx, []string{"nft", "delete", "table", "inet", "krm"})
	}
	return err
}
func (g *generic) LeaveDirectBypass(ctx context.Context) error {
	g.firewallMu.Lock()
	defer g.firewallMu.Unlock()
	c := g.firewallConfig()
	if c.Mode != "managed" {
		return nil
	}
	owner, owned, err := g.ownership()
	if err != nil {
		return err
	}
	return g.ensureFirewall(ctx, c, owner, owned)
}
func (g *generic) DirectBypassActive(ctx context.Context) (bool, error) {
	g.firewallMu.Lock()
	defer g.firewallMu.Unlock()
	if g.firewallConfig().Mode != "managed" {
		return false, nil
	}
	owner, owned, err := g.ownership()
	if err != nil || !owned {
		return false, err
	}
	exists, err := g.tableExists(ctx)
	if err != nil {
		return false, err
	}
	return owner.Bypass && !exists, nil
}
func (g *generic) RemoveFirewall(ctx context.Context) error {
	g.firewallMu.Lock()
	defer g.firewallMu.Unlock()
	c := g.firewallConfig()
	if c.Mode != "managed" {
		return nil
	}
	owner, owned, err := g.ownership()
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	policyExists, routeExists, err := g.checkPolicyResources(ctx, c, owner, owned)
	if err != nil {
		return err
	}
	exists, err := g.tableExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		if _, err = g.r.Run(ctx, []string{"nft", "delete", "table", "inet", "krm"}); err != nil {
			return err
		}
	}
	var errs []error
	var commands [][]string
	if routeExists {
		commands = append(commands, []string{"ip", "route", "del", "local", "0.0.0.0/0", "dev", "lo", "table", strconv.Itoa(c.Table)})
	}
	if policyExists {
		commands = append(commands, managedPolicyCommand("del", c, owner.Priority))
	}
	for _, cmd := range commands {
		if _, err = g.r.Run(ctx, cmd); err != nil && !isMissingRuleError(err) {
			errs = append(errs, err)
		}
	}
	if err = errors.Join(errs...); err != nil {
		return err
	}
	if err = os.Remove(g.ownershipPath()); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func renderFirewall(c firewallConfig) string { return renderFirewallTable(c, "krm") }

func renderFirewallTable(c firewallConfig, tableName string) string {
	quotedIfaces := make([]string, 0, len(c.Interfaces))
	for _, iface := range c.Interfaces {
		quotedIfaces = append(quotedIfaces, fmt.Sprintf("%q", iface))
	}
	bypass := strings.Join(c.Bypass, ", ")
	guard := fmt.Sprintf("  meta nfproto != ipv4 return\n  iifname != { %s } return\n  ip daddr { %s } return\n", strings.Join(quotedIfaces, ", "), bypass)
	return fmt.Sprintf(`table inet %s {
 chain tcp_redirect {
  type nat hook prerouting priority dstnat; policy accept;
%s  meta l4proto tcp redirect to :%d
 }
 chain udp_tproxy {
  type filter hook prerouting priority mangle; policy accept;
%s  meta l4proto udp meta mark set %d tproxy to :%d
 }
}
`, tableName, guard, c.TCPPort, guard, c.Mark, c.UDPPort)
}

func isMissingRuleError(err error) bool {
	// Missing binaries and execution failures do not prove a kernel resource is
	// absent. Only a tool that actually ran can report a missing table or rule.
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	if exit.ExitCode() != 1 && exit.ExitCode() != 2 {
		return false
	}
	line := strings.ToLower(strings.SplitN(err.Error(), "\n", 2)[0])
	line = strings.TrimSuffix(line, fmt.Sprintf(": exit status %d", exit.ExitCode()))
	switch line {
	case "nft: no such file or directory",
		"nft: error: no such file or directory",
		"nft: error: could not process rule: no such file or directory",
		"ip: rtnetlink answers: no such file or directory",
		"ip: rtnetlink answers: no such process",
		"ip: error: ipv4: fib table does not exist.":
		return true
	default:
		return false
	}
}
