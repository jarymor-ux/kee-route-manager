package amnezia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Reconcile checks stale private AWG runtime files against current profiles and
// the live kernel. It ONLY removes recovery files when every corresponding
// kernel resource is confirmed absent. Ambiguous or partial states are kept
// intact for explicit recovery; their interfaces are never adopted blindly.
func (r *Runtime) Reconcile(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.runner.(kernelInspector); !ok {
		return errors.New("AWG kernel reconciliation requires read-only Linux inspection")
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return fmt.Errorf("list AWG recovery files: %w", err)
	}
	profiles, err := r.store.List()
	if err != nil {
		return fmt.Errorf("list AWG recovery profiles: %w", err)
	}
	var failures []error
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".conf")
		path := filepath.Join(r.dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			failures = append(failures, fmt.Errorf("inspect recovery file %s: %w", name, err))
			continue
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 256*1024 {
			failures = append(failures, fmt.Errorf("unsafe AWG recovery file %s", name))
			continue
		}
		var id string
		ambiguous := false
		for _, profile := range profiles {
			if deviceName(profile.ID) == name {
				if id != "" {
					ambiguous = true
				}
				id = profile.ID
			}
		}
		if id == "" || ambiguous {
			failures = append(failures, fmt.Errorf("AWG recovery file %s has no unique managed profile", name))
			continue
		}
		if r.active[id] {
			// Current-process interfaces must only be removed through Stop.
			continue
		}
		saved, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, fmt.Errorf("read AWG recovery %s: %w", name, err))
			continue
		}
		raw, err := r.store.Read(id)
		if err != nil {
			failures = append(failures, fmt.Errorf("read AWG profile %s: %w", name, err))
			continue
		}
		expected, err := isolatedConfig(raw)
		if err != nil || !bytes.Equal(expected, saved) {
			failures = append(failures, fmt.Errorf("AWG recovery config %s differs from current managed profile", name))
			continue
		}
		if err := r.validateIntent(id); err != nil {
			failures = append(failures, fmt.Errorf("AWG recovery %s has invalid lifecycle intent: %w", name, err))
			continue
		}
		table, err := tableNumber(id)
		if err == nil {
			err = inspectKernelAvailability(ctx, r.runner, name, table)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("AWG recovery %s remains pending: %w", name, err))
			continue
		}
		// No interface, route or policy rule exists for this identity. This
		// path does not mutate live networking and is safe after reboot.
		if err := r.removeIntent(id); err != nil {
			failures = append(failures, fmt.Errorf("release AWG recovery intent %s: %w", name, err))
			continue
		}
		if err := os.Remove(path); err != nil {
			failures = append(failures, fmt.Errorf("release stale AWG recovery %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}
