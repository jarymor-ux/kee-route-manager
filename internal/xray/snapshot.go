package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

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
	targets, e := m.snapshotTargets(paths)
	if e != nil {
		return e
	}
	for i, path := range paths {
		target := targets[i]
		if meta[path] {
			source := filepath.Join(dir, fmt.Sprintf("%d", i))
			if _, statErr := os.Stat(source); statErr != nil {
				return fmt.Errorf("snapshot content for %q is missing: %w", path, statErr)
			}
			if e = copyFile(source, target, 0600); e != nil {
				return e
			}
		} else if e = os.Remove(target); e != nil && !errors.Is(e, os.ErrNotExist) {
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
	if e = os.Rename(n, dst); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(dst))
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
