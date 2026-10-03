package xray

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

func (m *Manager) HealthProxy() string {
	return fmt.Sprintf("http://127.0.0.1:%d", m.cfg.Xray.HealthProxyPort)
}
func (m *Manager) SlotProxy(i int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", m.cfg.Xray.ProbePortStart+i)
}
func (m *Manager) Switch(ctx context.Context, tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.switchUnlocked(ctx, tag)
}
func (m *Manager) switchUnlocked(ctx context.Context, tag string) error {
	path := filepath.Join(m.cfg.Xray.ManagedDir, "04_90_kee_route_manager_outbounds.json")
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next, err := selectOutbound(old, tag)
	if err != nil {
		return err
	}
	if !bytes.Equal(old, next) {
		if err = atomicWrite(path, next, 0600); err != nil {
			return err
		}
	}
	_, err = m.r.Run(ctx, []string{m.cfg.Xray.Binary, "api", "bo", "--server=" + m.cfg.Xray.APIAddress, "-b", m.cfg.Xray.BalancerTag, tag})
	if err != nil {
		return errors.Join(err, atomicWrite(path, old, 0600))
	}
	return nil
}
func (m *Manager) Direct(ctx context.Context) error {
	return m.Switch(ctx, m.cfg.Xray.ManagedDirectTag)
}
func (m *Manager) WaitReady(ctx context.Context, timeout time.Duration) error {
	return m.waitAPI(ctx, timeout)
}
func (m *Manager) waitAPI(ctx context.Context, d time.Duration) error {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		c, e := net.DialTimeout("tcp", m.cfg.Xray.APIAddress, time.Second)
		if e == nil {
			_ = c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("xray API did not become ready")
}
