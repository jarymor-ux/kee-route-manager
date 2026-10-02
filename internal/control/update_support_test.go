package control

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestManagedFirewallTrialRefusesBeforeReadinessOrEffects(t *testing.T) {
	for _, kind := range []string{"linux-systemd", "openwrt"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _ := trialFixture(t)
			if err := os.MkdirAll(c.Xray.ConfigDir, 0700); err != nil {
				t.Fatal(err)
			}
			c.Platform.Kind = kind
			c.Platform.Linux.FirewallMode, c.Platform.OpenWrt.FirewallMode = "managed", "managed"
			before := protectedTrialTree(t, c)
			if err := trialReady(context.Background(), c, "fixture"); err == nil || !strings.Contains(err.Error(), "managed firewall") {
				t.Fatalf("managed trial was not refused: %v", err)
			}
			if after := protectedTrialTree(t, c); !reflect.DeepEqual(before, after) {
				t.Fatal("refused trial changed controller data")
			}
		})
	}
}
