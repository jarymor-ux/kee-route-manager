package amnezia

import (
	"strings"
	"testing"
)

func TestAWG31BooleanOptionsPreserveOfficialOnOffSpelling(t *testing.T) {
	raw := strings.Replace(testAWGConfig, "[Peer]", "[Device]\nRandomTrailers = on\nDisableCookies = off\n[Peer]", 1)
	profile, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("official AWG 3.1 config rejected: %v", err)
	}
	if profile.Interface.Additional["randomtrailers"] != "on" || profile.Interface.Additional["disablecookies"] != "off" {
		t.Fatalf("AWG 3.1 booleans changed: %#v", profile.Interface.Additional)
	}
	safe, err := isolatedConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(safe), "RandomTrailers = on") || !strings.Contains(string(safe), "DisableCookies = off") {
		t.Fatal("AWG 3.1 settings were dropped from runtime config")
	}
	for _, bad := range []string{"true", "false", "yes", "1", "$(id)"} {
		invalid := strings.Replace(raw, "RandomTrailers = on", "RandomTrailers = "+bad, 1)
		if _, err := ParseConfig([]byte(invalid)); err == nil {
			t.Fatalf("invalid AWG 3.1 boolean %q accepted", bad)
		}
	}
}
