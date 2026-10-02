package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type Capabilities struct {
	Metrics         bool `json:"metrics"`
	Clients         bool `json:"clients"`
	ClientPolicy    bool `json:"client_policy"`
	WakeOnLAN       bool `json:"wake_on_lan"`
	Reboot          bool `json:"reboot"`
	SystemLogs      bool `json:"system_logs"`
	Diagnostics     bool `json:"diagnostics"`
	ManagedFirewall bool `json:"managed_firewall"`
	DirectBypass    bool `json:"direct_bypass"`
}
type Port struct {
	ID    string `json:"id"`
	Link  any    `json:"link,omitempty"`
	Speed any    `json:"speed,omitempty"`
}
type Metrics struct {
	CPUPercent     *float64  `json:"cpu_percent,omitempty"`
	RAMPercent     *float64  `json:"ram_percent,omitempty"`
	RAMUsedMB      int64     `json:"ram_used_mb,omitempty"`
	RAMTotalMB     int64     `json:"ram_total_mb,omitempty"`
	TemperatureC   *float64  `json:"temperature_c,omitempty"`
	UptimeSeconds  int64     `json:"uptime_seconds,omitempty"`
	WANConnected   *bool     `json:"wan_connected,omitempty"`
	WANName        string    `json:"wan_name,omitempty"`
	WANDescription string    `json:"wan_description,omitempty"`
	WANIP          string    `json:"wan_ip,omitempty"`
	RXBytes        uint64    `json:"rx_bytes,omitempty"`
	TXBytes        uint64    `json:"tx_bytes,omitempty"`
	RXMbps         float64   `json:"rx_mbps,omitempty"`
	TXMbps         float64   `json:"tx_mbps,omitempty"`
	Connections    int64     `json:"connections,omitempty"`
	Ports          []Port    `json:"ports,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
	Stale          bool      `json:"stale"`
	Error          string    `json:"error,omitempty"`
}
type Client struct {
	MAC              string `json:"mac"`
	Registered       bool   `json:"registered"`
	Name             string `json:"name,omitempty"`
	Hostname         string `json:"hostname,omitempty"`
	IP               string `json:"ip,omitempty"`
	Active           bool   `json:"active"`
	Link             string `json:"link,omitempty"`
	SSID             string `json:"ssid,omitempty"`
	RSSI             int    `json:"rssi,omitempty"`
	RXBytes          uint64 `json:"rx_bytes,omitempty"`
	TXBytes          uint64 `json:"tx_bytes,omitempty"`
	Access           string `json:"access,omitempty"`
	ConnectionPolicy string `json:"connection_policy,omitempty"`
	PolicyInherited  bool   `json:"policy_inherited"`
	PolicyID         string `json:"policy_id,omitempty"`
}

type Adapter interface {
	Kind() string
	Capabilities() Capabilities
	RestartXray(context.Context) error
	XrayRunning(context.Context) bool
	RestartKRM(context.Context) error
	Metrics(context.Context) (Metrics, error)
	Clients(context.Context) ([]Client, error)
	Wake(context.Context, string) error
	SetClientPolicy(context.Context, string, string) error
	Reboot(context.Context) error
	SystemLogs(context.Context, int) (string, error)
	Diagnostics(context.Context) (string, error)
	EnsureFirewall(context.Context) error
	RemoveFirewall(context.Context) error
	EnterDirectBypass(context.Context) error
	LeaveDirectBypass(context.Context) error
	DirectBypassActive(context.Context) (bool, error)
}

type Runner struct {
	Timeout   time.Duration
	MaxOutput int64
}

func (r Runner) Run(ctx context.Context, cmd []string) ([]byte, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > timeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	// Service wrappers can spawn children that keep output pipes open after the
	// wrapper is killed. Cancel the complete command group and bound pipe waits.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = time.Second
	var out, errOut limitBuffer
	limit := r.MaxOutput
	if limit <= 0 {
		limit = 4 << 20
	}
	out.limit = limit
	errOut.limit = limit
	c.Stdout = &out
	c.Stderr = &errOut
	err := c.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out.Bytes(), fmt.Errorf("command timed out: %s: %w", cmd[0], ctx.Err())
	}
	if ctx.Err() != nil {
		return out.Bytes(), fmt.Errorf("command canceled: %s: %w", cmd[0], ctx.Err())
	}
	if err != nil {
		m := strings.TrimSpace(errOut.String())
		if m == "" {
			m = strings.TrimSpace(out.String())
		}
		if m == "" {
			return out.Bytes(), fmt.Errorf("%s: %w", cmd[0], err)
		}
		return out.Bytes(), fmt.Errorf("%s: %s: %w", cmd[0], m, err)
	}
	return out.Bytes(), nil
}

type limitBuffer struct {
	b              bytes.Buffer
	limit, written int64
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	orig := len(p)
	remain := b.limit - b.written
	if remain <= 0 {
		return orig, nil
	}
	if int64(len(p)) > remain {
		p = p[:remain]
	}
	n, err := b.b.Write(p)
	b.written += int64(n)
	if err != nil {
		return n, err
	}
	return orig, nil
}
func (b *limitBuffer) Bytes() []byte  { return b.b.Bytes() }
func (b *limitBuffer) String() string { return b.b.String() }

func New(cfg config.Config) (Adapter, Runner, error) {
	r := Runner{Timeout: cfg.Platform.CommandTimeout.Duration, MaxOutput: 4 << 20}
	switch cfg.Platform.Kind {
	case "keenetic":
		return newKeenetic(cfg, r), r, nil
	case "openwrt":
		return newGeneric(cfg, r, "openwrt", cfg.Platform.OpenWrt.AllowReboot, cfg.Platform.OpenWrt.FirewallMode == "managed"), r, nil
	case "linux-systemd":
		return newGeneric(cfg, r, "linux-systemd", cfg.Platform.Linux.AllowReboot, cfg.Platform.Linux.FirewallMode == "managed"), r, nil
	default:
		return nil, r, fmt.Errorf("unsupported platform %q", cfg.Platform.Kind)
	}
}

func linuxMetrics() (Metrics, error) {
	m := Metrics{UpdatedAt: time.Now().UTC()}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		f := strings.Fields(string(b))
		if len(f) > 0 {
			v, _ := strconv.ParseFloat(f[0], 64)
			m.UptimeSeconds = int64(v)
		}
	}
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return m, err
	}
	vals := map[string]int64{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			v, _ := strconv.ParseInt(f[1], 10, 64)
			vals[strings.TrimSuffix(f[0], ":")] = v
		}
	}
	total := vals["MemTotal"]
	available := vals["MemAvailable"]
	if available == 0 {
		available = vals["MemFree"] + vals["Buffers"] + vals["Cached"]
	}
	used := total - available
	m.RAMTotalMB = total / 1024
	m.RAMUsedMB = used / 1024
	if total > 0 {
		v := 100 * float64(used) / float64(total)
		m.RAMPercent = &v
	}
	if b, err = os.ReadFile("/sys/class/thermal/thermal_zone0/temp"); err == nil {
		v, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if v > 1000 {
			v /= 1000
		}
		m.TemperatureC = &v
	}
	return m, nil
}
func remoteIP(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

var _ = remoteIP
