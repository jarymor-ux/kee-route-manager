package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const SchemaVersion = 1

var idRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

type Config struct {
	SchemaVersion int           `json:"schema_version"`
	Instance      Instance      `json:"instance"`
	Paths         Paths         `json:"paths"`
	API           API           `json:"api"`
	Failover      Failover      `json:"failover"`
	Web           Web           `json:"web"`
	Platform      Platform      `json:"platform"`
	Xray          Xray          `json:"xray"`
	Subscriptions Subscriptions `json:"subscriptions"`
	Targets       []Target      `json:"targets"`
	Health        Health        `json:"health"`
	Pool          Pool          `json:"pool"`
	Benchmark     Benchmark     `json:"benchmark"`
	Update        Update        `json:"update"`
	UIProxy       UIProxy       `json:"ui"`
}
type Instance struct {
	Name string `json:"name"`
	Role string `json:"role"`
}
type Paths struct {
	StateDir string `json:"state_dir"`
	CacheDir string `json:"cache_dir"`
	LogFile  string `json:"log_file"`
	RunDir   string `json:"run_dir"`
}
type API struct {
	Enabled    bool   `json:"enabled"`
	Listen     string `json:"listen"`
	UnixSocket string `json:"unix_socket"`
	TLS        TLS    `json:"tls"`
}
type Failover struct {
	DetectionInterval Duration `json:"detection_interval"`
	FailureThreshold  int      `json:"failure_threshold"`
	ProbeTimeout      Duration `json:"probe_timeout"`
	OverallDeadline   Duration `json:"overall_deadline"`
	Quorum            int      `json:"quorum"`
}
type Web struct {
	Enabled         bool     `json:"enabled"`
	Listen          string   `json:"listen"`
	CredentialsFile string   `json:"credentials_file"`
	SessionTTL      Duration `json:"session_ttl"`
	TLS             TLS      `json:"tls"`
}
type TLS struct {
	Enabled      bool     `json:"enabled"`
	AutoGenerate bool     `json:"auto_generate"`
	CertFile     string   `json:"cert_file"`
	KeyFile      string   `json:"key_file"`
	Hosts        []string `json:"hosts"`
}
type Platform struct {
	Kind               string   `json:"kind"`
	CommandTimeout     Duration `json:"command_timeout"`
	XrayRestartCommand []string `json:"xray_restart_command"`
	XrayStatusCommand  []string `json:"xray_status_command"`
	KRMRestartCommand  []string `json:"krm_restart_command"`
	Keenetic           Keenetic `json:"keenetic"`
	OpenWrt            OpenWrt  `json:"openwrt"`
	Linux              Linux    `json:"linux"`
}
type Keenetic struct {
	RCIBaseURL        string `json:"rci_base_url"`
	NDMCBinary        string `json:"ndmc_binary"`
	XKeenBinary       string `json:"xkeen_binary"`
	XKeenPolicyName   string `json:"xkeen_policy_name"`
	AllowReboot       bool   `json:"allow_reboot"`
	AllowPolicyChange bool   `json:"allow_policy_change"`
}
type OpenWrt struct {
	XrayService     string   `json:"xray_service"`
	AllowReboot     bool     `json:"allow_reboot"`
	FirewallMode    string   `json:"firewall_mode"`
	LANInterfaces   []string `json:"lan_interfaces"`
	TCPRedirectPort int      `json:"tcp_redirect_port"`
	UDPTProxyPort   int      `json:"udp_tproxy_port"`
	Mark            int      `json:"mark"`
	RouteTable      int      `json:"route_table"`
	BypassCIDRs     []string `json:"bypass_cidrs"`
}
type Linux struct {
	XrayService     string   `json:"xray_service"`
	AllowReboot     bool     `json:"allow_reboot"`
	FirewallMode    string   `json:"firewall_mode"`
	LANInterfaces   []string `json:"lan_interfaces"`
	TCPRedirectPort int      `json:"tcp_redirect_port"`
	UDPTProxyPort   int      `json:"udp_tproxy_port"`
	Mark            int      `json:"mark"`
	RouteTable      int      `json:"route_table"`
	BypassCIDRs     []string `json:"bypass_cidrs"`
}
type Xray struct {
	Binary           string    `json:"binary"`
	AssetDir         string    `json:"asset_dir"`
	ConfigDir        string    `json:"config_dir"`
	ManagedDir       string    `json:"managed_dir"`
	BaseRoutingFile  string    `json:"base_routing_file"`
	APIAddress       string    `json:"api_address"`
	APITag           string    `json:"api_tag"`
	BalancerTag      string    `json:"balancer_tag"`
	SlotTagPrefix    string    `json:"slot_tag_prefix"`
	ManagedDirectTag string    `json:"managed_direct_tag"`
	ProbePortStart   int       `json:"probe_port_start"`
	HealthProxyPort  int       `json:"health_proxy_port"`
	DynamicAPI       bool      `json:"dynamic_api"`
	Route            XrayRoute `json:"route"`
}
type XrayRoute struct {
	InboundTags         []string `json:"inbound_tags"`
	ReplaceOutboundTags []string `json:"replace_outbound_tags"`
}
type Subscriptions struct {
	MaxNodesPerSource int      `json:"max_nodes_per_source"`
	MaxSources        int      `json:"max_sources"`
	MaxNodes          int      `json:"max_nodes"`
	CacheTTL          Duration `json:"cache_ttl"`
	RefreshInterval   Duration `json:"refresh_interval"`
	RequestTimeout    Duration `json:"request_timeout"`
	MaxResponseBytes  ByteSize `json:"max_response_bytes"`
	Sources           []Source `json:"sources"`
}
type Source struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Enabled bool              `json:"enabled"`
	Headers map[string]string `json:"headers"`
}
type Target struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	URL              string   `json:"url"`
	Role             string   `json:"role"`
	Weight           int      `json:"weight"`
	Policy           string   `json:"policy"`
	MaxResponseBytes ByteSize `json:"max_response_bytes"`
}
type Health struct {
	Interval             Duration   `json:"interval"`
	FailureThreshold     int        `json:"failure_threshold"`
	RecoveryThreshold    int        `json:"recovery_threshold"`
	RequestTimeout       Duration   `json:"request_timeout"`
	MaxResponseBytes     ByteSize   `json:"max_response_bytes"`
	HotPoolFreshness     Duration   `json:"hot_pool_freshness"`
	ProviderRetryBackoff []Duration `json:"provider_retry_backoff"`
}
type Pool struct {
	Size              int               `json:"size"`
	ProviderDiversity ProviderDiversity `json:"provider_diversity"`
}
type ProviderDiversity struct {
	Enabled        bool `json:"enabled"`
	MaxPerProvider int  `json:"max_per_provider"`
}
type Benchmark struct {
	FullInterval            Duration `json:"full_interval"`
	BatchSize               int      `json:"batch_size"`
	LatencyWorkers          int      `json:"latency_workers"`
	RequestsPerWeight       int      `json:"requests_per_weight"`
	Finalists               int      `json:"finalists"`
	MinImprovementPercent   int      `json:"min_improvement_percent"`
	SwitchCooldown          Duration `json:"switch_cooldown"`
	StabilityBeforeUpgrade  Duration `json:"stability_before_upgrade"`
	TemporaryProxyPortStart int      `json:"temporary_proxy_port_start"`
	TemporaryStartupTimeout Duration `json:"temporary_startup_timeout"`
	Speed                   Speed    `json:"speed"`
}
type Speed struct {
	Enabled        bool     `json:"enabled"`
	URLTemplate    string   `json:"url_template"`
	WarmupBytes    ByteSize `json:"warmup_bytes"`
	MinSampleBytes ByteSize `json:"min_sample_bytes"`
	MaxSampleBytes ByteSize `json:"max_sample_bytes"`
	TargetDuration Duration `json:"target_duration"`
	Repetitions    int      `json:"repetitions"`
}
type Update struct {
	Enabled           bool     `json:"enabled"`
	Channel           string   `json:"channel"`
	ManifestURL       string   `json:"manifest_url"`
	SignatureURL      string   `json:"signature_url"`
	PublicKey         string   `json:"public_key"`
	CheckInterval     Duration `json:"check_interval"`
	AutoApply         bool     `json:"auto_apply"`
	HealthGracePeriod Duration `json:"health_grace_period"`
	AllowDowngrade    bool     `json:"allow_downgrade"`
}
type UIProxy struct {
	Enabled            bool     `json:"enabled"`
	UpstreamCAFile     string   `json:"upstream_ca_file"`
	UpstreamSPKISHA256 string   `json:"upstream_spki_sha256"`
	Upstream           string   `json:"upstream"`
	InsecureTLS        bool     `json:"insecure_tls"`
	RequestTimeout     Duration `json:"request_timeout"`
}

