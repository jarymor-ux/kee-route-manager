package config

import "testing"

func TestUpdateEligibilityUsesOnlyTheSelectedPlatformFirewall(t *testing.T) {
	for _, kind := range []string{"keenetic", "linux-systemd", "openwrt"} {
		t.Run(kind, func(t *testing.T) {
			c := Default()
			c.Platform.Kind = kind
			c.Platform.Linux.FirewallMode, c.Platform.OpenWrt.FirewallMode = "managed", "managed"
			if kind == "keenetic" {
				if err := c.UpdateApplySupport(); err != nil {
					t.Fatalf("irrelevant generic firewall settings disabled Keenetic: %v", err)
				}
				return
			}
			if err := c.UpdateApplySupport(); err == nil {
				t.Fatal("managed platform declared eligible")
			}
			if kind == "linux-systemd" {
				c.Platform.Linux.FirewallMode = "existing"
			} else {
				c.Platform.OpenWrt.FirewallMode = "existing"
			}
			if err := c.UpdateApplySupport(); err != nil {
				t.Fatalf("inactive platform settings disabled existing mode: %v", err)
			}
		})
	}
}

func TestAutomaticUpdateEligibilityFailsClosedOnUnknownPlatform(t *testing.T) {
	c := Default()
	c.Platform.Kind = "unrecognized"
	if err := c.UpdateApplySupport(); err == nil {
		t.Fatal("unknown platform declared eligible")
	}
	c.Platform.Kind = "auto"
	auto := c.UpdateApplySupport()
	detected := DetectPlatform()
	switch detected {
	case "keenetic", "linux-systemd", "openwrt":
		if auto != nil {
			t.Fatalf("automatic existing-mode platform %s rejected: %v", detected, auto)
		}
	default:
		if auto == nil {
			t.Fatal("automatic unresolved platform declared eligible")
		}
	}
	// Every detectable generic platform is managed here: automatic resolution
	// must not fall back to the unrelated existing-mode defaults.
	c.Platform.Linux.FirewallMode, c.Platform.OpenWrt.FirewallMode = "managed", "managed"
	if err := c.UpdateApplySupport(); detected != "keenetic" && err == nil {
		t.Fatal("automatic detection bypassed managed/unknown eligibility")
	}
}
