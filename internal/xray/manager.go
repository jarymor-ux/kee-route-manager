package xray

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

type Runner interface {
	Run(context.Context, []string) ([]byte, error)
}
type Platform interface {
	RestartXray(context.Context) error
	XrayRunning(context.Context) bool
}
type Manager struct {
	cfg config.Config
	r   Runner
	p   Platform
	mu  sync.Mutex
}

func NewManager(c config.Config, r Runner, p Platform) *Manager { return &Manager{cfg: c, r: r, p: p} }
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
func (m *Manager) Bootstrap(ctx context.Context, desired tunnel.DesiredPool) error {
	slots, nodes, initialTag := desired.Slots, desired.Nodes, desired.Selection.Tag
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, e := BuildManaged(m.cfg, slots, nodes)
	if e != nil {
		return e
	}
	if initialTag != "" {
		if managed.Outbounds, e = selectOutbound(managed.Outbounds, initialTag); e != nil {
			return e
		}
	}
	if e = m.validateCandidate(ctx, managed); e != nil {
		return e
	}
	backup, e := m.snapshot()
	if e != nil {
		return e
	}
	if e = m.preserveOriginal(backup); e != nil {
		os.RemoveAll(backup)
		return e
	}
	rollback := func(cause error) error {
		restoreErr := m.restore(backup)
		restartErr := m.p.RestartXray(context.Background())
		return errors.Join(cause, restoreErr, restartErr)
	}
	if e = writeManaged(m.cfg.Xray.ManagedDir, managed); e != nil {
		return rollback(e)
	}
	if e = m.patchBaseRoute(m.cfg.Xray.BaseRoutingFile); e != nil {
		return rollback(e)
	}
	if e = m.validateDir(ctx, m.cfg.Xray.ConfigDir); e != nil {
		return rollback(e)
	}
	if e = m.p.RestartXray(ctx); e != nil {
		return rollback(e)
	}
	if e = m.waitAPI(ctx, 15*time.Second); e != nil {
		return rollback(e)
	}
	if initialTag != "" {
		if e = m.switchUnlocked(ctx, initialTag); e != nil {
			return rollback(e)
		}
	}
	_ = os.RemoveAll(backup)
	return nil
}
func (m *Manager) ApplyPool(ctx context.Context, desired tunnel.DesiredPool) error {
	oldSlots, newSlots, nodes, active := desired.Previous, desired.Slots, desired.Nodes, desired.ActiveSlot
	m.mu.Lock()
	defer m.mu.Unlock()
	managed, e := BuildManaged(m.cfg, newSlots, nodes)
	if e != nil {
		return e
	}
	if desired.Selection.Tag != "" {
		if managed.Outbounds, e = selectOutbound(managed.Outbounds, desired.Selection.Tag); e != nil {
			return e
		}
	}
	if e = m.validateCandidate(ctx, managed); e != nil {
		return e
	}
	if !m.cfg.Xray.DynamicAPI {
		if e = atomicWrite(filepath.Join(m.cfg.Xray.ManagedDir, "04_90_kee_route_manager_outbounds.json"), managed.Outbounds, 0600); e != nil {
			return e
		}
		return m.p.RestartXray(ctx)
	}
	old := slotMap(oldSlots)
	next := slotMap(newSlots)
	order := []int{}
	for i := 0; i < m.cfg.Pool.Size; i++ {
		if i != active {
			order = append(order, i)
		}
	}
	if active >= 0 {
		order = append(order, active)
	}
	applied := []int{}
	oldOut := map[int]map[string]any{}
	for _, i := range order {
		if old[i].NodeID == next[i].NodeID {
			continue
		}
		tag := fmt.Sprintf("%s%d", m.cfg.Xray.SlotTagPrefix, i)
		oldOut[i] = blackholeOutbound(tag)
		if n, ok := nodes[old[i].NodeID]; ok {
			if previous, buildErr := Outbound(n, tag); buildErr == nil {
				oldOut[i] = previous
			}
		}
		var out map[string]any
		if n, ok := nodes[next[i].NodeID]; ok {
			out, e = Outbound(n, tag)
			if e != nil {
				return e
			}
		} else {
			out = blackholeOutbound(tag)
		}
		if e = m.replace(ctx, tag, out); e != nil {
			rollbackErr := m.replace(context.Background(), tag, oldOut[i])
			for j := len(applied) - 1; j >= 0; j-- {
				idx := applied[j]
				rollbackErr = errors.Join(rollbackErr, m.replace(context.Background(), fmt.Sprintf("%s%d", m.cfg.Xray.SlotTagPrefix, idx), oldOut[idx]))
			}
			return errors.Join(fmt.Errorf("replace slot %d: %w", i, e), rollbackErr)
		}
		applied = append(applied, i)
	}
	if e = atomicWrite(filepath.Join(m.cfg.Xray.ManagedDir, "04_90_kee_route_manager_outbounds.json"), managed.Outbounds, 0600); e != nil {
		var rollbackErr error
		for j := len(applied) - 1; j >= 0; j-- {
			idx := applied[j]
			rollbackErr = errors.Join(rollbackErr, m.replace(context.Background(), fmt.Sprintf("%s%d", m.cfg.Xray.SlotTagPrefix, idx), oldOut[idx]))
		}
		return errors.Join(e, rollbackErr)
	}
	return nil
}
func (m *Manager) replace(ctx context.Context, tag string, out map[string]any) error {
	if e := os.MkdirAll(m.cfg.Paths.RunDir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(m.cfg.Paths.RunDir, ".outbound-*.json")
	if e != nil {
		return e
	}
	path := f.Name()
	defer os.Remove(path)
	_, e = f.Write(pretty(map[string]any{"outbounds": []any{out}}))
	if e2 := f.Close(); e == nil {
		e = e2
	}
	if e != nil {
		return e
	}
	_, _ = m.r.Run(ctx, []string{m.cfg.Xray.Binary, "api", "rmo", "--server=" + m.cfg.Xray.APIAddress, tag})
	_, e = m.r.Run(ctx, []string{m.cfg.Xray.Binary, "api", "ado", "--server=" + m.cfg.Xray.APIAddress, path})
	return e
}
func (m *Manager) validateCandidate(ctx context.Context, v Managed) error {
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
	cmd := exec.CommandContext(ctx, m.cfg.Xray.Binary, "run", "-test", "-confdir", dir)
	if m.cfg.Xray.AssetDir != "" {
		cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+m.cfg.Xray.AssetDir, "xray.location.asset="+m.cfg.Xray.AssetDir)
	}
	out, e := cmd.CombinedOutput()
	if e != nil {
		return fmt.Errorf("xray validation: %w: %s", e, strings.TrimSpace(string(out)))
	}
	return nil
}
func (m *Manager) patchBaseRoute(path string) error {
	if path == "" {
		return nil
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	var root map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if e = dec.Decode(&root); e != nil {
		return fmt.Errorf("base routing file must be strict JSON: %w", e)
	}
	var extra any
	if e = dec.Decode(&extra); !errors.Is(e, io.EOF) {
		return fmt.Errorf("base routing file contains trailing JSON content")
	}

	routing, ok := root["routing"].(map[string]any)
	if !ok {
		return fmt.Errorf("routing object missing")
	}
	rules, ok := routing["rules"].([]any)
	if !ok {
		return fmt.Errorf("routing.rules missing")
	}
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
		return fmt.Errorf("no matching routing rule to adopt")
	}
	return atomicWrite(path, pretty(root), 0600)
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
func writeManaged(dir string, v Managed) error {
	files := map[string][]byte{"00_90_kee_route_manager_api.json": v.API, "03_90_kee_route_manager_inbounds.json": v.Inbounds, "04_90_kee_route_manager_outbounds.json": v.Outbounds, "05_90_kee_route_manager_routing.json": v.Routing}
	for n, b := range files {
		if e := atomicWrite(filepath.Join(dir, n), b, 0600); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) snapshot() (string, error) {
	dir, e := os.MkdirTemp(m.cfg.Paths.StateDir, "xray-snapshot-")
	if e != nil {
		return "", e
	}
	meta := map[string]bool{}
	paths := m.snapshotPaths()
	for i, p := range paths {
		if _, e = os.Stat(p); e == nil {
			meta[p] = true
			if e = copyFile(p, filepath.Join(dir, fmt.Sprintf("%d", i)), 0600); e != nil {
				os.RemoveAll(dir)
				return "", e
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			os.RemoveAll(dir)
			return "", e
		}
	}
	b, e := json.Marshal(meta)
	if e != nil {
		os.RemoveAll(dir)
		return "", e
	}
	if e = atomicWrite(filepath.Join(dir, "meta.json"), append(b, '\n'), 0600); e != nil {
		os.RemoveAll(dir)
		return "", e
	}
	list, e := json.Marshal(paths)
	if e != nil {
		os.RemoveAll(dir)
		return "", e
	}
	if e = atomicWrite(filepath.Join(dir, "paths.json"), append(list, '\n'), 0600); e != nil {
		os.RemoveAll(dir)
		return "", e
	}
	return dir, nil
}
func (m *Manager) restore(dir string) error {
	var paths []string
	var meta map[string]bool
	b, e := os.ReadFile(filepath.Join(dir, "paths.json"))
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, &paths); e != nil {
		return fmt.Errorf("invalid snapshot paths: %w", e)
	}
	b, e = os.ReadFile(filepath.Join(dir, "meta.json"))
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, &meta); e != nil {
		return fmt.Errorf("invalid snapshot metadata: %w", e)
	}
	if len(paths) == 0 || meta == nil {
		return fmt.Errorf("snapshot metadata is empty")
	}
	expected := map[string]bool{}
	for _, path := range m.snapshotPaths() {
		expected[path] = true
	}
	for i, path := range paths {
		if !expected[path] {
			return fmt.Errorf("snapshot contains unexpected path %q", path)
		}
		if meta[path] {
			source := filepath.Join(dir, fmt.Sprintf("%d", i))
			if _, statErr := os.Stat(source); statErr != nil {
				return fmt.Errorf("snapshot content for %q is missing: %w", path, statErr)
			}
			if e = copyFile(source, path, 0600); e != nil {
				return e
			}
		} else if e = os.Remove(path); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	return nil
}

func (m *Manager) snapshotPaths() []string {
	paths := make([]string, 0, 5)
	for _, name := range []string{"00_90_kee_route_manager_api.json", "03_90_kee_route_manager_inbounds.json", "04_90_kee_route_manager_outbounds.json", "05_90_kee_route_manager_routing.json"} {
		paths = append(paths, filepath.Join(m.cfg.Xray.ManagedDir, name))
	}
	if m.cfg.Xray.BaseRoutingFile != "" {
		paths = append(paths, m.cfg.Xray.BaseRoutingFile)
	}
	return paths
}

func (m *Manager) preserveOriginal(snapshot string) error {
	dst := filepath.Join(m.cfg.Paths.StateDir, "xray-original")
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := dst + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := copyDir(snapshot, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(dst))
}

func (m *Manager) RestoreOriginal(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	original := filepath.Join(m.cfg.Paths.StateDir, "xray-original")
	if _, err := os.Stat(original); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			for _, path := range m.snapshotPaths() {
				if path == m.cfg.Xray.BaseRoutingFile {
					continue
				}
				if _, statErr := os.Stat(path); statErr == nil {
					return fmt.Errorf("managed Xray artifacts exist without original snapshot; refusing destructive restore")
				} else if !errors.Is(statErr, os.ErrNotExist) {
					return statErr
				}
			}
			// A never-configured installation has nothing to restore or restart.
			return nil
		}
		return fmt.Errorf("original Xray snapshot unavailable: %w", err)
	}
	current, err := m.snapshot()
	if err != nil {
		return fmt.Errorf("snapshot current Xray configuration: %w", err)
	}
	defer os.RemoveAll(current)
	rollback := func(cause error) error {
		restoreErr := m.restore(current)
		validateErr := m.validateDir(context.Background(), m.cfg.Xray.ConfigDir)
		restartErr := m.p.RestartXray(context.Background())
		return errors.Join(cause, restoreErr, validateErr, restartErr)
	}
	if err = m.reverseRestore(original); err != nil {
		return rollback(err)
	}
	if err = m.validateDir(ctx, m.cfg.Xray.ConfigDir); err != nil {
		return rollback(fmt.Errorf("restored Xray config is invalid: %w", err))
	}
	if err = m.p.RestartXray(ctx); err != nil {
		return rollback(err)
	}
	return nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()), 0600); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(dst), ".copy-*")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	_ = f.Chmod(mode)
	_, e = io.Copy(f, in)
	if e == nil {
		e = f.Sync()
	}
	if e2 := f.Close(); e == nil {
		e = e2
	}
	if e != nil {
		return e
	}
	return os.Rename(n, dst)
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	_ = f.Chmod(mode)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	if e2 := f.Close(); e == nil {
		e = e2
	}
	if e != nil {
		return e
	}
	if e = os.Rename(n, path); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(path))
}
func slotMap(xs []model.Slot) map[int]model.Slot {
	m := map[int]model.Slot{}
	for _, x := range xs {
		m[x.Index] = x
	}
	return m
}
func overlap(v any, w map[string]bool) bool {
	a, ok := v.([]any)
	if !ok {
		return false
	}
	for _, x := range a {
		if w[stringValue(x)] {
			return true
		}
	}
	return false
}
func stringValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func selectOutbound(data []byte, tag string) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	outs, ok := root["outbounds"].([]any)
	if !ok {
		return nil, fmt.Errorf("outbounds missing")
	}
	var selected map[string]any
	for _, raw := range outs {
		out, ok := raw.(map[string]any)
		if ok && stringValue(out["tag"]) == tag {
			selected = out
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("selected outbound unavailable")
	}
	clone := map[string]any{}
	for k, v := range selected {
		clone[k] = v
	}
	clone["tag"] = selectionTag
	found := false
	for i, raw := range outs {
		if out, ok := raw.(map[string]any); ok && stringValue(out["tag"]) == selectionTag {
			outs[i] = clone
			found = true
		}
	}
	if !found {
		outs = append(outs, clone)
	}
	root["outbounds"] = outs
	return pretty(root), nil
}
func (m *Manager) checkSelectionCollision(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var root struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if err = json.Unmarshal(b, &root); err != nil {
		return fmt.Errorf("xray config must be strict JSON: %w", err)
	}
	for _, out := range root.Outbounds {
		if strings.HasPrefix(out.Tag, selectionTag) {
			return fmt.Errorf("reserved persistent selection tag conflicts with user outbound")
		}
	}
	return nil
}
func (m *Manager) Name() string { return "xray" }
func (m *Manager) Capabilities() tunnel.CoreCapabilities {
	return tunnel.CoreCapabilities{DynamicPool: m.cfg.Xray.DynamicAPI, PersistentSelection: true}
}
func (m *Manager) Select(ctx context.Context, s tunnel.Selection) error { return m.Switch(ctx, s.Tag) }
func (m *Manager) EnterDirect(ctx context.Context) error                { return m.Direct(ctx) }
func (m *Manager) ProbeEndpoint(slot int) (*url.URL, error) {
	if slot < 0 || slot >= m.cfg.Pool.Size {
		return nil, fmt.Errorf("invalid probe slot")
	}
	return url.Parse(m.SlotProxy(slot))
}
func (m *Manager) HealthEndpoint() (*url.URL, error) { return url.Parse(m.HealthProxy()) }
func (m *Manager) Ready(ctx context.Context) error   { return m.WaitReady(ctx, 3*time.Second) }
func (m *Manager) Restore(ctx context.Context) error { return m.RestoreOriginal(ctx) }
func (m *Manager) ActualState(ctx context.Context) (tunnel.ActualCoreState, error) {
	v := tunnel.ActualCoreState{Running: m.p.XrayRunning(ctx)}
	path := filepath.Join(m.cfg.Xray.ManagedDir, "04_90_kee_route_manager_outbounds.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	var root struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err = json.Unmarshal(b, &root); err != nil {
		return v, err
	}
	v.Configured = true
	hash := sha256.New()
	for _, path := range m.snapshotPaths() {
		if path == m.cfg.Xray.BaseRoutingFile {
			continue
		}
		data, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			v.Configured = false
			return v, nil
		}
		if readErr != nil {
			return v, readErr
		}
		hash.Write([]byte(filepath.Base(path)))
		hash.Write(data)
	}
	v.ConfigHash = fmt.Sprintf("%x", hash.Sum(nil))
	if err = m.checkAdoptedRouting(); err != nil {
		v.Drift = err.Error()
	}
	for _, out := range root.Outbounds {
		if stringValue(out["tag"]) == selectionTag {
			for _, candidate := range root.Outbounds {
				tag := stringValue(candidate["tag"])
				if tag == selectionTag {
					continue
				}
				a := map[string]any{}
				for k, value := range candidate {
					a[k] = value
				}
				a["tag"] = selectionTag
				if bytes.Equal(pretty(a), pretty(out)) {
					v.Selection.Tag = tag
					return v, nil
				}
			}
		}
	}
	return v, fmt.Errorf("persistent selection missing or has drifted")
}

