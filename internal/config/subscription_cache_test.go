package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionCacheSettingLoadsAndDefaults(t *testing.T) {
	for _, setting := range []string{"", "true", "false"} {
		t.Run("cache="+setting, func(t *testing.T) {
			body := validYAML
			if setting != "" {
				body = strings.Replace(body, "subscriptions:\n", "subscriptions:\n  cache_enabled: "+setting+"\n", 1)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Subscriptions.CacheEnabled != (setting != "false") {
				t.Fatal("cache_enabled was not decoded with its legacy default")
			}
		})
	}
}

func TestSubscriptionCacheDefaultPreservesControllerAndUITemplates(t *testing.T) {
	for _, name := range []string{"keenetic.yaml", "openwrt.yaml", "linux-systemd.yaml", "ui-keenetic.yaml", "ui-linux-openwrt.yaml"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(filepath.Join("..", "..", "configs", name))
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.Subscriptions.CacheEnabled {
				t.Fatal("legacy template unexpectedly disabled subscription caching")
			}
		})
	}
}
