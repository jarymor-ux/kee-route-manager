package xray

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

func fileIdentity(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return dataIdentity(b), nil
}
func dataIdentity(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// PrepareReplay computes the complete write set before journal intent is saved.
// Planning uses private temporary files so the same route transformation is used
// for proof and effects, including preservation of unrelated operator fields.
func (m *Manager) PrepareReplay(ctx context.Context, desired tunnel.DesiredPool, restore bool) (tunnel.ReplayProof, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !restore {
		if err := m.checkRoutingFiles(); err != nil {
			return nil, err
		}
	}
	proof := tunnel.ReplayProof{}
	for _, path := range m.snapshotPaths() {
		hash, err := fileIdentity(path)
		if err != nil {
			return nil, err
		}
		proof[path] = tunnel.ReplayFile{Before: hash, After: hash}
	}
	tmp, err := os.MkdirTemp(m.cfg.Paths.StateDir, ".replay-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if restore {
		original := filepath.Join(m.cfg.Paths.StateDir, "xray-original")
		b, err := os.ReadFile(filepath.Join(original, "paths.json"))
		if errors.Is(err, os.ErrNotExist) {
			for path, f := range proof {
				if path != m.cfg.Xray.BaseRoutingFile && f.Before != "" {
					return nil, fmt.Errorf("managed Xray artifacts exist without original snapshot; refusing destructive restore")
				}
			}
			return proof, nil
		}
		if err != nil {
			return nil, err
		}
		var paths []string
		if err = json.Unmarshal(b, &paths); err != nil {
			return nil, err
		}
		b, err = os.ReadFile(filepath.Join(original, "meta.json"))
		if err != nil {
			return nil, err
		}
		var meta map[string]bool
		if err = json.Unmarshal(b, &meta); err != nil {
			return nil, err
		}
		if len(paths) != len(proof) || meta == nil {
			return nil, fmt.Errorf("invalid original replay paths")
		}
		targets, err := m.snapshotTargets(paths)
		if err != nil {
			return nil, err
		}
		for i, path := range paths {
			target := targets[i]
			f := proof[target]
			if target == m.cfg.Xray.BaseRoutingFile {
				if !meta[path] {
					return nil, fmt.Errorf("original routing unavailable")
				}
				planned := filepath.Join(tmp, "routing.json")
				if err = copyFile(target, planned, 0600); err != nil {
					return nil, err
				}
				if err = m.reverseBaseRoute(filepath.Join(original, fmt.Sprint(i)), planned); err != nil {
					return nil, err
				}
				f.After, err = fileIdentity(planned)
			} else if meta[path] {
				f.After, err = fileIdentity(filepath.Join(original, fmt.Sprint(i)))
				if err == nil && f.After == "" {
					return nil, fmt.Errorf("original snapshot content missing")
				}
			} else {
				f.After = ""
			}
			if err != nil {
				return nil, err
			}
			proof[target] = f
		}
		return proof, nil
	}
	managed, err := BuildManaged(m.cfg, desired.Slots, desired.Nodes)
	if err != nil {
		return nil, err
	}
	if desired.Selection.Tag != "" {
		managed.Outbounds, err = selectOutbound(managed.Outbounds, desired.Selection.Tag)
		if err != nil {
			return nil, err
		}
	}
	outputs := map[string][]byte{"00_90_kee_route_manager_api.json": managed.API, "03_90_kee_route_manager_inbounds.json": managed.Inbounds, "04_90_kee_route_manager_outbounds.json": managed.Outbounds, "05_90_kee_route_manager_routing.json": managed.Routing}
	for name, b := range outputs {
		path := filepath.Join(m.cfg.Xray.ManagedDir, name)
		f := proof[path]
		f.After = dataIdentity(b)
		proof[path] = f
	}
	if path := m.cfg.Xray.BaseRoutingFile; path != "" {
		planned := filepath.Join(tmp, "routing.json")
		if err = copyFile(path, planned, 0600); err != nil {
			return nil, err
		}
		if err = m.patchBaseRoute(planned); err != nil {
			return nil, err
		}
		f := proof[path]
		f.After, err = fileIdentity(planned)
		if err != nil {
			return nil, err
		}
		proof[path] = f
	}
	return proof, nil
}

func (m *Manager) ValidateReplay(ctx context.Context, proof tunnel.ReplayProof) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	recorded := make([]string, 0, len(proof))
	for path := range proof {
		recorded = append(recorded, path)
	}
	targets, err := m.snapshotTargets(recorded)
	if err != nil {
		return err
	}
	for i, path := range targets {
		f := proof[recorded[i]]
		hash, err := fileIdentity(path)
		if err != nil {
			return err
		}
		if hash != f.Before && hash != f.After {
			return fmt.Errorf("managed tunnel file drift detected before journal replay: %s", filepath.Base(path))
		}
	}
	return nil
}

var _ tunnel.ReplayGuard = (*Manager)(nil)