var _ tunnel.TunnelCore = (*Manager)(nil)

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// reverseRestore restores only KRM-owned managed fragments and the route targets
// it adopted. Other user routing fields/rules remain exactly as currently edited.
func (m *Manager) reverseRestore(snapshot string) error {
	var paths []string
	var meta map[string]bool
	b, err := os.ReadFile(filepath.Join(snapshot, "paths.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &paths); err != nil {
		return err
	}
	b, err = os.ReadFile(filepath.Join(snapshot, "meta.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &meta); err != nil {
		return err
	}
	expected := map[string]bool{}
	for _, path := range m.snapshotPaths() {
		expected[path] = true
	}
	for i, path := range paths {
		if !expected[path] {
			return fmt.Errorf("snapshot contains unexpected path")
		}
		if path == m.cfg.Xray.BaseRoutingFile {
			if !meta[path] {
				return fmt.Errorf("original routing unavailable")
			}
			if err = m.reverseBaseRoute(filepath.Join(snapshot, fmt.Sprint(i)), path); err != nil {
				return err
			}
			continue
		}
		if meta[path] {
			if err = copyFile(filepath.Join(snapshot, fmt.Sprint(i)), path, 0600); err != nil {
				return err
			}
		} else {
			if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
func routeIdentity(rule map[string]any) string {
	identity := map[string]any{}
	for k, v := range rule {
		if k != "outboundTag" && k != "balancerTag" {
			identity[k] = v
		}
	}
	return string(pretty(identity))
}
func (m *Manager) reverseBaseRoute(originalPath, currentPath string) error {
	load := func(path string) (map[string]any, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var root map[string]any
		err = json.Unmarshal(b, &root)
		return root, err
	}
	original, err := load(originalPath)
	if err != nil {
		return err
	}
	current, err := load(currentPath)
	if err != nil {
		return err
	}
	origRouting, ok := original["routing"].(map[string]any)
	if !ok {
		return fmt.Errorf("original routing missing")
	}
	curRouting, ok := current["routing"].(map[string]any)
	if !ok {
		return fmt.Errorf("current routing missing")
	}
	originals := map[string]map[string]any{}
	origRules, ok := origRouting["rules"].([]any)
	if !ok {
		return fmt.Errorf("original routing rules missing")
	}
	for _, raw := range origRules {
		rule, ok := raw.(map[string]any)
		if ok {
			originals[routeIdentity(rule)] = rule
		}
	}
	curRules, ok := curRouting["rules"].([]any)
	if !ok {
		return fmt.Errorf("current routing rules missing")
	}
	for _, raw := range curRules {
		rule, ok := raw.(map[string]any)
		if !ok || stringValue(rule["balancerTag"]) != m.cfg.Xray.BalancerTag {
			continue
		}
		previous, ok := originals[routeIdentity(rule)]
		if !ok {
			return fmt.Errorf("adopted routing rule has changed; refusing destructive restore")
		}
		delete(rule, "balancerTag")
		delete(rule, "outboundTag")
		if tag, ok := previous["outboundTag"]; ok {
			rule["outboundTag"] = tag
		}
		if tag, ok := previous["balancerTag"]; ok {
			rule["balancerTag"] = tag
		}
	}
	return atomicWrite(currentPath, pretty(current), 0600)
}

func (m *Manager) checkAdoptedRouting() error {
	if m.cfg.Xray.BaseRoutingFile == "" {
		return nil
	}
	originalDir := filepath.Join(m.cfg.Paths.StateDir, "xray-original")
	b, err := os.ReadFile(filepath.Join(originalDir, "paths.json"))
	if err != nil {
		return fmt.Errorf("routing ownership snapshot unavailable: %w", err)
	}
	var paths []string
	if err = json.Unmarshal(b, &paths); err != nil {
		return err
	}
	originalPath := ""
	for i, path := range paths {
		if path == m.cfg.Xray.BaseRoutingFile {
			originalPath = filepath.Join(originalDir, fmt.Sprint(i))
		}
	}
	if originalPath == "" {
		return fmt.Errorf("original routing ownership missing")
	}
	rules := func(path string) ([]any, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var root map[string]any
		if err = json.Unmarshal(data, &root); err != nil {
			return nil, err
		}
		routing, ok := root["routing"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("routing missing")
		}
		values, ok := routing["rules"].([]any)
		if !ok {
			return nil, fmt.Errorf("routing rules missing")
		}
		return values, nil
	}
	original, err := rules(originalPath)
	if err != nil {
		return err
	}
	current, err := rules(m.cfg.Xray.BaseRoutingFile)
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, tag := range m.cfg.Xray.Route.InboundTags {
		wanted[tag] = true
	}
	replacements := map[string]bool{}
	for _, tag := range m.cfg.Xray.Route.ReplaceOutboundTags {
		replacements[tag] = true
	}
	for _, raw := range original {
		originalRule, ok := raw.(map[string]any)
		if !ok || !replacements[stringValue(originalRule["outboundTag"])] || !overlap(originalRule["inboundTag"], wanted) {
			continue
		}
		matched := false
		for _, rawCurrent := range current {
			rule, ok := rawCurrent.(map[string]any)
			if ok && routeIdentity(rule) == routeIdentity(originalRule) {
				if stringValue(rule["balancerTag"]) != m.cfg.Xray.BalancerTag || stringValue(rule["outboundTag"]) != "" {
					return fmt.Errorf("adopted routing target drift detected")
				}
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("adopted routing rule drift detected")
		}
	}
	return nil
}
