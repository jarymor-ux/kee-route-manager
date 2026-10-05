package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// XraySelectionTag owns its entire prefix because balancer selectors use prefix matching.
const XraySelectionTag = "krm-persisted-selection"

var httpHeaderNameRE = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

func loopback(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }
func listenPort(addr string) (string, int, error) {
	host, raw, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("numeric port must be 1..65535")
	}
	if host != "" && net.ParseIP(host) == nil {
		return "", 0, fmt.Errorf("listen address must be a literal IP")
	}
	return host, port, nil
}
func validHTTPURL(raw string, httpsOnly bool) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || !httpsOnly && u.Scheme == "http")
}
func tlsErrors(name string, t TLS) []error {
	if !t.Enabled {
		return nil
	}
	if t.CertFile == "" || t.KeyFile == "" || t.CertFile == t.KeyFile {
		return []error{fmt.Errorf("%s requires distinct cert_file and key_file", name)}
	}
	return nil
}
func durationError(name string, d Duration, max time.Duration) error {
	if d.Duration <= 0 || d.Duration > max {
		return fmt.Errorf("%s must be positive and <= %s", name, max)
	}
	return nil
}
func (c Config) validateCommon() []error {
	var es []error
	// Core authorizes both network and local requests. Embedded UI enablement
	// must never determine whether controller authentication is validated.
	if c.Instance.Role == "controller" {
		if c.Web.CredentialsFile == "" {
			es = append(es, fmt.Errorf("web.credentials_file is required for controller authorization"))
		}
		if c.Web.SessionTTL.Duration < 5*time.Minute || c.Web.SessionTTL.Duration > 30*24*time.Hour {
			es = append(es, fmt.Errorf("web.session_ttl must be 5m..30d for controller authorization"))
		}
	}
	if c.Web.Enabled {
		host, _, err := listenPort(c.Web.Listen)
		if err != nil {
			es = append(es, fmt.Errorf("web.listen: %w", err))
		} else if !c.Web.TLS.Enabled && !loopback(host) {
			es = append(es, fmt.Errorf("TLS-off web listener must be loopback"))
		}
		es = append(es, tlsErrors("web.tls", c.Web.TLS)...)
	}
	if c.Instance.Role == "ui" || c.Instance.Role == "ui-proxy" {
		if !c.UIProxy.Enabled || !c.Web.Enabled {
			es = append(es, fmt.Errorf("UI requires ui.enabled and web.enabled"))
		}
		if !validHTTPURL(c.UIProxy.Upstream, false) {
			es = append(es, fmt.Errorf("ui.upstream must be an HTTP(S) URL without credentials or fragment"))
		} else {
			u, _ := url.Parse(c.UIProxy.Upstream)
			if u.Scheme == "http" {
				if !loopback(u.Hostname()) {
					es = append(es, fmt.Errorf("plaintext UI upstream must be loopback"))
				}
				if c.UIProxy.InsecureTLS || c.UIProxy.UpstreamCAFile != "" || c.UIProxy.UpstreamSPKISHA256 != "" {
					es = append(es, fmt.Errorf("UI TLS trust options require an HTTPS upstream"))
				}
			}
			if u.RawQuery != "" || (u.Path != "" && u.Path != "/") {
				es = append(es, fmt.Errorf("ui.upstream must be an origin URL"))
			}
		}
		if c.UIProxy.UpstreamSPKISHA256 != "" {
			raw := strings.TrimPrefix(c.UIProxy.UpstreamSPKISHA256, "sha256/")
			pin, err := base64.StdEncoding.DecodeString(raw)
			if err != nil || len(pin) != 32 {
				pin, err = hex.DecodeString(raw)
			}
			if err != nil || len(pin) != 32 {
				es = append(es, fmt.Errorf("ui.upstream_spki_sha256 must be a base64 or hex SHA-256 digest"))
			}
		}
		if c.UIProxy.InsecureTLS && (c.UIProxy.UpstreamSPKISHA256 != "" || c.UIProxy.UpstreamCAFile != "") {
			es = append(es, fmt.Errorf("ui.insecure_tls cannot be combined with CA or pin trust"))
		}
		if err := durationError("ui.request_timeout", c.UIProxy.RequestTimeout, 5*time.Minute); err != nil {
			es = append(es, err)
		}
	}
	if c.Update.AutoApply {
		es = append(es, fmt.Errorf("update.auto_apply is unsupported; signed updates require an explicit panel or CLI action"))
	}
	return es
}

