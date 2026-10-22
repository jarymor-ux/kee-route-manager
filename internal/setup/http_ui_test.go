package setup

import "testing"

func TestKeeneticPrivateLANHTTPUI(t *testing.T) {
	ui, err := BuildUIConfig(UIOptions{
		Platform: PlatformKeenetic,
		PlainHTTP: true,
		Listen: "192.168.1.1:9444",
		Upstream: "https://127.0.0.1:9443",
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
	if _, err := BuildUIConfig(UIOptions{Platform: PlatformLinuxSystemd, PlainHTTP: true, Listen: "192.168.1.1:9444"}); err == nil {
		t.Fatal("HTTP LAN listener permitted outside Keenetic")
	}
	httpsUI, err := BuildUIConfig(UIOptions{Platform: PlatformKeenetic, Listen: "192.168.1.1:9444"})
	if err != nil || !httpsUI.Web.TLS.Enabled {
		t.Fatal("explicit HTTPS UI mode unavailable", err)
	}
}
