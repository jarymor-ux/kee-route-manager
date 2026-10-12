package amnezia

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type runtimeIntent struct {
	ProfileID string `json:"profile_id"`
	Interface string `json:"interface"`
	Table     string `json:"table"`
	Phase     string `json:"phase"`
}

func (r *Runtime) intentPath(id string) string {
	return filepath.Join(r.dir, deviceName(id)+".intent.json")
}

// The intent contains no tunnel keys. The atomic private write is made
// durable before the corresponding network command may run.
func (r *Runtime) writeIntent(id, phase string) error {
	if !profileIDPattern.MatchString(id) {
		return errors.New("invalid AWG intent profile ID")
	}
	if phase != "starting" && phase != "active" && phase != "stopping" {
		return errors.New("invalid AWG intent phase")
	}
	table, err := tableNumber(id)
	if err != nil {
		return err
	}
	data, err := json.Marshal(runtimeIntent{
		ProfileID: id,
		Interface: deviceName(id),
		Table: table,
		Phase: phase,
	})
	if err != nil {
		return err
	}
	if err := atomicPrivateWrite(r.dir, r.intentPath(id), data); err != nil {
		return fmt.Errorf("persist AWG lifecycle intent: %w", err)
	}
	return nil
}

func (r *Runtime) removeIntent(id string) error {
	if err := os.Remove(r.intentPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (r *Runtime) validateIntent(id string) error {
	path := r.intentPath(id)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		// Pre-journal installations may retain only their private config.
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return errors.New("unsafe AWG lifecycle intent")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var saved runtimeIntent
	if err := json.Unmarshal(data, &saved); err != nil {
		return fmt.Errorf("decode AWG lifecycle intent: %w", err)
	}
	table, err := tableNumber(id)
	if err != nil {
		return err
	}
	if saved.ProfileID != id || saved.Interface != deviceName(id) || saved.Table != table ||
		(saved.Phase != "starting" && saved.Phase != "active" && saved.Phase != "stopping") {
		return errors.New("AWG lifecycle intent does not match managed profile")
	}
	return nil
}
