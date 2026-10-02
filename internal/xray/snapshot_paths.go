package xray

import (
	"fmt"
	"path/filepath"
)

// Directory aliases identify the same owned file. Keep the filename in the
// identity: replacing an individual file symlink does not replace its target.
func snapshotPathIdentity(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("snapshot path must be absolute")
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("resolve snapshot directory: %w", err)
	}
	return filepath.Join(dir, filepath.Base(path)), nil
}

// Resolve the entire recorded write set before any effects. Only one recorded
// identity may map to each configured target; aliases cannot expand ownership.
func (m *Manager) snapshotTargets(recorded []string) ([]string, error) {
	expected := m.snapshotPaths()
	if len(recorded) != len(expected) {
		return nil, fmt.Errorf("incomplete snapshot paths")
	}
	byIdentity := make(map[string]string, len(expected))
	for _, path := range expected {
		identity, err := snapshotPathIdentity(path)
		if err != nil {
			return nil, err
		}
		if _, exists := byIdentity[identity]; exists {
			return nil, fmt.Errorf("duplicate configured snapshot target")
		}
		byIdentity[identity] = path
	}
	targets := make([]string, len(recorded))
	for i, path := range recorded {
		identity, err := snapshotPathIdentity(path)
		if err != nil {
			return nil, err
		}
		target, ok := byIdentity[identity]
		if !ok {
			return nil, fmt.Errorf("snapshot contains unexpected or duplicate path")
		}
		targets[i] = target
		delete(byIdentity, identity)
	}
	return targets, nil
}
