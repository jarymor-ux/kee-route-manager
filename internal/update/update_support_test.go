package update

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type forbiddenUpdateTransport struct{ calls int }

func (r *forbiddenUpdateTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return nil, fmt.Errorf("network must not be reached")
}

func TestManagedFirewallStageRefusesBeforeNetworkOrFilesystem(t *testing.T) {
	for _, kind := range []string{"linux-systemd", "openwrt"} {
		t.Run(kind, func(t *testing.T) {
			f := newStagingFixture(t)
			c := config.Default()
			c.Platform.Kind = kind
			c.Platform.Linux.FirewallMode, c.Platform.OpenWrt.FirewallMode = "managed", "managed"
			c.Update = f.u.cfg
			u := NewForConfig(c, f.u.current)
			u.client = f.u.client
			checked, err := u.Check(context.Background())
			if err != nil || !checked.Available || checked.StageSupported || len(checked.Assets) != 3 {
				t.Fatalf("signed managed discovery: %+v %v", checked, err)
			}
			transport := &forbiddenUpdateTransport{}
			u.client = &http.Client{Transport: transport}
			if _, err = u.Stage(context.Background()); err == nil || !strings.Contains(err.Error(), "managed firewall") {
				t.Fatalf("managed stage not rejected: %v", err)
			}
			if transport.calls != 0 {
				t.Fatal("unsupported staging reached network")
			}
			if _, err = os.Lstat(c.Update.InstallDir); !os.IsNotExist(err) {
				t.Fatal("unsupported staging created installation state")
			}
		})
	}
}
