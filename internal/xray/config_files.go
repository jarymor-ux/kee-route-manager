package xray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/redact"
)

func (m *Manager) validateCandidate(ctx context.Context, v Managed) error {
	if e := m.checkRoutingFiles(); e != nil {
		return e
	}
	tmp, e := os.MkdirTemp(m.cfg.Paths.RunDir, "krm-xray-candidate-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	entries, e := os.ReadDir(m.cfg.Xray.ConfigDir)
	if e != nil {
		return e
	}
	managed := map[string]bool{"00_90_kee_route_manager_api.json": true, "03_90_kee_route_manager_inbounds.json": true, "04_90_kee_route_manager_outbounds.json": true, "05_90_kee_route_manager_routing.json": true}
	for _, x := range entries {
		if x.IsDir() || managed[x.Name()] {
			continue
		}
		if strings.HasSuffix(x.Name(), ".json") {
			if e = m.checkSelectionCollision(filepath.Join(m.cfg.Xray.ConfigDir, x.Name())); e != nil {
				return e
			}
		}
		if e = copyFile(filepath.Join(m.cfg.Xray.ConfigDir, x.Name()), filepath.Join(tmp, x.Name()), 0600); e != nil {
			return e
		}
	}
	if e = writeManaged(tmp, v); e != nil {
		return e
	}
	if m.cfg.Xray.BaseRoutingFile != "" {
		if e = m.patchBaseRoute(filepath.Join(tmp, filepath.Base(m.cfg.Xray.BaseRoutingFile))); e != nil {
			return e
		}
	}
	return m.validateDir(ctx, tmp)
}
func (m *Manager) validateDir(ctx context.Context, dir string) error {
	timeout := m.cfg.Platform.CommandTimeout.Duration
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	validationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(validationCtx, m.cfg.Xray.Binary, "run", "-test", "-confdir", dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = time.Second
	if m.cfg.Xray.AssetDir != "" {
		cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+m.cfg.Xray.AssetDir, "xray.location.asset="+m.cfg.Xray.AssetDir)
	}
	out := &commandOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("xray validation: %w: %s", err, redact.Text(strings.TrimSpace(out.String())))
	}
	return nil
}
func (m *Manager) patchBaseRoute(path string) error {
	if path == "" {
		return nil
	}
	b, e := m.patchedBaseRoute(path)
	if e != nil {
		return e
	}
	return atomicWrite(path, b, 0600)
}
func (m *Manager) patchedBaseRoute(path string) ([]byte, error) {
	root, e := readRoutingRoot(path)
	if e != nil {
		return nil, e
	}
	if e = m.prepareBaseRouting(root); e != nil {
		return nil, e
	}
	routing := root["routing"].(map[string]any)
	rules := routing["rules"].([]any)
	replace := map[string]bool{}
	for _, x := range m.cfg.Xray.Route.ReplaceOutboundTags {
		replace[x] = true
	}
	wanted := map[string]bool{}
	for _, x := range m.cfg.Xray.Route.InboundTags {
		wanted[x] = true
	}
	changed := 0
	already := false
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringValue(rule["balancerTag"]) == m.cfg.Xray.BalancerTag && overlap(rule["inboundTag"], wanted) {
			already = true
		}
		if replace[stringValue(rule["outboundTag"])] && overlap(rule["inboundTag"], wanted) {
			delete(rule, "outboundTag")
			rule["balancerTag"] = m.cfg.Xray.BalancerTag
			changed++
		}
	}
	if changed == 0 && !already {
		return nil, fmt.Errorf("no matching routing rule to adopt")
	}
	generated := managedRouting(m.cfg)
	routing["rules"] = append(generated["rules"].([]any), rules...)
	balancers, _ := routing["balancers"].([]any)
	routing["balancers"] = append(balancers, generated["balancers"].([]any)...)
	return pretty(root), nil
}
func writeManaged(dir string, v Managed) error {
	files := map[string][]byte{"00_90_kee_route_manager_api.json": v.API, "03_90_kee_route_manager_inbounds.json": v.Inbounds, "04_90_kee_route_manager_outbounds.json": v.Outbounds, "05_90_kee_route_manager_routing.json": v.Routing}
	for n, b := range files {
		if e := atomicWrite(filepath.Join(dir, n), b, 0600); e != nil {
			return e
		}
	}
	return nil
}