// Resolve existing ancestors to detect symlink escapes even if the final file
// has yet to be created. The configuration root itself may be a symlink.
func physicalPath(p string) string {
	p = filepath.Clean(p)
	tail := []string{}
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return real
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		tail = append(tail, filepath.Base(p))
		p = parent
	}
}
func within(root, p string) bool {
	rel, err := filepath.Rel(physicalPath(root), physicalPath(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func (c Config) validateController() []error {
	var es []error
	add := func(err error) {
		if err != nil {
			es = append(es, err)
		}
	}
	for name, p := range map[string]string{"paths.state_dir": c.Paths.StateDir, "paths.cache_dir": c.Paths.CacheDir, "paths.run_dir": c.Paths.RunDir, "web.credentials_file": c.Web.CredentialsFile} {
		if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
			add(fmt.Errorf("%s requires an absolute path without NUL", name))
		}
	}
	if c.API.Enabled {
		host, _, err := listenPort(c.API.Listen)
		add(err)
		if err == nil && !loopback(host) {
			add(fmt.Errorf("api.listen must be loopback; use an authenticated UI proxy for remote access"))
		}
		es = append(es, tlsErrors("api.tls", c.API.TLS)...)
	}
	if c.API.UnixSocket != "" && (!filepath.IsAbs(c.API.UnixSocket) || !within(c.Paths.RunDir, c.API.UnixSocket)) {
		add(fmt.Errorf("api.unix_socket must be an absolute path inside paths.run_dir"))
	}
	if c.Update.InstallDir != "" {
		if !filepath.IsAbs(c.Update.InstallDir) || strings.ContainsRune(c.Update.InstallDir, 0) {
			add(fmt.Errorf("update.install_dir must be absolute without NUL"))
		}
		for _, path := range []string{c.Paths.StateDir, c.Paths.CacheDir, c.Paths.RunDir, c.Xray.ConfigDir} {
			if within(path, c.Update.InstallDir) || within(c.Update.InstallDir, path) {
				add(fmt.Errorf("update.install_dir must be separate from controller state, cache, run and Xray directories"))
			}
		}
	}
	if c.Update.LauncherSocket != "" && (!filepath.IsAbs(c.Update.LauncherSocket) || strings.ContainsRune(c.Update.LauncherSocket, 0) || !within(c.Paths.RunDir, c.Update.LauncherSocket) || physicalPath(c.Update.LauncherSocket) == physicalPath(c.Paths.RunDir) || physicalPath(c.Update.LauncherSocket) == physicalPath(c.API.UnixSocket)) {
		add(fmt.Errorf("update.launcher_socket must be a distinct absolute socket inside paths.run_dir"))
	}
	if !filepath.IsAbs(c.Xray.ConfigDir) || !filepath.IsAbs(c.Xray.ManagedDir) || physicalPath(c.Xray.ConfigDir) != physicalPath(c.Xray.ManagedDir) {
		add(fmt.Errorf("xray.managed_dir must resolve to absolute xray.config_dir; nested directories are not loaded"))
	}
	if c.Xray.BaseRoutingFile != "" && (!filepath.IsAbs(c.Xray.BaseRoutingFile) || !within(c.Xray.ConfigDir, c.Xray.BaseRoutingFile) || physicalPath(filepath.Dir(c.Xray.BaseRoutingFile)) != physicalPath(c.Xray.ConfigDir) || filepath.Ext(c.Xray.BaseRoutingFile) != ".json") {
		add(fmt.Errorf("xray.base_routing_file must be a JSON file directly inside config_dir"))
	}
	host, apiPort, err := listenPort(c.Xray.APIAddress)
	add(err)
	if err == nil && !loopback(host) {
		add(fmt.Errorf("xray.api_address must be loopback"))
	}
	durations := []struct {
		name string
		d    Duration
		max  time.Duration
	}{
		{"platform.command_timeout", c.Platform.CommandTimeout, 5 * time.Minute},
		{"subscriptions.cache_ttl", c.Subscriptions.CacheTTL, 30 * 24 * time.Hour},
		{"subscriptions.refresh_interval", c.Subscriptions.RefreshInterval, 7 * 24 * time.Hour},
		{"subscriptions.request_timeout", c.Subscriptions.RequestTimeout, 2 * time.Minute},
		{"health.interval", c.Health.Interval, time.Hour}, {"health.request_timeout", c.Health.RequestTimeout, time.Minute},
		{"health.hot_pool_freshness", c.Health.HotPoolFreshness, 24 * time.Hour},
		{"failover.detection_interval", c.Failover.DetectionInterval, time.Minute}, {"failover.probe_timeout", c.Failover.ProbeTimeout, time.Minute}, {"failover.overall_deadline", c.Failover.OverallDeadline, 2 * time.Minute},
		{"benchmark.full_interval", c.Benchmark.FullInterval, 30 * 24 * time.Hour}, {"benchmark.switch_cooldown", c.Benchmark.SwitchCooldown, 24 * time.Hour},
		{"benchmark.stability_before_upgrade", c.Benchmark.StabilityBeforeUpgrade, 24 * time.Hour}, {"benchmark.temporary_startup_timeout", c.Benchmark.TemporaryStartupTimeout, time.Minute},
		{"benchmark.speed.target_duration", c.Benchmark.Speed.TargetDuration, time.Minute}, {"update.check_interval", c.Update.CheckInterval, 30 * 24 * time.Hour}, {"update.health_grace_period", c.Update.HealthGracePeriod, 10 * time.Minute},
	}
	for _, v := range durations {
		add(durationError(v.name, v.d, v.max))
	}
	if len(c.Health.ProviderRetryBackoff) == 0 || len(c.Health.ProviderRetryBackoff) > 16 {
		add(fmt.Errorf("health.provider_retry_backoff requires 1..16 durations"))
	}
	for _, d := range c.Health.ProviderRetryBackoff {
		add(durationError("health.provider_retry_backoff", d, 24*time.Hour))
	}
	if c.Failover.Quorum < 2 || c.Failover.Quorum > 20 || c.Failover.FailureThreshold < 1 || c.Failover.FailureThreshold > 20 || c.Failover.OverallDeadline.Duration < c.Failover.ProbeTimeout.Duration {
		add(fmt.Errorf("failover quorum/threshold/deadline invalid"))
	}
	if c.Benchmark.Speed.Workers < 1 || c.Benchmark.Speed.Workers > 16 {
		add(fmt.Errorf("benchmark.speed.workers must be 1..16"))
	}
	// Fewer independent targets than quorum are allowed, but failover must classify
	// monitoring as inconclusive. This permits core-only offline setup safely.
	for name, v := range map[string]int{"subscriptions.max_sources": c.Subscriptions.MaxSources, "benchmark.latency_workers": c.Benchmark.LatencyWorkers, "benchmark.requests_per_weight": c.Benchmark.RequestsPerWeight, "benchmark.finalists": c.Benchmark.Finalists, "benchmark.speed.repetitions": c.Benchmark.Speed.Repetitions, "health.failure_threshold": c.Health.FailureThreshold, "health.recovery_threshold": c.Health.RecoveryThreshold} {
		max := 100
		if name == "benchmark.speed.repetitions" || name == "benchmark.requests_per_weight" {
			max = 10
		}
		if v < 1 || v > max {
			add(fmt.Errorf("%s must be 1..%d", name, max))
		}
	}
	if c.Subscriptions.MaxNodesPerSource < 0 || c.Subscriptions.MaxNodesPerSource > 5000 {
		add(fmt.Errorf("subscriptions.max_nodes_per_source must be 0..5000"))
	}
	for name, v := range map[string]ByteSize{"subscriptions.max_response_bytes": c.Subscriptions.MaxResponseBytes, "health.max_response_bytes": c.Health.MaxResponseBytes} {
		if v < 1 || v > 16<<20 {
			add(fmt.Errorf("%s must be 1..16MiB", name))
		}
	}
	for _, t := range c.Targets {
		if !validHTTPURL(t.URL, false) {
			add(fmt.Errorf("target %q URL requires an HTTP(S) host and no credentials", t.ID))
		}
		if t.MaxResponseBytes < 1 || t.MaxResponseBytes > 16<<20 {
			add(fmt.Errorf("target %q max_response_bytes must be 1..16MiB", t.ID))
		}
	}
	for _, s := range c.Subscriptions.Sources {
		if s.Enabled {
			u, err := url.Parse(s.URL)
			if err != nil || u == nil || (u.Scheme != "file" && !validHTTPURL(s.URL, false)) || (u.Scheme == "file" && (u.Host != "" || !filepath.IsAbs(u.Path) || u.RawQuery != "")) {
				add(fmt.Errorf("subscription %q URL invalid", s.ID))
			}
		}
		for k, v := range s.Headers {
			if !httpHeaderNameRE.MatchString(k) || strings.ContainsAny(v, "\r\n") || len(k) > 128 || len(v) > 8192 {
				add(fmt.Errorf("subscription %q header invalid", s.ID))
			}
		}
	}
	if c.Benchmark.Speed.MinSampleBytes < 1 || c.Benchmark.Speed.MaxSampleBytes < c.Benchmark.Speed.MinSampleBytes || c.Benchmark.Speed.MaxSampleBytes > 1<<30 || c.Benchmark.Speed.WarmupBytes < 0 || c.Benchmark.Speed.WarmupBytes > 1<<30 {
		add(fmt.Errorf("benchmark.speed sample sizes invalid"))
	}
	if c.Benchmark.Speed.Enabled && !validHTTPURL(strings.ReplaceAll(c.Benchmark.Speed.URLTemplate, "{bytes}", "1024"), true) {
		add(fmt.Errorf("benchmark.speed.url_template must use HTTPS"))
	}
	if c.Benchmark.MinImprovementPercent < 0 || c.Benchmark.MinImprovementPercent > 100 {
		add(fmt.Errorf("benchmark.min_improvement_percent must be 0..100"))
	}
	if c.Pool.ProviderDiversity.Enabled && (c.Pool.ProviderDiversity.MaxPerProvider < 1 || c.Pool.ProviderDiversity.MaxPerProvider > c.Pool.Size) {
		add(fmt.Errorf("pool.provider_diversity.max_per_provider invalid"))
	}
	ports := map[int]string{}
	register := func(name string, port int) {
		if port < 1 || port > 65535 {
			add(fmt.Errorf("%s port invalid", name))
			return
		}
		if old, ok := ports[port]; ok {
			add(fmt.Errorf("%s port overlaps %s", name, old))
		}
		ports[port] = name
	}
	register("xray.api_address", apiPort)
	register("xray.health_proxy_port", c.Xray.HealthProxyPort)
	// Invalid counts are rejected by Validate. Do not expand untrusted counts
	// into arbitrarily large port maps and diagnostic lists before that check.
	for i := 0; i < c.Pool.Size && i < 20; i++ {
		register("xray.probe", c.Xray.ProbePortStart+i)
	}
	for i := 0; i < c.Benchmark.BatchSize && i < 100; i++ {
		register("benchmark.temporary_proxy", c.Benchmark.TemporaryProxyPortStart+i)
	}
	if c.API.Enabled {
		_, p, e := listenPort(c.API.Listen)
		if e == nil {
			register("api.listen", p)
		}
	}
	if c.Web.Enabled {
		_, p, e := listenPort(c.Web.Listen)
		if e == nil {
			register("web.listen", p)
		}
	}
	fwMode, tcpPort, udpPort := c.Platform.Linux.FirewallMode, c.Platform.Linux.TCPRedirectPort, c.Platform.Linux.UDPTProxyPort
	if c.Platform.Kind == "openwrt" {
		fwMode, tcpPort, udpPort = c.Platform.OpenWrt.FirewallMode, c.Platform.OpenWrt.TCPRedirectPort, c.Platform.OpenWrt.UDPTProxyPort
	}
	if fwMode == "managed" {
		register("platform.tcp_redirect", tcpPort)
		if udpPort != tcpPort {
			register("platform.udp_tproxy", udpPort)
		}
	}
	tagRE := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	tags := map[string]bool{}
	for _, tag := range append([]string{c.Xray.APITag, c.Xray.BalancerTag, c.Xray.ManagedDirectTag}, slotTags(c)...) {
		if !tagRE.MatchString(tag) || tags[tag] || strings.HasPrefix(tag, XraySelectionTag) {
			add(fmt.Errorf("invalid, duplicate, or reserved managed Xray tag %q", tag))
		}
		tags[tag] = true
	}
	for name, xs := range map[string][]string{"inbound_tags": c.Xray.Route.InboundTags, "replace_outbound_tags": c.Xray.Route.ReplaceOutboundTags} {
		seen := map[string]bool{}
		for _, tag := range xs {
			if !tagRE.MatchString(tag) || seen[tag] || tags[tag] {
				add(fmt.Errorf("invalid/duplicate/conflicting xray.route.%s tag", name))
			}
			seen[tag] = true
		}
	}
	if c.Update.Enabled {
		if c.Update.GitHubRepository != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`).MatchString(c.Update.GitHubRepository) {
			add(fmt.Errorf("update.github_repository must be owner/repository"))
		}
		for _, raw := range []string{c.Update.ManifestURL, c.Update.SignatureURL} {
			if raw != "" && (!validHTTPURL(raw, true) || strings.Contains(raw, "/releases/latest/")) {
				add(fmt.Errorf("update URLs must be HTTPS channel-specific or versioned; /releases/latest is forbidden"))
			}
		}
		var key []byte
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if b, e := enc.DecodeString(strings.TrimSpace(c.Update.PublicKey)); e == nil && len(b) == 32 {
				key = b
				break
			}
		}
		if len(key) != 32 {
			add(fmt.Errorf("update.public_key must be a base64 Ed25519 public key"))
		}
	}
	return es
}
func slotTags(c Config) []string {
	xs := []string{}
	for i := 0; i < c.Pool.Size && i < 20; i++ {
		xs = append(xs, fmt.Sprintf("%s%d", c.Xray.SlotTagPrefix, i))
	}
	return xs
}
