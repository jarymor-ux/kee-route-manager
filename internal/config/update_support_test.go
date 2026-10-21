package config

import "testing"

func TestUpdateEligibilityRequiresKeeneticFamily(t *testing.T) {
	for _, kind := range []string{"keenetic", "linux-systemd", "openwrt", "unrecognized", "auto"} {
		c := Default()
		c.Platform.Kind = kind
		want := kind == "keenetic" || kind == "auto" && DetectPlatform() == "keenetic"
		if err := c.UpdateApplySupport(); (err == nil) != want {
			t.Fatalf("platform %q support=%v want=%v", kind, err, want)
		}
	}
}
