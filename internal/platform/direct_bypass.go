package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrDirectBypassUnsupported = errors.New("platform-level direct bypass is unsupported")

func bypassMarker(cfgPath string) string {
	return filepath.Join(cfgPath, "direct-bypass.active")
}

func markBypass(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("active\n"), 0o600)
}

func clearBypass(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func markerActive(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (g *generic) EnterDirectBypass(ctx context.Context) error {
	if !g.managed {
		return ErrDirectBypassUnsupported
	}
	if err := g.removeFirewallRules(ctx, g.firewallConfig()); err != nil {
		return err
	}
	return markBypass(bypassMarker(g.cfg.Paths.RunDir))
}

func (g *generic) LeaveDirectBypass(ctx context.Context) error {
	if !g.managed {
		return ErrDirectBypassUnsupported
	}
	if err := g.EnsureFirewall(ctx); err != nil {
		return err
	}
	return clearBypass(bypassMarker(g.cfg.Paths.RunDir))
}

func (g *generic) DirectBypassActive(context.Context) (bool, error) {
	if !g.managed {
		return false, ErrDirectBypassUnsupported
	}
	return markerActive(bypassMarker(g.cfg.Paths.RunDir))
}

func (k *keenetic) EnterDirectBypass(ctx context.Context) error {
	if _, err := k.r.Run(ctx, []string{k.cfg.Platform.Keenetic.XKeenBinary, "-stop"}); err != nil {
		return fmt.Errorf("stop XKeen for direct bypass: %w", err)
	}
	return markBypass(bypassMarker(k.cfg.Paths.RunDir))
}

func (k *keenetic) LeaveDirectBypass(ctx context.Context) error {
	if _, err := k.r.Run(ctx, []string{k.cfg.Platform.Keenetic.XKeenBinary, "-start"}); err != nil {
		return fmt.Errorf("start XKeen after direct bypass: %w", err)
	}
	return clearBypass(bypassMarker(k.cfg.Paths.RunDir))
}

func (k *keenetic) DirectBypassActive(context.Context) (bool, error) {
	return markerActive(bypassMarker(k.cfg.Paths.RunDir))
}
