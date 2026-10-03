package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

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
	return m.installManaged(ctx, managed, initialTag)
}

// installManaged replaces the complete generated configuration while m.mu is held.
func (m *Manager) installManaged(ctx context.Context, managed Managed, initialTag string) error {
	backup, e := m.snapshot()
	if e != nil {
		return e
	}
	defer os.RemoveAll(backup)
	if e = m.preserveOriginal(backup); e != nil {
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
	if path := m.cfg.Xray.BaseRoutingFile; path != "" {
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		planned, err := m.patchedBaseRoute(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, planned) {
			return m.installManaged(ctx, managed, desired.Selection.Tag)
		}
	}
	// Ports, API and routing topology cannot be changed by outbound replacement.
	// Refresh all generated files and runtime together when those settings change.
	for name, desiredBytes := range map[string][]byte{
		"00_90_kee_route_manager_api.json":      managed.API,
		"03_90_kee_route_manager_inbounds.json": managed.Inbounds,
		"05_90_kee_route_manager_routing.json":  managed.Routing,
	} {
		current, err := os.ReadFile(filepath.Join(m.cfg.Xray.ManagedDir, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err != nil || !bytes.Equal(current, desiredBytes) {
			return m.installManaged(ctx, managed, desired.Selection.Tag)
		}
	}
	if !m.cfg.Xray.DynamicAPI {
		return m.installManaged(ctx, managed, desired.Selection.Tag)
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
	// Capture exact persisted definitions before any runtime mutation. Next nodes
	// may legitimately omit an evicted node, and rebuilding loses custom fields.
	previousBytes, e := os.ReadFile(filepath.Join(m.cfg.Xray.ManagedDir, "04_90_kee_route_manager_outbounds.json"))
	if e != nil {
		return e
	}
	var previousConfig struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if e = json.Unmarshal(previousBytes, &previousConfig); e != nil {
		return e
	}
	oldOut := map[int]map[string]any{}
	for _, i := range order {
		if old[i].NodeID == next[i].NodeID {
			continue
		}
		tag := fmt.Sprintf("%s%d", m.cfg.Xray.SlotTagPrefix, i)
		for _, out := range previousConfig.Outbounds {
			if stringValue(out["tag"]) == tag {
				if oldOut[i] != nil {
					return fmt.Errorf("duplicate previous outbound %s", tag)
				}
				oldOut[i] = out
			}
		}
		if oldOut[i] == nil {
			return fmt.Errorf("previous outbound %s unavailable; refusing pool mutation", tag)
		}
	}
	for _, i := range order {
		if old[i].NodeID == next[i].NodeID {
			continue
		}
		tag := fmt.Sprintf("%s%d", m.cfg.Xray.SlotTagPrefix, i)
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