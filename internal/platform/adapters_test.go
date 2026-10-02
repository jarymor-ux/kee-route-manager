package platform

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestAdaptersHonorConfiguredServicesAndCapabilityBoundaries(t *testing.T) {
	for _, kind := range []string{"linux-systemd", "openwrt", "keenetic"} {
		t.Run(kind, func(t *testing.T) {
			c := config.Default()
			c.Platform.Kind = kind
			c.Platform.Linux.FirewallMode, c.Platform.OpenWrt.FirewallMode = "existing", "existing"
			c.Platform.Linux.AllowReboot, c.Platform.OpenWrt.AllowReboot, c.Platform.Keenetic.AllowReboot = false, false, false
			c.Platform.Keenetic.AllowPolicyChange = false
			c.Platform.XrayRestartCommand = []string{"/bin/sh", "-c", "exit 0"}
			c.Platform.KRMRestartCommand = []string{"/bin/sh", "-c", "exit 7"}
			c.Platform.XrayStatusCommand = []string{"/bin/sh", "-c", "exit 0"}
			p, _, err := New(c)
			if err != nil {
				t.Fatal(err)
			}
			if p.Kind() != kind || p.Capabilities().DirectBypass || p.Capabilities().Reboot || p.Capabilities().ClientPolicy {
				t.Fatalf("unsupported capabilities advertised: %+v", p.Capabilities())
			}
			ctx := context.Background()
			if err = p.RestartXray(ctx); err != nil || !p.XrayRunning(ctx) {
				t.Fatalf("configured service commands were ignored: %v", err)
			}
			if err = p.RestartKRM(ctx); err == nil {
				t.Fatal("service failure hidden")
			}
			if err = p.Reboot(ctx); err == nil {
				t.Fatal("disabled reboot accepted")
			}
			if err = p.SetClientPolicy(ctx, "00:11:22:33:44:55", "default"); err == nil {
				t.Fatal("disabled client policy accepted")
			}
			if kind != "keenetic" {
				if _, err = p.Clients(ctx); err == nil {
					t.Fatal("unsupported clients reported success")
				}
				if err = p.Wake(ctx, "00:11:22:33:44:55"); err == nil {
					t.Fatal("unsupported wake reported success")
				}
			}
			for _, lines := range []int{0, 1001} {
				if _, err = p.SystemLogs(ctx, lines); err == nil {
					t.Fatal("out-of-range log request executed")
				}
			}
			if err = p.EnsureFirewall(ctx); err != nil {
				t.Fatalf("existing firewall mode failed: %v", err)
			}
			if err = p.RemoveFirewall(ctx); err != nil {
				t.Fatalf("existing firewall removal should leave operator rules: %v", err)
			}
		})
	}
	c := config.Default()
	c.Platform.Kind = "unknown"
	if _, _, err := New(c); err == nil {
		t.Fatal("unknown platform accepted")
	}
}

func TestKeeneticClientsMergeRegistrationAndInheritedPolicies(t *testing.T) {
	k := newKeenetic(config.Default(), Runner{}).(*keenetic)
	k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/rci/show/ip/hotspot":
			return platformRegressionResponse(`{"host":[{"mac":"AA:BB:CC:DD:EE:01","name":"Offline","registered":true,"access":"deny"},{"mac":"AA:BB:CC:DD:EE:02","name":"Active","registered":true,"active":"yes","access":"permit","ip":"192.0.2.2","interface":{"id":"Bridge0"},"rssi":-50,"rxbytes":"1234","txbytes":5678},{"mac":"aa:bb:cc:dd:ee:03","active":false,"registered":false}]}`), nil
		case "/rci/ip/hotspot":
			return platformRegressionResponse(`{"host":[{"mac":"aa:bb:cc:dd:ee:01","policy":"Policy2","access":"deny"},{"mac":"aa:bb:cc:dd:ee:02","conform":true,"access":"permit"}],"policy":[{"interface":"Bridge0","policy":"Policy1"}]}`), nil
		case "/rci/show/ip/policy":
			return platformRegressionResponse(`{"Policy1":{"description":"VPN"},"Policy2":{"description":"WAN"}}`), nil
		default:
			return nil, fmt.Errorf("unexpected RCI path %s", r.URL.Path)
		}
	})}
	clients, err := k.Clients(context.Background())
	if err != nil || len(clients) != 2 {
		t.Fatalf("clients=%+v err=%v", clients, err)
	}
	active, offline := clients[0], clients[1]
	if active.MAC != "aa:bb:cc:dd:ee:02" || !active.Active || !active.PolicyInherited || active.PolicyID != "Policy1" || active.ConnectionPolicy != "VPN" || active.Access != "permit" || active.RXBytes != 1234 || active.TXBytes != 5678 || active.RSSI != -50 {
		t.Fatalf("live client fields or inherited policy lost: %+v", active)
	}
	if offline.Active || offline.PolicyInherited || offline.PolicyID != "Policy2" || offline.ConnectionPolicy != "WAN" || offline.Access != "deny" {
		t.Fatalf("offline registration or explicit policy lost: %+v", offline)
	}
	log := filepath.Join(t.TempDir(), "commands")
	script := filepath.Join(t.TempDir(), "fake-ndmc")
	if err = os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$2\" >> \"$KRM_WAKE_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KRM_WAKE_LOG", log)
	k.cfg.Platform.Keenetic.NDMCBinary = script
	for _, mac := range []string{"invalid; reboot", "aa:bb:cc:dd:ee:99"} {
		if err = k.Wake(context.Background(), mac); err == nil {
			t.Fatal("invalid or unregistered wake accepted")
		}
	}
	if _, err = os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("rejected wake invoked router command")
	}
	if err = k.Wake(context.Background(), "AA:BB:CC:DD:EE:01"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil || string(data) != "ip hotspot wake aa:bb:cc:dd:ee:01\n" {
		t.Fatalf("incorrect wake command %q: %v", data, err)
	}
}

func TestGenericSystemLogsFallsBackAfterJournalFailure(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{
		"journalctl": "#!/bin/sh\nexit 1\n",
		"logread":    "#!/bin/sh\n[ \"$1\" = -l ] && [ \"$2\" = 12 ] || exit 2\nprintf 'router-log-line\\n'\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	g := newGeneric(config.Default(), Runner{}, "openwrt", false, false)
	logs, err := g.SystemLogs(context.Background(), 12)
	if err != nil || !strings.Contains(logs, "router-log-line") {
		t.Fatalf("log fallback failed: %q %v", logs, err)
	}
}
