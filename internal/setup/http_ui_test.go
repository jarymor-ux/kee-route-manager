package setup

import (
	"net"
	"testing"
)

func TestKeeneticPrivateLANHTTPUI(t *testing.T) {
	ui, err := BuildUIConfig(UIOptions{
		Platform:       PlatformKeenetic,
		PlainHTTP:      true,
		Listen:         "192.168.1.1:9444",
		Upstream:       "https://127.0.0.1:9443",
		UpstreamCAFile: managedUIUpstreamCAPath(PlatformKeenetic),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ui.Web.TLS.Enabled || ui.Web.TLS.AutoGenerate || ui.Web.Listen != "192.168.1.1:9444" || ui.UIProxy.Upstream != "https://127.0.0.1:9443" {
		t.Fatalf("unexpected HTTP LAN UI: %#v", ui)
	}
	for _, address := range []string{"0.0.0.0:9444", "8.8.8.8:9444", "100.64.0.1:9444", "[::]:9444"} {
		if _, err := BuildUIConfig(UIOptions{Platform: PlatformKeenetic, PlainHTTP: true, Listen: address}); err == nil {
			t.Fatalf("non-private HTTP listener accepted: %s", address)
		}
	}
	if _, err := BuildUIConfig(UIOptions{Platform: Platform("linux-systemd"), PlainHTTP: true, Listen: "192.168.1.1:9444"}); err == nil {
		t.Fatal("HTTP LAN listener permitted outside Keenetic")
	}
	httpsUI, err := BuildUIConfig(UIOptions{Platform: PlatformKeenetic, Listen: "192.168.1.1:9444"})
	if err != nil || !httpsUI.Web.TLS.Enabled {
		t.Fatal("explicit HTTPS UI mode unavailable", err)
	}
}

func TestKeeneticPanelAutoBindOnlyUniquePrivateBridgeAddress(t *testing.T) {
	for _, tc := range []struct {
		name       string
		interfaces []lanBindCandidate
		want       string
	}{
		{"unique-lan", []lanBindCandidate{{name: "br0", ip: net.ParseIP("192.168.1.1"), up: true}}, "192.168.1.1"},
		{"multiple-bridges", []lanBindCandidate{{name: "br0", ip: net.ParseIP("192.168.1.1"), up: true}, {name: "br1", ip: net.ParseIP("192.168.2.1"), up: true}}, "127.0.0.1"},
		{"public-bridge", []lanBindCandidate{{name: "br0", ip: net.ParseIP("8.8.8.8"), up: true}}, "127.0.0.1"},
		{"down-bridge", []lanBindCandidate{{name: "br0", ip: net.ParseIP("192.168.1.1")}}, "127.0.0.1"},
		{"wan-only", []lanBindCandidate{{name: "eth0", ip: net.ParseIP("192.168.1.1"), up: true}}, "127.0.0.1"},
		{"non-bridge-tunnel", []lanBindCandidate{{name: "tun0", ip: net.ParseIP("10.0.0.1"), up: true}}, "127.0.0.1"},
		{"duplicate-bridge-address", []lanBindCandidate{{name: "br0", ip: net.ParseIP("192.168.1.1"), up: true}, {name: "br1", ip: net.ParseIP("192.168.1.1"), up: true}}, "192.168.1.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectKeeneticLANBind(tc.interfaces); got != tc.want {
				t.Fatalf("bind=%s, want %s", got, tc.want)
			}
		})
	}
}
