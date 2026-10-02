package platform

import (
	"strings"
	"testing"
)

func TestRenderManagedFirewallIsIPv4ScopedAndDeterministic(t *testing.T) {
	cfg := firewallConfig{Interfaces: []string{"br-lan", "eth0"}, Bypass: []string{"10.0.0.0/8", "192.168.0.0/16"}, TCPPort: 12345, UDPPort: 12346, Mark: 255, Table: 100}
	rules := renderFirewall(cfg)
	for _, expected := range []string{"type nat hook prerouting priority dstnat", "type filter hook prerouting priority mangle", "meta nfproto != ipv4 return", `iifname != { "br-lan", "eth0" } return`, "tcp redirect to :12345", "udp meta mark set 255 tproxy to :12346", "meta mark set 255"} {
		if !strings.Contains(rules, expected) {
			t.Fatalf("missing %q in:\n%s", expected, rules)
		}
	}
	if strings.Contains(rules, "flush ruleset") {
		t.Fatal("must not flush unrelated firewall rules")
	}
}
