package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validConfig(t *testing.T) Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte(validYAML), 0600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestRejectDangerousConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"zero health interval", func(c *Config) { c.Health.Interval = Dur(0) }},
		{"negative timeout", func(c *Config) { c.Health.RequestTimeout = Dur(-time.Second) }},
		{"remote Xray API", func(c *Config) { c.Xray.APIAddress = "0.0.0.0:10085" }},
		{"managed path escape", func(c *Config) { c.Xray.ManagedDir = "/tmp/outside" }},
		{"routing path escape", func(c *Config) { c.Xray.BaseRoutingFile = "/tmp/route.json" }},
		{"probe health collision", func(c *Config) { c.Xray.HealthProxyPort = c.Xray.ProbePortStart }},
		{"oversized workers", func(c *Config) { c.Benchmark.LatencyWorkers = 10000 }},
		{"oversized response", func(c *Config) { c.Health.MaxResponseBytes = 1 << 40 }},
		{"remote plaintext web", func(c *Config) { c.Web.Listen = "0.0.0.0:9444" }},
		{"auto apply", func(c *Config) { c.Update.AutoApply = true }},
		{"tag collision", func(c *Config) { c.Xray.ManagedDirectTag = c.Xray.APITag }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig(t)
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}
func TestSanitizedDoesNotMutateOriginal(t *testing.T) {
	c := validConfig(t)
	c.Subscriptions.Sources[0].Headers = map[string]string{"Authorization": "secret"}
	safe := c.Sanitized()
	if safe.Subscriptions.Sources[0].URL != "<redacted>" || safe.Subscriptions.Sources[0].Headers != nil {
		t.Fatal("secret retained")
	}
	if c.Subscriptions.Sources[0].URL == "<redacted>" || c.Subscriptions.Sources[0].Headers["Authorization"] != "secret" {
		t.Fatal("sanitization mutated active config")
	}
}
func TestRejectNonFiniteByteSize(t *testing.T) {
	for _, v := range []string{"NaN", "Inf", "-Inf", "9223372036854775808"} {
		if _, err := ParseByteSize(v); err == nil {
			t.Errorf("%s accepted", v)
		}
	}
}
func TestUIConfigRequiresTrustedUpstream(t *testing.T) {
	c := Default()
	c.Instance.Role = "ui"
	c.UIProxy.Upstream = "http://router.example:9443"
	if err := c.Validate(); err == nil {
		t.Fatal("remote plaintext upstream accepted")
	}
	c.UIProxy.Upstream = "https://router.example:9443"
	c.UIProxy.UpstreamSPKISHA256 = "invalid"
	if err := c.Validate(); err == nil {
		t.Fatal("invalid pin accepted")
	}
}
func TestUpdateRejectsMutableLatest(t *testing.T) {
	c := validConfig(t)
	c.Update.Enabled = true
	c.Update.PublicKey = strings.Repeat("A", 43)
	c.Update.ManifestURL = "https://github.com/o/r/releases/latest/download/manifest-rc.json"
	c.Update.SignatureURL = c.Update.ManifestURL + ".sig"
	if err := c.Validate(); err == nil {
		t.Fatal("latest release channel accepted")
	}
}

func TestManagedSymlinkEscapeRejected(t *testing.T) {
	c := validConfig(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "managed")); err != nil {
		t.Fatal(err)
	}
	c.Xray.ConfigDir = root
	c.Xray.ManagedDir = filepath.Join(root, "managed")
	c.Xray.BaseRoutingFile = ""
	if err := c.Validate(); err == nil {
		t.Fatal("managed symlink escape accepted")
	}
}
func TestCanonicalRC2RolesLoad(t *testing.T) {
	for _, body := range []string{
		validYAML + "\napi:\n  enabled: true\n  listen: \"127.0.0.1:9443\"\n  unix_socket: run/control.sock\n",
		"instance:\n  role: ui\nweb:\n  enabled: true\n  listen: \"127.0.0.1:9444\"\n  tls:\n    enabled: false\nui:\n  enabled: true\n  upstream: \"https://127.0.0.1:9443\"\n",
	} {
		p := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(p, []byte(body), 0600)
		if _, err := Load(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadRelativeConfigResolvesAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte(validYAML), 0600)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, p)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(rel)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(c.Paths.StateDir) || !filepath.IsAbs(c.API.UnixSocket) {
		t.Fatal("relative path retained")
	}
}

func TestControllerAuthValidatedWithoutEmbeddedWeb(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, tc := range []struct {
			name   string
			mutate func(*Config)
		}{
			{"missing credentials", func(c *Config) { c.Web.CredentialsFile = "" }},
			{"zero session TTL", func(c *Config) { c.Web.SessionTTL = Dur(0) }},
			{"negative session TTL", func(c *Config) { c.Web.SessionTTL = Dur(-time.Second) }},
			{"short session TTL", func(c *Config) { c.Web.SessionTTL = Dur(time.Minute) }},
			{"oversized session TTL", func(c *Config) { c.Web.SessionTTL = Dur(31 * 24 * time.Hour) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c := validConfig(t)
				c.Web.Enabled = false
				c.API.Enabled = enabled
				tc.mutate(&c)
				if err := c.Validate(); err == nil {
					t.Fatal("invalid controller auth accepted")
				}
			})
		}
	}
}
func TestMinimalUIDefaultsHaveSeparateNamespace(t *testing.T) {
	for _, kind := range []string{"linux-systemd", "keenetic"} {
		body := "instance:\n  role: ui\nplatform:\n  kind: " + kind + "\nui:\n  upstream: https://127.0.0.1:9443\n"
		p := filepath.Join(t.TempDir(), "ui.yaml")
		os.WriteFile(p, []byte(body), 0600)
		c, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{"state": c.Paths.StateDir, "cache": c.Paths.CacheDir, "run": c.Paths.RunDir, "log": c.Paths.LogFile, "cert": c.Web.TLS.CertFile, "key": c.Web.TLS.KeyFile} {
			if !strings.Contains(value, "kee-route-manager-ui") {
				t.Errorf("%s uses core namespace: %s", name, value)
			}
		}
		if c.Web.CredentialsFile != "" {
			t.Fatal("UI carries core credentials path")
		}
	}
}
func TestUICustomPathsPreserved(t *testing.T) {
	c := Default()
	c.Instance.Role = "ui"
	c.Paths.StateDir = "/custom/state"
	c.Paths.LogFile = "/custom/log"
	c.Web.TLS.CertFile = "/custom/cert"
	c.Web.TLS.KeyFile = "/custom/key"
	c.ApplyPlatformDefaults()
	if c.Paths.StateDir != "/custom/state" || c.Paths.LogFile != "/custom/log" || c.Web.TLS.CertFile != "/custom/cert" || c.Web.TLS.KeyFile != "/custom/key" {
		t.Fatal("custom UI paths replaced")
	}
}
