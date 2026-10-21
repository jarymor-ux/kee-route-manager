package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControllerRejectsRemovedRouterTargets(t *testing.T) {
	for _, kind := range []string{"openwrt", "linux-systemd", "netcraze", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			body := strings.Replace(validYAML, "kind: keenetic", "kind: "+kind, 1)
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("removed target %q accepted", kind)
			}
		})
	}
}

func TestLegacyPlatformSettingsAreRejected(t *testing.T) {
	for _, field := range []string{"linux", "openwrt"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		body := strings.Replace(validYAML, "kind: keenetic", "kind: keenetic\n  "+field+":\n    firewall_mode: managed", 1)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("removed settings %q silently accepted", field)
		}
	}
}
