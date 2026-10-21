package platform

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"testing"
)

func TestOnlyKeeneticFamilyHasPlatformAdapter(t *testing.T) {
	for _, kind := range []string{"openwrt", "linux-systemd", "unknown", "netcraze"} {
		c := config.Default()
		c.Platform.Kind = kind
		if _, _, err := New(c); err == nil {
			t.Fatalf("removed adapter %q available", kind)
		}
	}
	c := config.Default()
	c.Platform.Kind = "keenetic"
	p, _, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind() != "keenetic" || !p.Capabilities().Clients || !p.Capabilities().WakeOnLAN || p.Capabilities().DirectBypass {
		t.Fatalf("Keenetic capabilities changed: %+v", p.Capabilities())
	}
	if err := p.EnsureFirewall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.RemoveFirewall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.EnterDirectBypass(context.Background()); err == nil {
		t.Fatal("unverified bypass advertised")
	}
}
