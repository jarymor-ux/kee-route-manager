package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateBoundsPortEnumerationForInvalidCounts(t *testing.T) {
	for _, field := range []string{"pool.size", "benchmark.batch_size"} {
		t.Run(field, func(t *testing.T) {
			c := validConfig(t)
			// Large enough to prove rejected counts are not expanded into a
			// port map/error per element, while bounding a failing regression.
			if field == "pool.size" {
				c.Pool.Size = 100000
			} else {
				c.Benchmark.BatchSize = 100000
			}
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("invalid count accepted: %v", err)
			}
			if size := len(err.Error()); size > 8192 {
				t.Fatalf("invalid count expanded into %d bytes of port diagnostics", size)
			}
		})
	}
}

func TestProbeAndTemporaryPortsUseInclusiveUpperBound(t *testing.T) {
	for _, pool := range []int{1, 5, 20} {
		c := validConfig(t)
		c.Pool.Size = pool
		c.Xray.ProbePortStart = 65536 - pool
		if err := c.Validate(); err != nil {
			t.Fatalf("valid pool of %d ending at port65535 rejected: %v", pool, err)
		}
		c.Xray.ProbePortStart++
		if err := c.Validate(); err == nil {
			t.Fatalf("pool of %d exceeding port65535 accepted", pool)
		}
	}
	for _, count := range []int{1, 20, 100} {
		c := validConfig(t)
		c.Benchmark.BatchSize = count
		c.Benchmark.TemporaryProxyPortStart = 65536 - count
		if err := c.Validate(); err != nil {
			t.Fatalf("valid temporary batch ending at65535 rejected: %v", err)
		}
		c.Benchmark.TemporaryProxyPortStart++
		if err := c.Validate(); err == nil {
			t.Fatal("temporary batch exceeding port65535 accepted")
		}
	}
}

