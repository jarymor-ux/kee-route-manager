package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func byteSizePtr(v config.ByteSize) *config.ByteSize { return &v }

func validOptions(platform Platform) SetupOptions {
	return SetupOptions{
		Platform: platform,
		Subscriptions: []config.Source{
			{
				ID:      "primary",
				Name:    "Primary subscription",
				URL:     "https://subscription.example.test/main",
				Enabled: true,
				Headers: map[string]string{"Authorization": "Bearer test"},
			},
		},
		ScoreTargets: []config.Target{
			{
				ID:               "score_primary",
				Name:             "Score target",
				URL:              "https://score.example.test/ping",
				Weight:           1,
				Policy:           "2xx3xx",
				MaxResponseBytes: 64 << 10,
			},
		},
		HealthTargets: []config.Target{
			{
				ID:               "health_primary",
				Name:             "Health target",
				URL:              "https://health.example.test/ping",
				Weight:           1,
				Policy:           "2xx3xx",
				MaxResponseBytes: 64 << 10,
			},
			{
				ID:               "health_secondary",
				Name:             "Independent health target",
				URL:              "https://health2.example.test/ping",
				Weight:           1,
				Policy:           "exact:204",
				MaxResponseBytes: 64 << 10,
			},
		},
		Xray: XrayOptions{
			InboundTags:         []string{"redirect", "tproxy"},
			ReplaceOutboundTags: []string{"vless-reality", "vless-backup"},
		},
		Pool: PoolOptions{Size: 7},
		Benchmark: BenchmarkOptions{
			SpeedEnabled:        true,
			SpeedWorkers:        3,
			SpeedURLTemplate:    "https://speed.example.test/download?bytes={bytes}",
			SpeedWarmupBytes:    byteSizePtr(4 << 20),
			SpeedMinSampleBytes: 32 << 20,
			SpeedMaxSampleBytes: 128 << 20,
			SpeedRepetitions:    2,
		},
		Update: UpdateOptions{
			Enabled:          true,
			Channel:          "rc",
			GitHubRepository: "jarymor-ux/kee-route-manager",
			PublicKey:        "t8ZyoMK5zMz2vTBuWaH8HIwMOo+E1nJXydOak0RWKAE",
			AutoApply:        false,
		},
	}
}

