package setup

import (
	"fmt"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type Platform string

const (
	PlatformKeenetic     Platform = "keenetic"
	PlatformOpenWrt      Platform = "openwrt"
	PlatformLinuxSystemd Platform = "linux-systemd"
)

type SetupOptions struct {
	Platform      Platform
	Subscriptions []config.Source
	ScoreTargets  []config.Target
	HealthTargets []config.Target
	Xray          XrayOptions
	Pool          PoolOptions
	Benchmark     BenchmarkOptions
	Update        UpdateOptions
}

type XrayOptions struct {
	Binary              string
	AssetDir            string
	ConfigDir           string
	ManagedDir          string
	BaseRoutingFile     string
	InboundTags         []string
	ReplaceOutboundTags []string
}

type PoolOptions struct {
	Size                       int
	ProviderDiversityEnabled   bool
	ProviderDiversityMaxSource int
}

type BenchmarkOptions struct {
	SpeedEnabled        bool
	SpeedWorkers        int
	SpeedURLTemplate    string
	SpeedWarmupBytes    *config.ByteSize
	SpeedMinSampleBytes config.ByteSize
	SpeedMaxSampleBytes config.ByteSize
	SpeedTargetDuration config.Duration
	SpeedRepetitions    int
}

type UpdateOptions struct {
	Enabled           bool
	Channel           string
	GitHubRepository  string
	ManifestURL       string
	SignatureURL      string
	PublicKey         string
	CheckInterval     config.Duration
	AutoApply         bool
	HealthGracePeriod config.Duration
	AllowDowngrade    bool
}

type UIOptions struct {
	Platform           Platform
	Listen             string
	Upstream           string
	UpstreamCAFile     string
	UpstreamSPKISHA256 string
	InsecureTLS        bool
	RequestTimeout     config.Duration
}

func BuildControllerConfig(opts SetupOptions) (config.Config, error) {
	cfg := config.Default()
	cfg.Platform.Kind = string(opts.Platform)
	applyControllerTemplate(&cfg, opts.Platform)
	cfg.ApplyPlatformDefaults()

	cfg.Web.Enabled = false
	cfg.Subscriptions.Sources = cloneSources(opts.Subscriptions)
	cfg.Targets = appendTargets(nil, opts.ScoreTargets, "score")
	cfg.Targets = appendTargets(cfg.Targets, opts.HealthTargets, "health")

	cfg.Xray.Route.InboundTags = append([]string(nil), opts.Xray.InboundTags...)
	cfg.Xray.Route.ReplaceOutboundTags = append([]string(nil), opts.Xray.ReplaceOutboundTags...)
	if opts.Xray.Binary != "" {
		cfg.Xray.Binary = opts.Xray.Binary
	}
	if opts.Xray.AssetDir != "" {
		cfg.Xray.AssetDir = opts.Xray.AssetDir
	}
	if opts.Xray.ConfigDir != "" {
		cfg.Xray.ConfigDir = opts.Xray.ConfigDir
	}
	if opts.Xray.ManagedDir != "" {
		cfg.Xray.ManagedDir = opts.Xray.ManagedDir
	}
	if opts.Xray.BaseRoutingFile != "" {
		cfg.Xray.BaseRoutingFile = opts.Xray.BaseRoutingFile
	}

	cfg.Pool.Size = opts.Pool.Size
	cfg.Pool.ProviderDiversity.Enabled = opts.Pool.ProviderDiversityEnabled
	cfg.Pool.ProviderDiversity.MaxPerProvider = opts.Pool.ProviderDiversityMaxSource
	if !cfg.Pool.ProviderDiversity.Enabled && cfg.Pool.ProviderDiversity.MaxPerProvider == 0 {
		cfg.Pool.ProviderDiversity.MaxPerProvider = 1
	}

	applyBenchmarkOptions(&cfg.Benchmark, opts.Benchmark)
	applyUpdateOptions(&cfg.Update, opts.Update)

	if err := cfg.Validate(); err != nil {
		return config.Config{}, fmt.Errorf("validate generated controller config: %w", err)
	}
	return cfg, nil
}

func BuildUIConfig(opts UIOptions) (config.Config, error) {
	cfg := config.Default()
	cfg.Instance = config.Instance{Name: "Kee Route Manager UI", Role: "ui"}
	cfg.Platform.Kind = string(opts.Platform)
	cfg.ApplyPlatformDefaults()

	cfg.API.Enabled = false
	cfg.Web.Enabled = true
	if opts.Listen != "" {
		cfg.Web.Listen = opts.Listen
	}
	cfg.Web.CredentialsFile = ""
	cfg.Web.TLS.Enabled = true
	cfg.Web.TLS.AutoGenerate = true
	cfg.Web.TLS.Hosts = []string{"localhost"}

	cfg.UIProxy.Enabled = true
	cfg.UIProxy.Upstream = opts.Upstream
	cfg.UIProxy.InsecureTLS = opts.InsecureTLS
	cfg.UIProxy.UpstreamSPKISHA256 = opts.UpstreamSPKISHA256
	if opts.UpstreamCAFile != "" {
		cfg.UIProxy.UpstreamCAFile = opts.UpstreamCAFile
	} else if opts.Platform == PlatformKeenetic {
		cfg.UIProxy.UpstreamCAFile = "/opt/etc/kee-route-manager-ui/controller-ca.crt"
	} else {
		cfg.UIProxy.UpstreamCAFile = "/etc/kee-route-manager-ui/controller-ca.crt"
	}
	if opts.RequestTimeout.Duration > 0 {
		cfg.UIProxy.RequestTimeout = opts.RequestTimeout
	}

	if err := cfg.Validate(); err != nil {
		return config.Config{}, fmt.Errorf("validate generated UI config: %w", err)
	}
	return cfg, nil
}

func applyControllerTemplate(cfg *config.Config, platform Platform) {
	cfg.API.TLS.Hosts = []string{"127.0.0.1", "localhost"}
	cfg.Web.TLS.Hosts = nil
	cfg.Update.CheckInterval = config.Dur(30 * time.Minute)

	switch platform {
	case PlatformKeenetic:
		cfg.Paths.StateDir = "/opt/var/lib/kee-route-manager"
		cfg.Paths.CacheDir = "/opt/var/cache/kee-route-manager"
		cfg.Paths.LogFile = "/opt/var/log/kee-route-manager.log"
		cfg.Paths.RunDir = "/opt/var/run/kee-route-manager"
		cfg.API.TLS.CertFile = "/opt/etc/kee-route-manager/api.crt"
		cfg.API.TLS.KeyFile = "/opt/etc/kee-route-manager/api.key"
		cfg.Web.CredentialsFile = "/opt/etc/kee-route-manager/credentials.json"
		cfg.Web.TLS.CertFile = "/opt/etc/kee-route-manager/tls.crt"
		cfg.Web.TLS.KeyFile = "/opt/etc/kee-route-manager/tls.key"
		cfg.Xray.Binary = "/opt/sbin/xray"
		cfg.Xray.AssetDir = "/opt/share/xray"
		cfg.Xray.ConfigDir = "/opt/etc/xray/configs"
		cfg.Xray.ManagedDir = "/opt/etc/xray/configs"
		cfg.Xray.BaseRoutingFile = "/opt/etc/xray/configs/05_routing.json"
		cfg.Platform.XrayStatusCommand = []string{"/opt/etc/kee-route-manager/xray-status.sh"}
	case PlatformOpenWrt:
		cfg.Platform.OpenWrt.AllowReboot = true
		cfg.Paths.LogFile = "/var/log/kee-route-manager.log"
		cfg.Paths.RunDir = "/var/run/kee-route-manager"
		cfg.API.TLS.CertFile = "/etc/kee-route-manager/api.crt"
		cfg.API.TLS.KeyFile = "/etc/kee-route-manager/api.key"
		cfg.Xray.AssetDir = "/usr/share/xray"
		cfg.Xray.BaseRoutingFile = "/etc/xray/configs/05_routing.json"
	case PlatformLinuxSystemd:
		cfg.Paths.LogFile = "/var/log/kee-route-manager.log"
		cfg.API.TLS.CertFile = "/etc/kee-route-manager/api.crt"
		cfg.API.TLS.KeyFile = "/etc/kee-route-manager/api.key"
		cfg.Xray.AssetDir = "/usr/share/xray"
		cfg.Xray.BaseRoutingFile = "/etc/xray/configs/05_routing.json"
	}
}

func applyBenchmarkOptions(dst *config.Benchmark, src BenchmarkOptions) {
	dst.Speed.Enabled = src.SpeedEnabled
	if src.SpeedWorkers != 0 {
		dst.Speed.Workers = src.SpeedWorkers
	}
	if src.SpeedURLTemplate != "" {
		dst.Speed.URLTemplate = src.SpeedURLTemplate
	}
	if src.SpeedWarmupBytes != nil {
		dst.Speed.WarmupBytes = *src.SpeedWarmupBytes
	}
	if src.SpeedMinSampleBytes != 0 {
		dst.Speed.MinSampleBytes = src.SpeedMinSampleBytes
	}
	if src.SpeedMaxSampleBytes != 0 {
		dst.Speed.MaxSampleBytes = src.SpeedMaxSampleBytes
	}
	if src.SpeedTargetDuration.Duration != 0 {
		dst.Speed.TargetDuration = src.SpeedTargetDuration
	}
	if src.SpeedRepetitions != 0 {
		dst.Speed.Repetitions = src.SpeedRepetitions
	}
}

func applyUpdateOptions(dst *config.Update, src UpdateOptions) {
	dst.Enabled = src.Enabled
	if src.Channel != "" {
		dst.Channel = src.Channel
	}
	dst.GitHubRepository = src.GitHubRepository
	dst.ManifestURL = src.ManifestURL
	dst.SignatureURL = src.SignatureURL
	dst.PublicKey = src.PublicKey
	if src.CheckInterval.Duration != 0 {
		dst.CheckInterval = src.CheckInterval
	}
	dst.AutoApply = src.AutoApply
	if src.HealthGracePeriod.Duration != 0 {
		dst.HealthGracePeriod = src.HealthGracePeriod
	}
	dst.AllowDowngrade = src.AllowDowngrade
}

func cloneSources(in []config.Source) []config.Source {
	out := make([]config.Source, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].Headers != nil {
			out[i].Headers = make(map[string]string, len(in[i].Headers))
			for k, v := range in[i].Headers {
				out[i].Headers[k] = v
			}
		}
	}
	return out
}

func appendTargets(dst []config.Target, in []config.Target, role string) []config.Target {
	for _, target := range in {
		target.Role = role
		dst = append(dst, target)
	}
	return dst
}
