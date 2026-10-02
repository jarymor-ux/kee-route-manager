package xray

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func readRoutingRoot(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err = decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("routing file must be strict JSON: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("routing file contains trailing JSON content")
	}
	return root, nil
}

// A second routing-bearing file replaces the complete authoritative section in
// Xray. Refuse ambiguous ownership instead of silently discarding user rules.
func (m *Manager) checkRoutingFiles() error {
	entries, err := os.ReadDir(m.cfg.Xray.ConfigDir)
	if err != nil {
		return err
	}
	managed := map[string]bool{}
	for _, path := range m.snapshotPaths() {
		managed[filepath.Base(path)] = true
	}
	for _, entry := range entries {
		if entry.IsDir() || managed[entry.Name()] || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		root, err := readRoutingRoot(filepath.Join(m.cfg.Xray.ConfigDir, entry.Name()))
		if err != nil {
			return err
		}
		if root["routing"] != nil {
			return fmt.Errorf("routing section outside configured base file: %s", entry.Name())
		}
	}
	return nil
}

func (m *Manager) originalBaseRouting() (map[string]any, error) {
	dir := filepath.Join(m.cfg.Paths.StateDir, "xray-original")
	data, err := os.ReadFile(filepath.Join(dir, "paths.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	if err = json.Unmarshal(data, &paths); err != nil {
		return nil, err
	}
	targets, err := m.snapshotTargets(paths)
	if err != nil {
		return nil, err
	}
	for i, path := range targets {
		if path == m.cfg.Xray.BaseRoutingFile {
			return readRoutingRoot(filepath.Join(dir, fmt.Sprint(i)))
		}
	}
	return nil, fmt.Errorf("original routing ownership missing")
}

func (m *Manager) reservedRoutingRule(rule map[string]any) bool {
	tags, _ := rule["inboundTag"].([]any)
	if tag, ok := rule["inboundTag"].(string); ok {
		tags = []any{tag}
	}
	for _, tag := range tags {
		value := stringValue(tag)
		if value == m.cfg.Xray.APITag || value == "krm-health" || strings.HasPrefix(value, "krm-probe-") {
			return true
		}
	}
	return false
}

func (m *Manager) rejectOriginalRoutingCollision(root map[string]any) error {
	routing, ok := root["routing"].(map[string]any)
	if !ok {
		return fmt.Errorf("original routing missing")
	}
	rules, _ := routing["rules"].([]any)
	for _, raw := range rules {
		if rule, ok := raw.(map[string]any); ok && (m.reservedRoutingRule(rule) || stringValue(rule["balancerTag"]) == m.cfg.Xray.BalancerTag) {
			return fmt.Errorf("reserved managed routing rule conflicts with original routing")
		}
	}
	balancers, _ := routing["balancers"].([]any)
	for _, raw := range balancers {
		if balancer, ok := raw.(map[string]any); ok && stringValue(balancer["tag"]) == m.cfg.Xray.BalancerTag {
			return fmt.Errorf("reserved managed balancer conflicts with original routing")
		}
	}
	return nil
}

// stripManagedRouting recognizes exact generated values, never ownership by tag
// alone. Missing all additions is allowed for legacy snapshots and idempotent
// restore; partial or edited additions are ambiguous and must be refused.
func (m *Manager) stripManagedRouting(routing map[string]any, requirePrefix bool) (bool, error) {
	expected := managedRouting(m.cfg)
	expectedRules := expected["rules"].([]any)
	remaining := []any{}
	seen := map[string]bool{}
	wanted := map[string]bool{}
	for _, rule := range expectedRules {
		wanted[string(pretty(rule))] = true
	}
	rules, ok := routing["rules"].([]any)
	if !ok {
		return false, fmt.Errorf("routing rules missing")
	}
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok || !m.reservedRoutingRule(rule) {
			remaining = append(remaining, raw)
			continue
		}
		key := string(pretty(rule))
		if !wanted[key] || seen[key] {
			return false, fmt.Errorf("managed routing rule drift detected")
		}
		seen[key] = true
	}
	balancers := []any{}
	if raw := routing["balancers"]; raw != nil {
		balancers, ok = raw.([]any)
		if !ok {
			return false, fmt.Errorf("routing balancers must be an array")
		}
	}
	remainingBalancers := []any{}
	foundBalancer := false
	for _, raw := range balancers {
		balancer, ok := raw.(map[string]any)
		if !ok || stringValue(balancer["tag"]) != m.cfg.Xray.BalancerTag {
			remainingBalancers = append(remainingBalancers, raw)
			continue
		}
		if foundBalancer || !bytes.Equal(pretty(raw), pretty(expected["balancers"].([]any)[0])) {
			return false, fmt.Errorf("managed routing balancer drift detected")
		}
		foundBalancer = true
	}
	if len(seen) == 0 && !foundBalancer {
		return false, nil
	}
	if len(seen) != len(expectedRules) || !foundBalancer {
		return false, fmt.Errorf("managed routing additions are incomplete")
	}
	if requirePrefix {
		for i, expected := range expectedRules {
			if !bytes.Equal(pretty(rules[i]), pretty(expected)) {
				return false, fmt.Errorf("managed routing rules no longer precede user rules")
			}
		}
	}
	routing["rules"] = remaining
	routing["balancers"] = remainingBalancers
	return true, nil
}

func (m *Manager) prepareBaseRouting(root map[string]any) error {
	routing, ok := root["routing"].(map[string]any)
	if !ok {
		return fmt.Errorf("routing object missing")
	}
	original, err := m.originalBaseRouting()
	if err != nil {
		return err
	}
	if original == nil {
		if err = m.rejectOriginalRoutingCollision(root); err != nil {
			return err
		}
	} else {
		if err = m.rejectOriginalRoutingCollision(original); err != nil {
			return err
		}
		if _, err = m.stripManagedRouting(routing, true); err != nil {
			return err
		}
	}
	if _, ok := routing["rules"].([]any); !ok {
		return fmt.Errorf("routing.rules missing")
	}
	if raw := routing["balancers"]; raw != nil {
		if _, ok := raw.([]any); !ok {
			return fmt.Errorf("routing balancers must be an array")
		}
	}
	return nil
}
