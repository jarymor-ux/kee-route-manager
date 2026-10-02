package platform

import (
	"context"
	"fmt"
	"os"
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

func (g *generic) EnsureFirewall(ctx context.Context) error {
	c := g.firewallConfig()
	if c.Mode != "managed" {
		return nil
	}
	if err := os.MkdirAll(g.cfg.Paths.RunDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(g.cfg.Paths.RunDir, "krm-firewall.nft")
	checkPath := filepath.Join(g.cfg.Paths.RunDir, "krm-firewall-check.nft")
	if err := os.WriteFile(checkPath, []byte(renderFirewallTable(c, "krm_check")), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(renderFirewallTable(c, "krm")), 0600); err != nil {
		return err
	}
	if _, err := g.r.Run(ctx, []string{"nft", "-c", "-f", checkPath}); err != nil {
		return fmt.Errorf("validate managed nftables rules: %w", err)
	}
	if _, err := g.r.Run(ctx, []string{"nft", "delete", "table", "inet", "krm"}); err != nil && !isMissingRuleError(err) {
		return fmt.Errorf("replace managed nftables table: %w", err)
	}
	if _, err := g.r.Run(ctx, []string{"nft", "-f", path}); err != nil {
		return fmt.Errorf("apply managed nftables rules: %w", err)
	}
	mark, table := strconv.Itoa(c.Mark), strconv.Itoa(c.Table)
	_, _ = g.r.Run(ctx, []string{"ip", "rule", "del", "fwmark", mark, "table", table})
	if _, err := g.r.Run(ctx, []string{"ip", "rule", "add", "fwmark", mark, "table", table}); err != nil {
		_ = g.removeFirewallRules(context.Background(), c)
		return fmt.Errorf("install managed policy rule: %w", err)
	}
	if _, err := g.r.Run(ctx, []string{"ip", "route", "replace", "local", "0.0.0.0/0", "dev", "lo", "table", table}); err != nil {
		_ = g.removeFirewallRules(context.Background(), c)
		return fmt.Errorf("install managed policy route: %w", err)
	}
	return nil
}

func (g *generic) RemoveFirewall(ctx context.Context) error {
	c := g.firewallConfig()
	if c.Mode != "managed" {
		return nil
	}
	return g.removeFirewallRules(ctx, c)
}

func (g *generic) removeFirewallRules(ctx context.Context, c firewallConfig) error {
	mark, table := strconv.Itoa(c.Mark), strconv.Itoa(c.Table)
	var errs []string
	if _, err := g.r.Run(ctx, []string{"ip", "route", "del", "local", "0.0.0.0/0", "dev", "lo", "table", table}); err != nil && !isMissingRuleError(err) {
		errs = append(errs, err.Error())
	}
	if _, err := g.r.Run(ctx, []string{"ip", "rule", "del", "fwmark", mark, "table", table}); err != nil && !isMissingRuleError(err) {
		errs = append(errs, err.Error())
	}
	if _, err := g.r.Run(ctx, []string{"nft", "delete", "table", "inet", "krm"}); err != nil && !isMissingRuleError(err) {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("remove managed firewall: %s", strings.Join(errs, "; "))
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
	return fmt.Sprintf(`table inet %s {
 chain prerouting {
  type filter hook prerouting priority mangle; policy accept;
  meta nfproto != ipv4 return
  iifname != { %s } return
  ip daddr { %s } return
  meta l4proto tcp redirect to :%d
  meta l4proto udp meta mark set %d tproxy to :%d
 }
}
`, tableName, strings.Join(quotedIfaces, ", "), bypass, c.TCPPort, c.Mark, c.UDPPort)
}

func isMissingRuleError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such file") || strings.Contains(text, "no such process") || strings.Contains(text, "not found") || strings.Contains(text, "does not exist")
}