func defaultBypassCIDRs() []string {
	return []string{"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4"}
}

func Default() Config {
	return Config{
		SchemaVersion: 1, Instance: Instance{Name: "Kee Route Manager", Role: "controller"},
		Paths:         Paths{StateDir: "/var/lib/kee-route-manager", CacheDir: "/var/cache/kee-route-manager", LogFile: "/var/log/kee-route-manager/krm.log", RunDir: "/run/kee-route-manager"},
		API:           API{Enabled: true, Listen: "127.0.0.1:9443", TLS: TLS{Enabled: true, AutoGenerate: true, CertFile: "/etc/kee-route-manager/tls.crt", KeyFile: "/etc/kee-route-manager/tls.key"}},
		Failover:      Failover{DetectionInterval: Dur(5 * time.Second), FailureThreshold: 2, ProbeTimeout: Dur(2 * time.Second), OverallDeadline: Dur(5 * time.Second), Quorum: 2},
		Web:           Web{Enabled: true, Listen: "0.0.0.0:9444", CredentialsFile: "/etc/kee-route-manager/credentials.json", SessionTTL: Dur(24 * time.Hour), TLS: TLS{Enabled: true, AutoGenerate: true, CertFile: "/etc/kee-route-manager/tls.crt", KeyFile: "/etc/kee-route-manager/tls.key"}},
		Platform:      Platform{Kind: "auto", CommandTimeout: Dur(30 * time.Second), Keenetic: Keenetic{RCIBaseURL: "http://127.0.0.1:79/rci/", NDMCBinary: "ndmc", XKeenBinary: "/opt/sbin/xkeen", XKeenPolicyName: "XKeen", AllowReboot: true, AllowPolicyChange: true}, OpenWrt: OpenWrt{XrayService: "xray", FirewallMode: "existing", LANInterfaces: []string{"br-lan"}, TCPRedirectPort: 12345, UDPTProxyPort: 12345, Mark: 1, RouteTable: 100, BypassCIDRs: defaultBypassCIDRs()}, Linux: Linux{XrayService: "xray", FirewallMode: "existing", LANInterfaces: []string{"br0"}, TCPRedirectPort: 12345, UDPTProxyPort: 12345, Mark: 1, RouteTable: 100, BypassCIDRs: defaultBypassCIDRs()}},
		Xray:          Xray{Binary: "/usr/bin/xray", ConfigDir: "/etc/xray/configs", ManagedDir: "/etc/xray/configs", APIAddress: "127.0.0.1:10085", APITag: "krm-api", BalancerTag: "krm-main", SlotTagPrefix: "krm-slot-", ManagedDirectTag: "krm-direct", ProbePortStart: 19000, HealthProxyPort: 18999, DynamicAPI: true, Route: XrayRoute{InboundTags: []string{"redirect", "tproxy"}, ReplaceOutboundTags: []string{"vless-reality"}}},
		Subscriptions: Subscriptions{MaxNodesPerSource: 500, MaxSources: 20, MaxNodes: 500, CacheTTL: Dur(7 * 24 * time.Hour), RefreshInterval: Dur(30 * time.Minute), RequestTimeout: Dur(20 * time.Second), MaxResponseBytes: 4 << 20},
		Health:        Health{Interval: Dur(15 * time.Second), FailureThreshold: 2, RecoveryThreshold: 2, RequestTimeout: Dur(8 * time.Second), MaxResponseBytes: 64 << 10, HotPoolFreshness: Dur(5 * time.Minute), ProviderRetryBackoff: []Duration{Dur(15 * time.Second), Dur(30 * time.Second), Dur(time.Minute), Dur(2 * time.Minute), Dur(5 * time.Minute), Dur(10 * time.Minute)}},
		Pool:          Pool{Size: 5},
		Benchmark:     Benchmark{FullInterval: Dur(6 * time.Hour), BatchSize: 20, LatencyWorkers: 8, RequestsPerWeight: 2, Finalists: 6, MinImprovementPercent: 15, SwitchCooldown: Dur(10 * time.Minute), StabilityBeforeUpgrade: Dur(10 * time.Minute), TemporaryProxyPortStart: 20000, TemporaryStartupTimeout: Dur(10 * time.Second), Speed: Speed{Enabled: false, WarmupBytes: 8 << 20, MinSampleBytes: 64 << 20, MaxSampleBytes: 512 << 20, TargetDuration: Dur(6 * time.Second), Repetitions: 3}},
		Update:        Update{Enabled: false, Channel: "rc", CheckInterval: Dur(24 * time.Hour), HealthGracePeriod: Dur(30 * time.Second)},
		UIProxy:       UIProxy{Enabled: true, RequestTimeout: Dur(30 * time.Second)},
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	raw, err := yamlSubsetToJSON(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse YAML: %w", err)
	}
	cfg := Default()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	cfg.ApplyPlatformDefaults()
	cfg.resolve(path)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
func (c *Config) resolve(configPath string) {
	base := filepath.Dir(configPath)
	f := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Clean(filepath.Join(base, p))
	}
	c.Paths.StateDir = f(c.Paths.StateDir)
	c.Paths.CacheDir = f(c.Paths.CacheDir)
	c.Paths.LogFile = f(c.Paths.LogFile)
	c.Paths.RunDir = f(c.Paths.RunDir)
	c.Web.CredentialsFile = f(c.Web.CredentialsFile)
	c.Web.TLS.CertFile = f(c.Web.TLS.CertFile)
	c.Web.TLS.KeyFile = f(c.Web.TLS.KeyFile)
	c.API.UnixSocket = f(c.API.UnixSocket)
	c.API.TLS.CertFile = f(c.API.TLS.CertFile)
	c.API.TLS.KeyFile = f(c.API.TLS.KeyFile)
	c.UIProxy.UpstreamCAFile = f(c.UIProxy.UpstreamCAFile)
	c.Xray.Binary = f(c.Xray.Binary)
	c.Xray.AssetDir = f(c.Xray.AssetDir)
	c.Xray.ConfigDir = f(c.Xray.ConfigDir)
	c.Xray.ManagedDir = f(c.Xray.ManagedDir)
	c.Xray.BaseRoutingFile = f(c.Xray.BaseRoutingFile)
}
func (c *Config) ApplyPlatformDefaults() {
	if c.Instance.Role == "ui-proxy" {
		c.Instance.Role = "ui"
	}

	kind := c.Platform.Kind
	if kind == "auto" {
		kind = DetectPlatform()
		c.Platform.Kind = kind
	}
	if c.Platform.Kind == "keenetic" {
		if c.API.TLS.CertFile == "/etc/kee-route-manager/tls.crt" {
			c.API.TLS.CertFile = "/opt/etc/kee-route-manager/tls.crt"
		}
		if c.API.TLS.KeyFile == "/etc/kee-route-manager/tls.key" {
			c.API.TLS.KeyFile = "/opt/etc/kee-route-manager/tls.key"
		}
	}

	switch kind {
	case "keenetic":
		if c.Paths.StateDir == "/var/lib/kee-route-manager" {
			c.Paths.StateDir = "/opt/var/lib/kee-route-manager"
		}
		if c.Paths.CacheDir == "/var/cache/kee-route-manager" {
			c.Paths.CacheDir = "/opt/var/cache/kee-route-manager"
		}
		if c.Paths.LogFile == "/var/log/kee-route-manager/krm.log" {
			c.Paths.LogFile = "/opt/var/log/kee-route-manager.log"
		}
		if c.Paths.RunDir == "/run/kee-route-manager" {
			c.Paths.RunDir = "/opt/var/run/kee-route-manager"
		}
		if c.Web.CredentialsFile == "/etc/kee-route-manager/credentials.json" {
			c.Web.CredentialsFile = "/opt/etc/kee-route-manager/credentials.json"
		}
		if c.Web.TLS.CertFile == "/etc/kee-route-manager/tls.crt" {
			c.Web.TLS.CertFile = "/opt/etc/kee-route-manager/tls.crt"
		}
		if c.Web.TLS.KeyFile == "/etc/kee-route-manager/tls.key" {
			c.Web.TLS.KeyFile = "/opt/etc/kee-route-manager/tls.key"
		}
		if c.Xray.Binary == "/usr/bin/xray" {
			c.Xray.Binary = "/opt/sbin/xray"
		}
		if c.Xray.ConfigDir == "/etc/xray/configs" {
			c.Xray.ConfigDir = "/opt/etc/xray/configs"
		}
		if c.Xray.ManagedDir == "/etc/xray/configs" {
			c.Xray.ManagedDir = "/opt/etc/xray/configs"
		}
		if len(c.Platform.XrayRestartCommand) == 0 {
			c.Platform.XrayRestartCommand = []string{c.Platform.Keenetic.XKeenBinary, "-restart"}
		}
		if len(c.Platform.XrayStatusCommand) == 0 {
			c.Platform.XrayStatusCommand = []string{c.Platform.Keenetic.XKeenBinary, "-status"}
		}
		if len(c.Platform.KRMRestartCommand) == 0 {
			c.Platform.KRMRestartCommand = []string{"/opt/etc/init.d/S99kee-route-manager", "restart"}
		}
	case "openwrt":
		if len(c.Platform.XrayRestartCommand) == 0 {
			c.Platform.XrayRestartCommand = []string{"/etc/init.d/" + c.Platform.OpenWrt.XrayService, "restart"}
		}
		if len(c.Platform.XrayStatusCommand) == 0 {
			c.Platform.XrayStatusCommand = []string{"/etc/init.d/" + c.Platform.OpenWrt.XrayService, "status"}
		}
		if len(c.Platform.KRMRestartCommand) == 0 {
			c.Platform.KRMRestartCommand = []string{"/etc/init.d/kee-route-manager", "restart"}
		}
	case "linux-systemd":
		if len(c.Platform.XrayRestartCommand) == 0 {
			c.Platform.XrayRestartCommand = []string{"systemctl", "restart", c.Platform.Linux.XrayService}
		}
		if len(c.Platform.XrayStatusCommand) == 0 {
			c.Platform.XrayStatusCommand = []string{"systemctl", "is-active", c.Platform.Linux.XrayService}
		}
		if len(c.Platform.KRMRestartCommand) == 0 {
			c.Platform.KRMRestartCommand = []string{"systemctl", "restart", "kee-route-manager"}
		}
	}
	if c.API.UnixSocket == "" {
		c.API.UnixSocket = filepath.Join(c.Paths.RunDir, "control.sock")
	}
}
func validateManagedFirewall(prefix, mode string, interfaces []string, tcpPort, udpPort, mark, table int, bypass []string) []error {
	if mode != "managed" {
		return nil
	}
	var errs []error
	interfaceRE := regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)
	if len(interfaces) == 0 {
		errs = append(errs, fmt.Errorf("%s.lan_interfaces is required in managed mode", prefix))
	}
	seen := map[string]bool{}
	for _, iface := range interfaces {
		if !interfaceRE.MatchString(iface) {
			errs = append(errs, fmt.Errorf("%s.lan_interfaces contains invalid interface %q", prefix, iface))
		}
		if seen[iface] {
			errs = append(errs, fmt.Errorf("%s.lan_interfaces contains duplicate %q", prefix, iface))
		}
		seen[iface] = true
	}
	for name, port := range map[string]int{"tcp_redirect_port": tcpPort, "udp_tproxy_port": udpPort} {
		if port < 1 || port > 65535 {
			errs = append(errs, fmt.Errorf("%s.%s must be between 1 and 65535", prefix, name))
		}
	}
	if mark < 1 || mark > 1<<30 {
		errs = append(errs, fmt.Errorf("%s.mark must be between 1 and %d", prefix, 1<<30))
	}
	if table < 1 || table > 1<<30 || table >= 253 && table <= 255 {
		errs = append(errs, fmt.Errorf("%s.route_table must be between 1 and %d and must not use reserved tables 253..255", prefix, 1<<30))
	}
	if len(bypass) == 0 {
		errs = append(errs, fmt.Errorf("%s.bypass_cidrs cannot be empty in managed mode", prefix))
	}
	for _, cidr := range bypass {
		ip, _, err := net.ParseCIDR(cidr)
		if err != nil || ip.To4() == nil {
			errs = append(errs, fmt.Errorf("%s.bypass_cidrs contains invalid IPv4 CIDR %q", prefix, cidr))
		}
	}
	return errs
}

