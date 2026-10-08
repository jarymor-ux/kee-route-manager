package core

import (
	"context"
	"net"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
)

func (m *Manager) PanelDNSAutomatic() bool { _, ok := m.platform.(platform.PanelDNSAdapter); return ok }
func (m *Manager) EnsurePanelAlias(ctx context.Context, hostname, ip string) error {
	address := net.ParseIP(ip)
	if !config.ValidPanelHostname(hostname) || address == nil || (!address.IsPrivate() && !address.IsLoopback()) || address.String() != ip {
		return platform.ErrPanelDNSUnavailable
	}
	return m.RunAction(ctx, "panel-dns", "web", func(ctx context.Context) error {
		if adapter, ok := m.platform.(platform.PanelDNSAdapter); ok {
			return adapter.EnsurePanelAlias(ctx, hostname, ip)
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, hostname)
		if err != nil {
			return platform.ErrPanelDNSUnavailable
		}
		for _, resolved := range addresses {
			if resolved.IP.Equal(address) {
				return nil
			}
		}
		return platform.ErrPanelDNSUnavailable
	})
}
