package amnezia

import (
	"strings"
	"testing"
)

const testAWGConfig = `[Interface]
PrivateKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
Address = 10.66.66.2/32
MTU = 1280
Jc = 5
Jmin = 20
Jmax = 50
S1 = 12
S2 = 12
S3 = 12
S4 = 12
H1 = 101-103
H2 = 201
H3 = 301
H4 = 401
[Peer]
PublicKey = AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=
Endpoint = vpn.example.org:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
`

func TestParseAmneziaWGProfile(t *testing.T) {
	p, err := ParseConfig([]byte(testAWGConfig))
	if err != nil {
		t.Fatal(err)
	}
	if p.Interface.PrivateKey == "" || p.Peer.PublicKey == "" {
		t.Fatal("keys lost")
	}
	if p.Peer.Endpoint != "vpn.example.org:51820" {
		t.Fatalf("wrong endpoint %q", p.Peer.Endpoint)
	}
	if p.Interface.Jc != 5 || p.Interface.Jmin != 20 || p.Interface.Jmax != 50 {
		t.Fatalf("obfuscation lost: %+v", p.Interface)
	}
	if len(p.Peer.AllowedIPs) != 2 || p.Interface.MTU != 1280 {
		t.Fatalf("network fields lost: %+v", p)
	}
	summary := p.Summary()
	if strings.Contains(summary, p.Interface.PrivateKey) || strings.Contains(summary, p.Peer.PublicKey) {
		t.Fatal("public summary leaked credentials")
	}
}

func TestRejectsShellDirectivesAndInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"post-up", strings.Replace(testAWGConfig, "MTU = 1280", "PostUp = touch /tmp/unsafe", 1)},
		{"missing-private-key", strings.ReplaceAll(testAWGConfig, "PrivateKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=", "")},
		{"invalid-address", strings.ReplaceAll(testAWGConfig, "Address = 10.66.66.2/32", "Address = not-an-ip")},
		{"bad-key", strings.ReplaceAll(testAWGConfig, "PublicKey = AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=", "PublicKey = bad")},
		{"bad-endpoint", strings.ReplaceAll(testAWGConfig, "Endpoint = vpn.example.org:51820", "Endpoint = wrong")},
		{"bad-junk-range", strings.ReplaceAll(testAWGConfig, "Jmin = 20", "Jmin = 70")},
		{"duplicate-private-key", strings.Replace(testAWGConfig, "Address = 10.66.66.2/32", "PrivateKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=\nAddress = 10.66.66.2/32", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(tc.text)); err == nil {
				t.Fatal("invalid .conf accepted")
			}
		})
	}
}