func TestControllerRejectsUnsafePathsListenersAndRouteTags(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*Config)
	}{
		{"relative state", "paths.state_dir", func(c *Config) { c.Paths.StateDir = "state" }},
		{"NUL cache", "paths.cache_dir", func(c *Config) { c.Paths.CacheDir += "\x00" }},
		{"relative run", "paths.run_dir", func(c *Config) { c.Paths.RunDir = "run" }},
		{"relative credentials", "web.credentials_file", func(c *Config) { c.Web.CredentialsFile = "credentials.json" }},
		{"relative socket", "api.unix_socket", func(c *Config) { c.API.UnixSocket = "control.sock" }},
		{"socket prefix sibling", "api.unix_socket", func(c *Config) { c.API.UnixSocket = c.Paths.RunDir + "-other/control.sock" }},
		{"socket traversal", "api.unix_socket", func(c *Config) { c.API.UnixSocket = filepath.Join(c.Paths.RunDir, "..", "control.sock") }},
		{"remote API", "api.listen must be loopback", func(c *Config) { c.API.Listen = "192.0.2.1:9443" }},
		{"API hostname", "literal IP", func(c *Config) { c.API.Listen = "localhost:9443" }},
		{"API named port", "numeric port", func(c *Config) { c.API.Listen = "127.0.0.1:https" }},
		{"API zero port", "numeric port", func(c *Config) { c.API.Listen = "127.0.0.1:0" }},
		{"API excessive port", "numeric port", func(c *Config) { c.API.Listen = "127.0.0.1:65536" }},
		{"API malformed address", "address", func(c *Config) { c.API.Listen = "missing-port" }},
		{"API shared TLS file", "distinct cert_file", func(c *Config) { c.API.TLS.KeyFile = c.API.TLS.CertFile }},
		{"Xray config relative", "absolute xray.config_dir", func(c *Config) { c.Xray.ConfigDir = "xray"; c.Xray.ManagedDir = "xray" }},
		{"routing wrong extension", "JSON file directly inside", func(c *Config) { c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "routing.yaml") }},
		{"API and Xray overlap", "overlaps xray.api_address", func(c *Config) { c.API.Listen = c.Xray.APIAddress }},
		{"web and API overlap", "overlaps api.listen", func(c *Config) { c.Web.Listen = c.API.Listen }},
		{"probe and batch overlap", "overlaps xray.probe", func(c *Config) { c.Benchmark.TemporaryProxyPortStart = c.Xray.ProbePortStart }},
		{"health zero port", "xray.health_proxy_port port invalid", func(c *Config) { c.Xray.HealthProxyPort = 0 }},
		{"probe privileged port", "xray.probe_port_start invalid", func(c *Config) { c.Xray.ProbePortStart = 1023 }},
		{"duplicate inbound tags", "xray.route.inbound_tags", func(c *Config) { c.Xray.Route.InboundTags = []string{"redirect", "redirect"} }},
		{"inbound managed tag", "xray.route.inbound_tags", func(c *Config) { c.Xray.Route.InboundTags = []string{c.Xray.APITag} }},
		{"replacement managed tag", "xray.route.replace_outbound_tags", func(c *Config) { c.Xray.Route.ReplaceOutboundTags = []string{c.Xray.ManagedDirectTag} }},
		{"invalid tag characters", "invalid, duplicate, or reserved", func(c *Config) { c.Xray.APITag = "api;untrusted" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig(t)
			tc.change(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	c := validConfig(t)
	c.Paths.RunDir = root
	c.API.UnixSocket = filepath.Join(root, "escape", "not-created", "control.sock")
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "api.unix_socket") {
		t.Fatalf("nonexistent socket under escaping symlink accepted: %v", err)
	}
}

func TestManagedFirewallConfigBoundary(t *testing.T) {
	for _, kind := range []string{"linux-systemd", "openwrt"} {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range []struct {
				name, want string
				change     func(*Linux)
			}{
				{"unknown mode", "firewall_mode", func(f *Linux) { f.FirewallMode = "automatic" }},
				{"no LAN interfaces", "lan_interfaces", func(f *Linux) { f.LANInterfaces = nil }},
				{"interface syntax", "invalid interface", func(f *Linux) { f.LANInterfaces = []string{"br0;flush"} }},
				{"long interface", "invalid interface", func(f *Linux) { f.LANInterfaces = []string{strings.Repeat("x", 16)} }},
				{"duplicate interface", "duplicate", func(f *Linux) { f.LANInterfaces = []string{"br0", "br0"} }},
				{"TCP port zero", "tcp_redirect_port", func(f *Linux) { f.TCPRedirectPort = 0 }},
				{"UDP port oversized", "udp_tproxy_port", func(f *Linux) { f.UDPTProxyPort = 65536 }},
				{"zero mark", ".mark", func(f *Linux) { f.Mark = 0 }},
				{"large mark", ".mark", func(f *Linux) { f.Mark = 1<<30 + 1 }},
				{"zero route table", ".route_table", func(f *Linux) { f.RouteTable = 0 }},
				{"large route table", ".route_table", func(f *Linux) { f.RouteTable = 1<<30 + 1 }},
				{"reserved default table", ".route_table", func(f *Linux) { f.RouteTable = 253 }},
				{"reserved main table", ".route_table", func(f *Linux) { f.RouteTable = 254 }},
				{"reserved local table", ".route_table", func(f *Linux) { f.RouteTable = 255 }},
				{"empty bypasses", "bypass_cidrs", func(f *Linux) { f.BypassCIDRs = nil }},
				{"IPv6 bypass", "invalid IPv4 CIDR", func(f *Linux) { f.BypassCIDRs = []string{"::/0"} }},
				{"invalid bypass", "invalid IPv4 CIDR", func(f *Linux) { f.BypassCIDRs = []string{"10.0.0.0/33"} }},
				{"TCP health collision", "overlaps xray.health_proxy_port", func(f *Linux) { f.TCPRedirectPort = 18999 }},
				{"UDP health collision", "overlaps xray.health_proxy_port", func(f *Linux) { f.UDPTProxyPort = 18999 }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					c := validConfig(t)
					c.Platform.Kind = kind
					f := c.Platform.Linux
					f.FirewallMode = "managed"
					tc.change(&f)
					c.Platform.Linux = f
					c.Platform.OpenWrt = OpenWrt(f)
					err := c.Validate()
					if err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("want %q, got %v", tc.want, err)
					}
				})
			}
			for _, table := range []int{1, 252, 256, 1 << 30} {
				c := validConfig(t)
				c.Platform.Kind = kind
				c.Platform.Linux.FirewallMode, c.Platform.Linux.RouteTable, c.Platform.Linux.Mark = "managed", table, 1<<30
				c.Platform.OpenWrt = OpenWrt(c.Platform.Linux)
				if err := c.Validate(); err != nil {
					t.Fatalf("valid table/mark boundary rejected: %v", err)
				}
			}
		})
	}
}

