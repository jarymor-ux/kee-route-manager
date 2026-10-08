package platform

import (
	"context"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"strconv"
	"strings"
	"sync"
)

type generic struct {
	cfg             config.Config
	r               Runner
	kind            string
	reboot, managed bool
	firewallMu      sync.Mutex
	cpu             cpuSampler
}

func newGeneric(c config.Config, r Runner, k string, reboot, managed bool) Adapter {
	return &generic{cfg: c, r: r, kind: k, reboot: reboot, managed: managed}
}
func (g *generic) Kind() string { return g.kind }
func (g *generic) Capabilities() Capabilities {
	return Capabilities{Metrics: true, Reboot: g.reboot, SystemLogs: true, Diagnostics: true, ManagedFirewall: g.managed, DirectBypass: g.managed}
}
func (g *generic) RestartXray(ctx context.Context) error {
	_, e := g.r.Run(ctx, g.cfg.Platform.XrayRestartCommand)
	return e
}
func (g *generic) XrayRunning(ctx context.Context) bool {
	_, e := g.r.Run(ctx, g.cfg.Platform.XrayStatusCommand)
	return e == nil
}
func (g *generic) RestartKRM(ctx context.Context) error {
	_, e := g.r.Run(ctx, g.cfg.Platform.KRMRestartCommand)
	return e
}
func (g *generic) Metrics(context.Context) (Metrics, error) {
	m, err := linuxMetrics()
	if err == nil {
		m.CPUPercent = g.cpu.sample()
	}
	return m, err
}
func (g *generic) Clients(context.Context) ([]Client, error) {
	return nil, fmt.Errorf("clients unsupported on %s", g.kind)
}
func (g *generic) Wake(context.Context, string) error {
	return fmt.Errorf("wake unsupported on %s", g.kind)
}
func (g *generic) SetClientPolicy(context.Context, string, string) error {
	return fmt.Errorf("policy unsupported on %s", g.kind)
}
func (g *generic) Reboot(ctx context.Context) error {
	if !g.reboot {
		return fmt.Errorf("reboot disabled")
	}
	_, e := g.r.Run(ctx, []string{"reboot"})
	return e
}
func (g *generic) SystemLogs(ctx context.Context, lines int) (string, error) {
	if lines < 1 || lines > 1000 {
		return "", fmt.Errorf("lines must be 1..1000")
	}
	out, e := g.r.Run(ctx, []string{"journalctl", "-n", strconv.Itoa(lines), "--no-pager"})
	if e != nil {
		out, e = g.r.Run(ctx, []string{"logread", "-l", strconv.Itoa(lines)})
	}
	return string(out), e
}
func (g *generic) Diagnostics(ctx context.Context) (string, error) {
	var b strings.Builder
	for _, cmd := range [][]string{{"ip", "route"}, {"ip", "addr"}, {"cat", "/etc/resolv.conf"}} {
		b.WriteString("$ " + strings.Join(cmd, " ") + "\n")
		out, e := g.r.Run(ctx, cmd)
		b.Write(out)
		if e != nil {
			b.WriteString(e.Error())
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}
