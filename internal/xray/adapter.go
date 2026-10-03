package xray

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

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
	m.mu.Lock()
	defer m.mu.Unlock()
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
	targets, err := m.snapshotTargets(paths)
	if err != nil {
		return err
	}
	for i, path := range paths {
		target := targets[i]
		if target == m.cfg.Xray.BaseRoutingFile {
			if !meta[path] {
				return fmt.Errorf("original routing unavailable")
			}
			if err = m.reverseBaseRoute(filepath.Join(snapshot, fmt.Sprint(i)), target); err != nil {
				return err
			}
			continue
		}
		if meta[path] {
			if err = copyFile(filepath.Join(snapshot, fmt.Sprint(i)), target, 0600); err != nil {
				return err
			}
		} else {
			if err = os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
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
	original, err := readRoutingRoot(originalPath)
	if err != nil {
		return err
	}
	current, err := readRoutingRoot(currentPath)
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
	if err = m.rejectOriginalRoutingCollision(original); err != nil {
		return err
	}
	removed, err := m.stripManagedRouting(curRouting, false)
	if err != nil {
		return err
	}
	if removed && len(curRouting["balancers"].([]any)) == 0 {
		if originalBalancers, existed := origRouting["balancers"]; existed {
			if values, _ := originalBalancers.([]any); len(values) == 0 {
				curRouting["balancers"] = originalBalancers
			}
		} else {
			delete(curRouting, "balancers")
		}
	}
	originals := map[string][]map[string]any{}
	origRules, ok := origRouting["rules"].([]any)
	if !ok {
		return fmt.Errorf("original routing rules missing")
	}
	wanted, replacements := map[string]bool{}, map[string]bool{}
	for _, tag := range m.cfg.Xray.Route.InboundTags {
		wanted[tag] = true
	}
	for _, tag := range m.cfg.Xray.Route.ReplaceOutboundTags {
		replacements[tag] = true
	}
	for _, raw := range origRules {
		rule, ok := raw.(map[string]any)
		if ok && replacements[stringValue(rule["outboundTag"])] && overlap(rule["inboundTag"], wanted) {
			key := routeIdentity(rule)
			originals[key] = append(originals[key], rule)
		}
	}
	curRules, ok := curRouting["rules"].([]any)
	if !ok {
		return fmt.Errorf("current routing rules missing")
	}
	counts := map[string]int{}
	for _, raw := range curRules {
		if rule, ok := raw.(map[string]any); ok {
			key := routeIdentity(rule)
			if len(originals[key]) > 0 {
				counts[key]++
			}
		}
	}
	for key, rules := range originals {
		if counts[key] != len(rules) {
			return fmt.Errorf("adopted routing rule multiplicity changed; refusing destructive restore")
		}
	}
	for _, raw := range curRules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key := routeIdentity(rule)
		previousRules := originals[key]
		adopted := stringValue(rule["balancerTag"]) == m.cfg.Xray.BalancerTag
		if len(previousRules) == 0 {
			if adopted {
				return fmt.Errorf("adopted routing rule has changed; refusing destructive restore")
			}
			continue
		}
		previous := previousRules[0]
		originals[key] = previousRules[1:]
		if !adopted {
			if stringValue(rule["outboundTag"]) != stringValue(previous["outboundTag"]) || stringValue(rule["balancerTag"]) != stringValue(previous["balancerTag"]) {
				return fmt.Errorf("adopted routing target drift detected; refusing destructive restore")
			}
			continue // This occurrence was already restored before an interruption.
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
	if err := m.checkRoutingFiles(); err != nil {
		return err
	}
	if m.cfg.Xray.BaseRoutingFile == "" {
		return nil
	}
	originalRoot, err := m.originalBaseRouting()
	if err != nil {
		return fmt.Errorf("routing ownership snapshot unavailable: %w", err)
	}
	if originalRoot == nil {
		return fmt.Errorf("original routing ownership missing")
	}
	if err = m.rejectOriginalRoutingCollision(originalRoot); err != nil {
		return err
	}
	currentRoot, err := readRoutingRoot(m.cfg.Xray.BaseRoutingFile)
	if err != nil {
		return err
	}
	currentRouting, ok := currentRoot["routing"].(map[string]any)
	if !ok {
		return fmt.Errorf("routing missing")
	}
	owned, err := m.stripManagedRouting(currentRouting, true)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("managed routing additions missing")
	}
	rules := func(root map[string]any) ([]any, error) {
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
	original, err := rules(originalRoot)
	if err != nil {
		return err
	}
	current, err := rules(currentRoot)
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
	identities := map[string]int{}
	for _, raw := range original {
		originalRule, ok := raw.(map[string]any)
		if !ok || !replacements[stringValue(originalRule["outboundTag"])] || !overlap(originalRule["inboundTag"], wanted) {
			continue
		}
		identities[routeIdentity(originalRule)]++
	}
	counts := map[string]int{}
	for _, raw := range current {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key := routeIdentity(rule)
		if identities[key] == 0 {
			if stringValue(rule["balancerTag"]) == m.cfg.Xray.BalancerTag {
				return fmt.Errorf("unexpected adopted routing rule detected")
			}
			continue
		}
		if stringValue(rule["balancerTag"]) != m.cfg.Xray.BalancerTag || stringValue(rule["outboundTag"]) != "" {
			return fmt.Errorf("adopted routing target drift detected")
		}
		counts[key]++
	}
	for key, want := range identities {
		if counts[key] != want {
			return fmt.Errorf("adopted routing rule multiplicity drift detected")
		}
	}
	return nil
}