func DetectPlatform() string {
	if _, err := os.Stat("/opt/etc/ndm"); err == nil {
		return "keenetic"
	}
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		return "openwrt"
	}
	if runtime.GOOS == "linux" {
		return "linux-systemd"
	}
	return "unknown"
}

func (c Config) Validate() error {
	var es []error
	es = append(es, c.validateCommon()...)
	if c.SchemaVersion != 1 {
		es = append(es, fmt.Errorf("schema_version must be 1"))
	}
	if c.Instance.Role != "controller" && c.Instance.Role != "ui-proxy" && c.Instance.Role != "ui" {
		es = append(es, fmt.Errorf("instance.role must be controller or ui"))
	}
	if c.Web.Enabled {
		if _, _, err := net.SplitHostPort(c.Web.Listen); err != nil {
			es = append(es, fmt.Errorf("web.listen: %w", err))
		}
		if c.Web.CredentialsFile == "" {
			es = append(es, fmt.Errorf("web.credentials_file is required"))
		}
		if c.Web.SessionTTL.Duration < 5*time.Minute {
			es = append(es, fmt.Errorf("web.session_ttl must be at least 5m"))
		}
	}
	if c.Instance.Role == "ui-proxy" || c.Instance.Role == "ui" {
		u, err := url.Parse(c.UIProxy.Upstream)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			es = append(es, fmt.Errorf("ui_proxy.upstream must be a valid http(s) URL"))
		}
		return errors.Join(es...)
	}
	es = append(es, c.validateController()...)
	if c.Platform.Kind != "keenetic" && c.Platform.Kind != "openwrt" && c.Platform.Kind != "linux-systemd" {
		es = append(es, fmt.Errorf("unsupported platform.kind %q", c.Platform.Kind))
	}
	if c.Platform.Kind == "openwrt" {
		v := c.Platform.OpenWrt
		if v.FirewallMode != "existing" && v.FirewallMode != "managed" {
			es = append(es, fmt.Errorf("platform.openwrt.firewall_mode must be existing or managed"))
		}
		es = append(es, validateManagedFirewall("platform.openwrt", v.FirewallMode, v.LANInterfaces, v.TCPRedirectPort, v.UDPTProxyPort, v.Mark, v.RouteTable, v.BypassCIDRs)...)
	}
	if c.Platform.Kind == "linux-systemd" {
		v := c.Platform.Linux
		if v.FirewallMode != "existing" && v.FirewallMode != "managed" {
			es = append(es, fmt.Errorf("platform.linux.firewall_mode must be existing or managed"))
		}
		es = append(es, validateManagedFirewall("platform.linux", v.FirewallMode, v.LANInterfaces, v.TCPRedirectPort, v.UDPTProxyPort, v.Mark, v.RouteTable, v.BypassCIDRs)...)
	}
	if c.Xray.Binary == "" || c.Xray.ConfigDir == "" || c.Xray.ManagedDir == "" {
		es = append(es, fmt.Errorf("xray binary/config_dir/managed_dir are required"))
	}
	if _, _, err := net.SplitHostPort(c.Xray.APIAddress); err != nil {
		es = append(es, fmt.Errorf("xray.api_address: %w", err))
	}
	if c.Pool.Size < 1 || c.Pool.Size > 20 {
		es = append(es, fmt.Errorf("pool.size must be 1..20"))
	}
	if c.Xray.ProbePortStart < 1024 || c.Xray.ProbePortStart+c.Pool.Size >= 65535 {
		es = append(es, fmt.Errorf("xray.probe_port_start invalid"))
	}
	if len(c.Subscriptions.Sources) == 0 {
		es = append(es, fmt.Errorf("at least one subscription source is required"))
	}
	if len(c.Subscriptions.Sources) > c.Subscriptions.MaxSources {
		es = append(es, fmt.Errorf("too many subscription sources"))
	}
	ids := map[string]bool{}
	for i, s := range c.Subscriptions.Sources {
		if !idRE.MatchString(s.ID) {
			es = append(es, fmt.Errorf("subscriptions.sources[%d].id invalid", i))
		}
		if ids[s.ID] {
			es = append(es, fmt.Errorf("duplicate subscription id %q", s.ID))
		}
		ids[s.ID] = true
		if s.Enabled {
			u, err := url.Parse(s.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "file") {
				es = append(es, fmt.Errorf("subscription %q URL invalid", s.ID))
			}
		}
	}
	if c.Subscriptions.MaxNodes < c.Pool.Size || c.Subscriptions.MaxNodes > 5000 {
		es = append(es, fmt.Errorf("subscriptions.max_nodes invalid"))
	}
	tids := map[string]bool{}
	score, health := 0, 0
	for i, t := range c.Targets {
		if !idRE.MatchString(t.ID) {
			es = append(es, fmt.Errorf("targets[%d].id invalid", i))
		}
		if tids[t.ID] {
			es = append(es, fmt.Errorf("duplicate target %q", t.ID))
		}
		tids[t.ID] = true
		u, err := url.Parse(t.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			es = append(es, fmt.Errorf("target %q URL invalid", t.ID))
		}
		if t.Role == "score" {
			score++
		} else if t.Role == "health" {
			health++
		} else {
			es = append(es, fmt.Errorf("target %q role must be score or health", t.ID))
		}
		if t.Weight < 1 || t.Weight > 100 {
			es = append(es, fmt.Errorf("target %q weight invalid", t.ID))
		}
		if t.Policy != "2xx3xx" && !regexp.MustCompile(`^exact:[1-5][0-9]{2}$`).MatchString(t.Policy) {
			es = append(es, fmt.Errorf("target %q policy invalid", t.ID))
		}
	}
	if score == 0 {
		es = append(es, fmt.Errorf("at least one score target is required"))
	}
	if health == 0 {
		es = append(es, fmt.Errorf("at least one health target is required"))
	}
	if c.Health.FailureThreshold < 1 || c.Health.RecoveryThreshold < 1 {
		es = append(es, fmt.Errorf("health thresholds must be positive"))
	}
	if c.Benchmark.BatchSize < 1 || c.Benchmark.BatchSize > 100 {
		es = append(es, fmt.Errorf("benchmark.batch_size invalid"))
	}
	if c.Benchmark.Speed.Enabled {
		if !strings.Contains(c.Benchmark.Speed.URLTemplate, "{bytes}") {
			es = append(es, fmt.Errorf("benchmark.speed.url_template must contain {bytes}"))
		}
		if c.Benchmark.Speed.MinSampleBytes <= 0 || c.Benchmark.Speed.MaxSampleBytes < c.Benchmark.Speed.MinSampleBytes {
			es = append(es, fmt.Errorf("benchmark speed sizes invalid"))
		}
	}
	if c.Update.Enabled {
		if c.Update.Channel != "rc" && c.Update.Channel != "stable" {
			es = append(es, fmt.Errorf("update.channel invalid"))
		}
		if c.Update.ManifestURL == "" || c.Update.SignatureURL == "" || c.Update.PublicKey == "" {
			es = append(es, fmt.Errorf("update URLs and public_key required"))
		}
	}
	return errors.Join(es...)
}
func (c Config) TargetsByRole(role string) []Target {
	out := []Target{}
	for _, t := range c.Targets {
		if t.Role == role {
			out = append(out, t)
		}
	}
	return out
}
func (c Config) Sanitized() Config {
	v := c
	v.Subscriptions.Sources = append([]Source(nil), c.Subscriptions.Sources...)
	for i := range v.Subscriptions.Sources {
		v.Subscriptions.Sources[i].URL = "<redacted>"
		v.Subscriptions.Sources[i].Headers = nil
	}
	return v
}
