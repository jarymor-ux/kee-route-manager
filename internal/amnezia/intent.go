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
