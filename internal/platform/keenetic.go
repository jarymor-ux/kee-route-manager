package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type keenetic struct {
	cfg  config.Config
	r    Runner
	http *http.Client
}

func newKeenetic(c config.Config, r Runner) Adapter {
	return &keenetic{c, r, &http.Client{Timeout: 8 * time.Second}}
}
func (k *keenetic) Kind() string { return "keenetic" }
func (k *keenetic) Capabilities() Capabilities {
	return Capabilities{Metrics: true, Clients: true, ClientPolicy: k.cfg.Platform.Keenetic.AllowPolicyChange, WakeOnLAN: true, Reboot: k.cfg.Platform.Keenetic.AllowReboot, SystemLogs: true, Diagnostics: true}
}
func (k *keenetic) RestartXray(ctx context.Context) error {
	_, e := k.r.Run(ctx, k.cfg.Platform.XrayRestartCommand)
	return e
}
func (k *keenetic) XrayRunning(ctx context.Context) bool {
	_, e := k.r.Run(ctx, k.cfg.Platform.XrayStatusCommand)
	return e == nil
}
func (k *keenetic) RestartKRM(ctx context.Context) error {
	_, e := k.r.Run(ctx, k.cfg.Platform.KRMRestartCommand)
	return e
}
func (k *keenetic) rci(ctx context.Context, path string) (map[string]any, error) {
	u := strings.TrimRight(k.cfg.Platform.Keenetic.RCIBaseURL, "/") + "/" + path
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, e
	}
	resp, e := k.http.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RCI HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if e != nil {
		return nil, e
	}
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	if _, bad := v["status"]; bad {
		return nil, fmt.Errorf("RCI status error")
	}
	return v, nil
}
func (k *keenetic) Metrics(ctx context.Context) (Metrics, error) {
	sys, e := k.rci(ctx, "show/system")
	if e != nil {
		return Metrics{}, e
	}
	ifs, e := k.rci(ctx, "show/interface")
	if e != nil {
		return Metrics{}, e
	}
	wan := map[string]any{}
	for _, raw := range ifs {
		if v, ok := raw.(map[string]any); ok && truth(v["defaultgw"]) && fmt.Sprint(v["connected"]) == "yes" {
			wan = v
			break
		}
	}
	name := stringValue(wan["id"])
	if name != "" && !regexp.MustCompile(`^[A-Za-z0-9_/.-]+$`).MatchString(name) {
		return Metrics{}, fmt.Errorf("invalid interface id")
	}
	stats := map[string]any{}
	if name != "" {
		stats, e = k.rci(ctx, "show/interface/stat?name="+url.QueryEscape(name))
		if e != nil {
			return Metrics{}, e
		}
	}
	connected := name != ""
	m := Metrics{UpdatedAt: time.Now().UTC(), WANConnected: &connected, WANName: name, WANDescription: stringValue(wan["description"]), WANIP: stringValue(wan["address"]), RXBytes: uint64(number(stats["rxbytes"])), TXBytes: uint64(number(stats["txbytes"])), RXMbps: number(stats["rxspeed"]) * 8 / 1e6, TXMbps: number(stats["txspeed"]) * 8 / 1e6, Connections: int64(number(sys["conntotal"]) - number(sys["connfree"])), UptimeSeconds: int64(number(sys["uptime"]))}
	if cp, e := k.rci(ctx, "show/system/cpustat"); e == nil {
		if busy, ok := cp["busy"].(map[string]any); ok {
			v := number(busy["cur"])
			m.CPUPercent = &v
		}
	}
	if m.CPUPercent == nil {
		v := number(sys["cpuload"])
		m.CPUPercent = &v
	}
	mem := strings.Split(stringValue(sys["memory"]), "/")
	if len(mem) == 2 {
		used, _ := strconv.ParseInt(mem[0], 10, 64)
		total, _ := strconv.ParseInt(mem[1], 10, 64)
		m.RAMUsedMB = used / 1024
		m.RAMTotalMB = total / 1024
		if total > 0 {
			v := 100 * float64(used) / float64(total)
			m.RAMPercent = &v
		}
	}
	for _, raw := range ifs {
		if v, ok := raw.(map[string]any); ok && stringValue(v["type"]) == "Port" {
			m.Ports = append(m.Ports, Port{ID: stringValue(v["id"]), Link: v["link"], Speed: v["speed"]})
		}
	}
	sort.Slice(m.Ports, func(i, j int) bool { return m.Ports[i].ID < m.Ports[j].ID })
	if b, e := os.ReadFile("/sys/class/thermal/thermal_zone0/temp"); e == nil {
		v, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if v > 1000 {
			v /= 1000
		}
		m.TemperatureC = &v
	}
	return m, nil
}
func (k *keenetic) Clients(ctx context.Context) ([]Client, error) {
	live, e := k.rci(ctx, "show/ip/hotspot")
	if e != nil {
		return nil, e
	}
	cfg, e := k.rci(ctx, "ip/hotspot")
	if e != nil {
		return nil, e
	}
	policies, e := k.rci(ctx, "show/ip/policy")
	if e != nil {
		return nil, e
	}
	rules := map[string]map[string]any{}
	for _, raw := range array(cfg["host"]) {
		if v, ok := raw.(map[string]any); ok {
			rules[strings.ToLower(stringValue(v["mac"]))] = v
		}
	}
	segments := map[string]string{}
	for _, raw := range array(cfg["policy"]) {
		if v, ok := raw.(map[string]any); ok {
			segments[stringValue(v["interface"])] = stringValue(v["policy"])
		}
	}
	out := []Client{}
	for _, raw := range array(live["host"]) {
		h, ok := raw.(map[string]any)
		if !ok || (!truth(h["active"]) && !truth(h["registered"])) {
			continue
		}
		mac := strings.ToLower(stringValue(h["mac"]))
		rule := rules[mac]
		inherit := truth(rule["conform"])
		pid := stringValue(rule["policy"])
		if inherit {
			if iface, ok := h["interface"].(map[string]any); ok {
				pid = segments[stringValue(iface["id"])]
			}
		}
		desc := pid
		if p, ok := policies[pid].(map[string]any); ok && stringValue(p["description"]) != "" {
			desc = stringValue(p["description"])
		}
		if desc == "" {
			desc = "Default policy"
		}
		out = append(out, Client{MAC: mac, Registered: truth(h["registered"]), Name: stringValue(h["name"]), Hostname: stringValue(h["hostname"]), IP: stringValue(h["ip"]), Active: truth(h["active"]), Link: stringValue(h["link"]), SSID: stringValue(h["ssid"]), RSSI: int(number(h["rssi"])), RXBytes: uint64(number(h["rxbytes"])), TXBytes: uint64(number(h["txbytes"])), Access: stringValue(h["access"]), ConnectionPolicy: desc, PolicyInherited: inherit, PolicyID: pid})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
func (k *keenetic) Wake(ctx context.Context, mac string) error {
	if !validMAC(mac) {
		return fmt.Errorf("invalid MAC")
	}
	xs, e := k.Clients(ctx)
	if e != nil {
		return e
	}
	known := false
	for _, c := range xs {
		if c.Registered && strings.EqualFold(c.MAC, mac) {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("device must be registered")
	}
	_, e = k.r.Run(ctx, []string{k.cfg.Platform.Keenetic.NDMCBinary, "-c", "ip hotspot wake " + strings.ToLower(mac)})
	return e
}
func (k *keenetic) SetClientPolicy(ctx context.Context, mac, choice string) error {
	if !k.cfg.Platform.Keenetic.AllowPolicyChange {
		return fmt.Errorf("policy changes disabled")
	}
	if !validMAC(mac) || (choice != "xkeen" && choice != "default") {
		return fmt.Errorf("invalid policy request")
	}
	mac = strings.ToLower(mac)
	cfg, e := k.rci(ctx, "ip/hotspot")
	if e != nil {
		return e
	}
	var before map[string]any
	for _, raw := range array(cfg["host"]) {
		if v, ok := raw.(map[string]any); ok && strings.EqualFold(stringValue(v["mac"]), mac) {
			before = v
			break
		}
	}
	if before == nil {
		return fmt.Errorf("device must be registered")
	}
	policies, e := k.rci(ctx, "show/ip/policy")
	if e != nil {
		return e
	}
	pid := ""
	for id, raw := range policies {
		if p, ok := raw.(map[string]any); ok && strings.EqualFold(stringValue(p["description"]), k.cfg.Platform.Keenetic.XKeenPolicyName) {
			pid = id
			break
		}
	}
	if choice == "xkeen" && !regexp.MustCompile(`^Policy[0-9]+$`).MatchString(pid) {
		return fmt.Errorf("XKeen policy unavailable")
	}
	command := func(c context.Context, text string) error {
		_, err := k.r.Run(c, []string{k.cfg.Platform.Keenetic.NDMCBinary, "-c", text})
		return err
	}
	findHost := func(cfg map[string]any) map[string]any {
		for _, raw := range array(cfg["host"]) {
			if v, ok := raw.(map[string]any); ok && strings.EqualFold(stringValue(v["mac"]), mac) {
				return v
			}
		}
		return nil
	}
	rollback := func(cause error) error {
		// Recovery must outlive a disconnected caller, but remain bounded as a whole.
		timeout := k.r.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		var errs []error
		run := func(text string) {
			if err := command(recovery, text); err != nil {
				errs = append(errs, err)
			}
		}
		old := stringValue(before["policy"])
		if regexp.MustCompile(`^Policy[0-9]+$`).MatchString(old) {
			run("ip hotspot host " + mac + " policy " + old)
		} else {
			run("no ip hotspot host " + mac + " policy")
		}
		if access := stringValue(before["access"]); access == "permit" || access == "deny" {
			run("ip hotspot host " + mac + " " + access)
		}
		if truth(before["conform"]) {
			run("ip hotspot host " + mac + " conform")
		} else {
			run("no ip hotspot host " + mac + " conform")
		}
		run("system configuration save")
		restoredCfg, err := k.rci(recovery, "ip/hotspot")
		if err != nil {
			errs = append(errs, err)
		} else {
			restored := findHost(restoredCfg)
			if restored == nil || truth(restored["conform"]) != truth(before["conform"]) || stringValue(restored["policy"]) != old || stringValue(restored["access"]) != stringValue(before["access"]) {
				errs = append(errs, fmt.Errorf("router did not restore client policy safely"))
			}
		}
		if err := errors.Join(errs...); err != nil {
			return errors.Join(cause, fmt.Errorf("client policy rollback: %w", err))
		}
		return cause
	}
	if e = command(ctx, "no ip hotspot host "+mac+" conform"); e != nil {
		return rollback(e)
	}
	if choice == "xkeen" {
		e = command(ctx, "ip hotspot host "+mac+" policy "+pid)
	} else {
		e = command(ctx, "no ip hotspot host "+mac+" policy")
	}
	if e != nil {
		return rollback(e)
	}
	afterCfg, e := k.rci(ctx, "ip/hotspot")
	if e != nil {
		return rollback(e)
	}
	after := findHost(afterCfg)
	expected := ""
	if choice == "xkeen" {
		expected = pid
	}
	if after == nil || truth(after["conform"]) || stringValue(after["policy"]) != expected || stringValue(after["access"]) != stringValue(before["access"]) {
		return rollback(fmt.Errorf("router did not apply policy safely"))
	}
	if e = command(ctx, "system configuration save"); e != nil {
		return rollback(e)
	}
	return nil
}
func (k *keenetic) Reboot(ctx context.Context) error {
	if !k.cfg.Platform.Keenetic.AllowReboot {
		return fmt.Errorf("reboot disabled")
	}
	go func() {
		time.Sleep(time.Second)
		_, _ = k.r.Run(context.Background(), []string{k.cfg.Platform.Keenetic.NDMCBinary, "-c", "system reboot"})
	}()
	return nil
}
func (k *keenetic) SystemLogs(ctx context.Context, lines int) (string, error) {
	if lines < 1 || lines > 1000 {
		return "", fmt.Errorf("lines must be 1..1000")
	}
	b, e := k.r.Run(ctx, []string{k.cfg.Platform.Keenetic.NDMCBinary, "-c", "show log"})
	if e != nil {
		return "", e
	}
	xs := strings.Split(string(b), "\n")
	if len(xs) > lines {
		xs = xs[len(xs)-lines:]
	}
	return strings.Join(xs, "\n"), nil
}
func (k *keenetic) Diagnostics(ctx context.Context) (string, error) {
	var b strings.Builder
	for _, cmd := range [][]string{{"nslookup", "example.com"}, {"ping", "-c", "3", "-W", "2", "1.1.1.1"}, {"ping", "-c", "3", "-W", "2", "8.8.8.8"}} {
		b.WriteString("$ " + strings.Join(cmd, " ") + "\n")
		out, e := k.r.Run(ctx, cmd)
		b.Write(out)
		if e != nil {
			b.WriteString(e.Error())
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}
func truth(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "yes" || x == "true" || x == "1"
	case float64:
		return x != 0
	}
	return false
}
func number(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		y, _ := x.Float64()
		return y
	case string:
		y, _ := strconv.ParseFloat(x, 64)
		return y
	}
	return 0
}
func stringValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func array(v any) []any { a, _ := v.([]any); return a }
func validMAC(v string) bool {
	return regexp.MustCompile(`^(?i:[0-9a-f]{2}:){5}(?i:[0-9a-f]{2})$`).MatchString(v)
}
func (k *keenetic) EnsureFirewall(context.Context) error { return nil }
func (k *keenetic) RemoveFirewall(context.Context) error { return nil }

// XKeen's interception is outside KRM ownership. Automatic bypass is deliberately
// unavailable until a router-specific mechanism has been tested on hardware.
func (k *keenetic) EnterDirectBypass(context.Context) error {
	return fmt.Errorf("platform direct bypass is unsupported on Keenetic; disable XKeen interception using the router's documented policy controls")
}
func (k *keenetic) LeaveDirectBypass(context.Context) error          { return nil }
func (k *keenetic) DirectBypassActive(context.Context) (bool, error) { return false, nil }