func TestBuildControllerConfigPlatforms(t *testing.T) {
	tests := []struct {
		name     string
		platform Platform
		want     func(config.Config)
	}{
		{
			name:     "keenetic",
			platform: PlatformKeenetic,
			want: func(c config.Config) {
				if c.Paths.StateDir != "/opt/var/lib/kee-route-manager" {
					t.Fatalf("state_dir = %q", c.Paths.StateDir)
				}
				if c.Paths.RunDir != "/opt/var/run/kee-route-manager" {
					t.Fatalf("run_dir = %q", c.Paths.RunDir)
				}
				if c.API.TLS.CertFile != "/opt/etc/kee-route-manager/api.crt" {
					t.Fatalf("api cert = %q", c.API.TLS.CertFile)
				}
				if c.Xray.Binary != "/opt/sbin/xray" || c.Xray.AssetDir != "/opt/share/xray" {
					t.Fatalf("unexpected xray paths: %#v", c.Xray)
				}
				if c.Xray.BaseRoutingFile != "/opt/etc/xray/configs/05_routing.json" {
					t.Fatalf("base routing = %q", c.Xray.BaseRoutingFile)
				}
				if got := strings.Join(c.Platform.XrayStatusCommand, " "); got != "/opt/etc/kee-route-manager/xray-status.sh" {
					t.Fatalf("xray status command = %q", got)
				}
			},
		},
		{
			name:     "openwrt",
			platform: PlatformOpenWrt,
			want: func(c config.Config) {
				if c.Paths.StateDir != "/var/lib/kee-route-manager" {
					t.Fatalf("state_dir = %q", c.Paths.StateDir)
				}
				if c.Paths.RunDir != "/var/run/kee-route-manager" {
					t.Fatalf("run_dir = %q", c.Paths.RunDir)
				}
				if c.API.TLS.CertFile != "/etc/kee-route-manager/api.crt" {
					t.Fatalf("api cert = %q", c.API.TLS.CertFile)
				}
				if c.Xray.AssetDir != "/usr/share/xray" || c.Xray.BaseRoutingFile != "/etc/xray/configs/05_routing.json" {
					t.Fatalf("unexpected xray paths: %#v", c.Xray)
				}
				if !c.Platform.OpenWrt.AllowReboot {
					t.Fatal("openwrt allow_reboot must match shipped template")
				}
			},
		},
		{
			name:     "linux-systemd",
			platform: PlatformLinuxSystemd,
			want: func(c config.Config) {
				if c.Paths.StateDir != "/var/lib/kee-route-manager" {
					t.Fatalf("state_dir = %q", c.Paths.StateDir)
				}
				if c.Paths.RunDir != "/run/kee-route-manager" {
					t.Fatalf("run_dir = %q", c.Paths.RunDir)
				}
				if c.API.TLS.CertFile != "/etc/kee-route-manager/api.crt" {
					t.Fatalf("api cert = %q", c.API.TLS.CertFile)
				}
				if c.Xray.AssetDir != "/usr/share/xray" || c.Xray.BaseRoutingFile != "/etc/xray/configs/05_routing.json" {
					t.Fatalf("unexpected xray paths: %#v", c.Xray)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := BuildControllerConfig(validOptions(tt.platform))
			if err != nil {
				t.Fatalf("BuildControllerConfig: %v", err)
			}
			if cfg.Platform.Kind != string(tt.platform) {
				t.Fatalf("platform kind = %q", cfg.Platform.Kind)
			}
			tt.want(cfg)
			assertGeneratedFields(t, cfg)
			roundTrip(t, cfg)
		})
	}
}

func assertGeneratedFields(t *testing.T, cfg config.Config) {
	t.Helper()
	if len(cfg.Subscriptions.Sources) != 1 || cfg.Subscriptions.Sources[0].ID != "primary" {
		t.Fatalf("subscriptions = %#v", cfg.Subscriptions.Sources)
	}
	score := cfg.TargetsByRole("score")
	if len(score) != 1 || score[0].ID != "score_primary" {
		t.Fatalf("score targets = %#v", score)
	}
	health := cfg.TargetsByRole("health")
	if len(health) != 2 {
		t.Fatalf("health targets = %#v", health)
	}
	if got := strings.Join(cfg.Xray.Route.InboundTags, ","); got != "redirect,tproxy" {
		t.Fatalf("inbound tags = %q", got)
	}
	if got := strings.Join(cfg.Xray.Route.ReplaceOutboundTags, ","); got != "vless-reality,vless-backup" {
		t.Fatalf("outbound tags = %q", got)
	}
	if cfg.Pool.Size != 7 {
		t.Fatalf("pool size = %d", cfg.Pool.Size)
	}
	if !cfg.Benchmark.Speed.Enabled || cfg.Benchmark.Speed.Workers != 3 ||
		cfg.Benchmark.Speed.WarmupBytes != 4<<20 ||
		cfg.Benchmark.Speed.MinSampleBytes != 32<<20 ||
		cfg.Benchmark.Speed.MaxSampleBytes != 128<<20 ||
		cfg.Benchmark.Speed.Repetitions != 2 {
		t.Fatalf("benchmark speed = %#v", cfg.Benchmark.Speed)
	}
	if !cfg.Update.Enabled || cfg.Update.Channel != "rc" ||
		cfg.Update.GitHubRepository != "jarymor-ux/kee-route-manager" ||
		cfg.Update.AutoApply {
		t.Fatalf("update = %#v", cfg.Update)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func roundTrip(t *testing.T, cfg config.Config) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteConfig(path, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatalf("loaded.Validate: %v", err)
	}
	if loaded.Platform.Kind != cfg.Platform.Kind {
		t.Fatalf("round-trip platform = %q, want %q", loaded.Platform.Kind, cfg.Platform.Kind)
	}
}

func TestBuildControllerConfigMultipleSourcesAndHealthTargets(t *testing.T) {
	opts := validOptions(PlatformLinuxSystemd)
	opts.Subscriptions = append(opts.Subscriptions, config.Source{
		ID:      "backup",
		Name:    "Backup subscription",
		URL:     "https://subscription.example.test/backup",
		Enabled: true,
		Headers: map[string]string{"X-Token": "backup"},
	})
	opts.HealthTargets = append(opts.HealthTargets, config.Target{
		ID:               "health_third",
		Name:             "Third health target",
		URL:              "https://health3.example.test/ping",
		Weight:           2,
		Policy:           "2xx3xx",
		MaxResponseBytes: 64 << 10,
	})

	cfg, err := BuildControllerConfig(opts)
	if err != nil {
		t.Fatalf("BuildControllerConfig: %v", err)
	}
	if len(cfg.Subscriptions.Sources) != 2 {
		t.Fatalf("sources = %d", len(cfg.Subscriptions.Sources))
	}
	if got := len(cfg.TargetsByRole("health")); got != 3 {
		t.Fatalf("health targets = %d", got)
	}
	roundTrip(t, cfg)
}

func TestWriteConfigEscapesUserData(t *testing.T) {
	opts := validOptions(PlatformLinuxSystemd)
	special := "name: # ' \" & ?"
	secret := "token: # ' \" & ?"
	headerName := "X:#'\"&?"
	opts.Subscriptions[0].Name = special
	opts.Subscriptions[0].Headers = map[string]string{headerName: secret}
	opts.ScoreTargets[0].Name = special
	opts.HealthTargets[0].Name = special

	cfg, err := BuildControllerConfig(opts)
	if err != nil {
		t.Fatalf("BuildControllerConfig: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteConfig(path, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		data, _ := os.ReadFile(path)
		t.Fatalf("config.Load: %v\n%s", err, data)
	}
	if loaded.Subscriptions.Sources[0].Name != special {
		t.Fatalf("name = %q", loaded.Subscriptions.Sources[0].Name)
	}
	if loaded.Subscriptions.Sources[0].Headers[headerName] != secret {
		t.Fatalf("header did not round-trip: %#v", loaded.Subscriptions.Sources[0].Headers)
	}
}

func TestBuildControllerConfigAllowsExplicitZeroSpeedWarmup(t *testing.T) {
	opts := validOptions(PlatformLinuxSystemd)
	opts.Benchmark.SpeedWarmupBytes = byteSizePtr(0)

	cfg, err := BuildControllerConfig(opts)
	if err != nil {
		t.Fatalf("BuildControllerConfig: %v", err)
	}
	if cfg.Benchmark.Speed.WarmupBytes != 0 {
		t.Fatalf("warmup_bytes = %v, want 0B", cfg.Benchmark.Speed.WarmupBytes)
	}
	roundTrip(t, cfg)
}

func TestInvalidOptionsUseConfigValidation(t *testing.T) {
	opts := validOptions(PlatformLinuxSystemd)
	opts.Pool.Size = 0

	_, err := BuildControllerConfig(opts)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "pool.size must be 1..20") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAutoApplyIsNeverImplicitlyEnabled(t *testing.T) {
	opts := validOptions(PlatformLinuxSystemd)
	cfg, err := BuildControllerConfig(opts)
	if err != nil {
		t.Fatalf("BuildControllerConfig: %v", err)
	}
	if cfg.Update.AutoApply {
		t.Fatal("update.auto_apply unexpectedly enabled")
	}

	opts.Update.AutoApply = true
	_, err = BuildControllerConfig(opts)
	if err == nil || !strings.Contains(err.Error(), "update.auto_apply is unsupported") {
		t.Fatalf("expected existing config validation error, got %v", err)
	}
}

func TestValidationErrorsDoNotLeakSubscriptionSecrets(t *testing.T) {
	opts := validOptions(PlatformLinuxSystemd)
	secretURL := "https://user:super-secret@example.test/sub"
	secretHeader := "super-secret-header"
	opts.Subscriptions[0].URL = secretURL
	opts.Subscriptions[0].Headers = map[string]string{"Authorization": secretHeader}

	_, err := BuildControllerConfig(opts)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if strings.Contains(err.Error(), secretURL) ||
		strings.Contains(err.Error(), "super-secret") ||
		strings.Contains(err.Error(), secretHeader) {
		t.Fatalf("secret leaked in error: %v", err)
	}
}

func TestBuildUIConfig(t *testing.T) {
	for _, platform := range []Platform{PlatformKeenetic, PlatformOpenWrt, PlatformLinuxSystemd} {
		t.Run(string(platform), func(t *testing.T) {
			cfg, err := BuildUIConfig(UIOptions{
				Platform: platform,
				Listen:   "0.0.0.0:9444",
				Upstream: "https://127.0.0.1:9443",
			})
			if err != nil {
				t.Fatalf("BuildUIConfig: %v", err)
			}
			if cfg.Instance.Role != "ui" || cfg.API.Enabled || !cfg.Web.Enabled || !cfg.UIProxy.Enabled {
				t.Fatalf("unexpected UI config: %#v", cfg)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			roundTrip(t, cfg)
		})
	}
}


func TestBuildUIConfigRejectsUnsupportedPlatform(t *testing.T) {
	_, err := BuildUIConfig(UIOptions{
		Platform: Platform("open-wrt"),
		Upstream: "https://127.0.0.1:9443",
	})
	if err == nil {
		t.Fatal("expected unsupported platform error")
	}
	if !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteUIConfigOmitsControllerOnlySections(t *testing.T) {
	cfg, err := BuildUIConfig(UIOptions{
		Platform: PlatformLinuxSystemd,
		Listen:   "0.0.0.0:9444",
		Upstream: "https://127.0.0.1:9443",
	})
	if err != nil {
		t.Fatalf("BuildUIConfig: %v", err)
	}

	path := filepath.Join(t.TempDir(), "ui.yaml")
	if err := WriteConfig(path, cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	for _, section := range []string{
		"failover:",
		"xray:",
		"subscriptions:",
		"targets:",
		"health:",
		"pool:",
		"benchmark:",
		"update:",
		"xray_restart_command:",
		"xray_status_command:",
		"krm_restart_command:",
		"keenetic:",
		"openwrt:",
		"linux:",
		"credentials_file:",
		"session_ttl:",
		"unix_socket:",
	} {
		if strings.Contains(text, section) {
			t.Fatalf("UI config contains controller-only field %q:\n%s", section, text)
		}
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v\n%s", err, text)
	}
	if loaded.Instance.Role != "ui" || loaded.Platform.Kind != string(PlatformLinuxSystemd) {
		t.Fatalf("loaded UI identity = role %q platform %q", loaded.Instance.Role, loaded.Platform.Kind)
	}
}