func TestMonitoringAndBenchmarkResourceBounds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		low, high int
		value     func(*Config) *int
	}{
		{"pool.size", 1, 20, func(c *Config) *int { return &c.Pool.Size }},
		{"benchmark.batch_size", 1, 100, func(c *Config) *int { return &c.Benchmark.BatchSize }},
		{"benchmark.latency_workers", 1, 100, func(c *Config) *int { return &c.Benchmark.LatencyWorkers }},
		{"benchmark.finalists", 1, 100, func(c *Config) *int { return &c.Benchmark.Finalists }},
		{"benchmark.requests_per_weight", 1, 10, func(c *Config) *int { return &c.Benchmark.RequestsPerWeight }},
		{"benchmark.speed.repetitions", 1, 10, func(c *Config) *int { return &c.Benchmark.Speed.Repetitions }},
		{"health.failure_threshold", 1, 100, func(c *Config) *int { return &c.Health.FailureThreshold }},
		{"health.recovery_threshold", 1, 100, func(c *Config) *int { return &c.Health.RecoveryThreshold }},
		{"subscriptions.max_sources", 1, 100, func(c *Config) *int { return &c.Subscriptions.MaxSources }},
		{"subscriptions.max_nodes_per_source", 0, 5000, func(c *Config) *int { return &c.Subscriptions.MaxNodesPerSource }},
		{"benchmark.min_improvement_percent", 0, 100, func(c *Config) *int { return &c.Benchmark.MinImprovementPercent }},
	} {
		for _, value := range []int{tc.low - 1, tc.low, tc.high, tc.high + 1} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, value), func(t *testing.T) {
				c := validConfig(t)
				*tc.value(&c) = value
				err := c.Validate()
				wantValid := value >= tc.low && value <= tc.high
				if (err == nil) != wantValid {
					t.Fatalf("valid=%t, err=%v", wantValid, err)
				}
				if !wantValid && !strings.Contains(err.Error(), tc.name) {
					t.Fatalf("missing actionable field name %q: %v", tc.name, err)
				}
			})
		}
	}
	for _, tc := range []struct {
		name  string
		max   time.Duration
		value func(*Config) *Duration
	}{
		{"platform.command_timeout", 5 * time.Minute, func(c *Config) *Duration { return &c.Platform.CommandTimeout }},
		{"subscriptions.cache_ttl", 30 * 24 * time.Hour, func(c *Config) *Duration { return &c.Subscriptions.CacheTTL }},
		{"subscriptions.refresh_interval", 7 * 24 * time.Hour, func(c *Config) *Duration { return &c.Subscriptions.RefreshInterval }},
		{"subscriptions.request_timeout", 2 * time.Minute, func(c *Config) *Duration { return &c.Subscriptions.RequestTimeout }},
		{"health.interval", time.Hour, func(c *Config) *Duration { return &c.Health.Interval }},
		{"health.request_timeout", time.Minute, func(c *Config) *Duration { return &c.Health.RequestTimeout }},
		{"health.hot_pool_freshness", 24 * time.Hour, func(c *Config) *Duration { return &c.Health.HotPoolFreshness }},
		{"benchmark.full_interval", 30 * 24 * time.Hour, func(c *Config) *Duration { return &c.Benchmark.FullInterval }},
		{"benchmark.temporary_startup_timeout", time.Minute, func(c *Config) *Duration { return &c.Benchmark.TemporaryStartupTimeout }},
		{"benchmark.speed.target_duration", time.Minute, func(c *Config) *Duration { return &c.Benchmark.Speed.TargetDuration }},
	} {
		for _, value := range []time.Duration{0, tc.max, tc.max + time.Nanosecond} {
			t.Run(tc.name+"/"+value.String(), func(t *testing.T) {
				c := validConfig(t)
				*tc.value(&c) = Dur(value)
				err := c.Validate()
				if (err == nil) != (value > 0 && value <= tc.max) {
					t.Fatalf("duration=%v err=%v", value, err)
				}
			})
		}
	}
	for _, tc := range []struct {
		name, want string
		change     func(*Config)
	}{
		{"empty provider retries", "provider_retry_backoff", func(c *Config) { c.Health.ProviderRetryBackoff = nil }},
		{"too many retries", "provider_retry_backoff", func(c *Config) { c.Health.ProviderRetryBackoff = make([]Duration, 17) }},
		{"zero retry delay", "provider_retry_backoff", func(c *Config) { c.Health.ProviderRetryBackoff = []Duration{Dur(0)} }},
		{"failover quorum below two", "failover quorum", func(c *Config) { c.Failover.Quorum = 1 }},
		{"failover threshold zero", "failover quorum/threshold", func(c *Config) { c.Failover.FailureThreshold = 0 }},
		{"probe longer than deadline", "failover quorum/threshold/deadline", func(c *Config) {
			c.Failover.ProbeTimeout = Dur(6 * time.Second)
			c.Failover.OverallDeadline = Dur(5 * time.Second)
		}},
		{"zero sample", "sample sizes", func(c *Config) { c.Benchmark.Speed.MinSampleBytes = 0 }},
		{"max sample below min", "sample sizes", func(c *Config) { c.Benchmark.Speed.MaxSampleBytes = c.Benchmark.Speed.MinSampleBytes - 1 }},
		{"oversized sample", "sample sizes", func(c *Config) { c.Benchmark.Speed.MaxSampleBytes = 1<<30 + 1 }},
		{"negative warmup", "sample sizes", func(c *Config) { c.Benchmark.Speed.WarmupBytes = -1 }},
		{"oversized warmup", "sample sizes", func(c *Config) { c.Benchmark.Speed.WarmupBytes = 1<<30 + 1 }},
		{"plaintext speed", "must use HTTPS", func(c *Config) {
			c.Benchmark.Speed.Enabled = true
			c.Benchmark.Speed.URLTemplate = "http://speed.test/?bytes={bytes}"
		}},
		{"missing size substitution", "must contain {bytes}", func(c *Config) {
			c.Benchmark.Speed.Enabled = true
			c.Benchmark.Speed.URLTemplate = "https://speed.test/"
		}},
		{"diversity zero", "max_per_provider", func(c *Config) { c.Pool.ProviderDiversity.Enabled = true; c.Pool.ProviderDiversity.MaxPerProvider = 0 }},
		{"diversity exceeds pool", "max_per_provider", func(c *Config) {
			c.Pool.ProviderDiversity.Enabled = true
			c.Pool.ProviderDiversity.MaxPerProvider = c.Pool.Size + 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig(t)
			tc.change(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestUITrustAndUpdateConfigurationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*Config)
	}{
		{"disabled UI", "ui.enabled", func(c *Config) { c.UIProxy.Enabled = false }},
		{"disabled web", "web.enabled", func(c *Config) { c.Web.Enabled = false }},
		{"upstream userinfo", "without credentials", func(c *Config) { c.UIProxy.Upstream = "https://admin:secret@router.test" }},
		{"upstream fragment", "without credentials or fragment", func(c *Config) { c.UIProxy.Upstream = "https://router.test/#fragment" }},
		{"upstream query", "origin URL", func(c *Config) { c.UIProxy.Upstream = "https://router.test/?token=secret" }},
		{"upstream path", "origin URL", func(c *Config) { c.UIProxy.Upstream = "https://router.test/api" }},
		{"insecure plus CA", "cannot be combined", func(c *Config) { c.UIProxy.InsecureTLS = true; c.UIProxy.UpstreamCAFile = "/private/ca.pem" }},
		{"insecure plus pin", "cannot be combined", func(c *Config) {
			c.UIProxy.InsecureTLS = true
			c.UIProxy.UpstreamSPKISHA256 = base64.StdEncoding.EncodeToString(make([]byte, 32))
		}},
		{"zero request timeout", "ui.request_timeout", func(c *Config) { c.UIProxy.RequestTimeout = Dur(0) }},
		{"too long request timeout", "ui.request_timeout", func(c *Config) { c.UIProxy.RequestTimeout = Dur(5*time.Minute + time.Nanosecond) }},
		{"remote plaintext listener", "TLS-off web listener must be loopback", func(c *Config) { c.Web.TLS.Enabled = false; c.Web.Listen = "0.0.0.0:9444" }},
		{"missing TLS cert", "distinct cert_file", func(c *Config) { c.Web.TLS.CertFile = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Instance.Role = "ui"
			c.UIProxy.Upstream = "https://router.test:9443"
			tc.change(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	for _, pin := range []string{base64.StdEncoding.EncodeToString(make([]byte, 32)), "sha256/" + base64.StdEncoding.EncodeToString(make([]byte, 32)), strings.Repeat("ab", 32)} {
		c := Default()
		c.Instance.Role = "ui"
		c.UIProxy.Upstream = "https://router.test:9443"
		c.UIProxy.UpstreamSPKISHA256 = pin
		if err := c.Validate(); err != nil {
			t.Fatalf("supported pin encoding rejected: %v", err)
		}
	}
	for _, tc := range []struct {
		name, want string
		change     func(*Config)
	}{
		{"unsupported channel", "update.channel", func(c *Config) { c.Update.Channel = "nightly" }},
		{"repository traversal", "owner/repository", func(c *Config) { c.Update.GitHubRepository = "owner/repository/extra" }},
		{"invalid public key", "Ed25519 public key", func(c *Config) { c.Update.PublicKey = "invalid" }},
		{"no discovery source", "URLs and public_key required", func(c *Config) { c.Update.GitHubRepository = "" }},
		{"plaintext manifest", "HTTPS", func(c *Config) { c.Update.ManifestURL = "http://updates.test/manifest.json" }},
		{"manifest userinfo", "HTTPS", func(c *Config) { c.Update.ManifestURL = "https://admin:secret@updates.test/manifest.json" }},
		{"mutable signature", "/releases/latest", func(c *Config) {
			c.Update.SignatureURL = "https://github.com/owner/repo/releases/latest/download/manifest.sig"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig(t)
			c.Update.Enabled = true
			c.Update.PublicKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
			c.Update.GitHubRepository = "owner/repository"
			tc.change(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		c := validConfig(t)
		c.Update.Enabled = true
		c.Update.PublicKey = enc.EncodeToString([]byte(strings.Repeat("\xfb", 32)))
		c.Update.GitHubRepository = "owner/repository"
		if err := c.Validate(); err != nil {
			t.Fatalf("valid discovery/key rejected: %v", err)
		}
		c.Update.GitHubRepository = ""
		c.Update.ManifestURL = "https://updates.test/v1/manifest.json"
		c.Update.SignatureURL = c.Update.ManifestURL + ".sig"
		if err := c.Validate(); err != nil {
			t.Fatalf("valid explicit signed update source rejected: %v", err)
		}
	}
}
