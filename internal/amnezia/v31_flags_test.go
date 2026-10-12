package amnezia

import (
	"strings"
	"testing"
)

func TestAWG31RandomTrailersAndCookieMode(t *testing.T) {
	raw := strings.Replace(testAWGConfig, "[Peer]", "[Device]\nHeaderProtectionKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=\nRandomTrailers = true\nDisableCookies = false\n[Peer]", 1)
	if _, err := ParseConfig([]byte(raw)); err != nil {
		t.Fatalf("valid AWG3.1 flags rejected: %v", err)
	}
	for _, flag := range []string{"yes", "$(touch /tmp/file)", "1;uname"} {
		modified := strings.Replace(raw, "RandomTrailers = true", "RandomTrailers = "+flag, 1)
		if _, err := ParseConfig([]byte(modified)); err == nil {
			t.Fatalf("unsafe flag %q accepted", flag)
		}
	}
}
