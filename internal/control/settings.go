package control

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
)

// Settings changes rebuild managers under the original daemon ownership locks.
// No UI/launcher process or executable release identity is changed.
type settingsRuntime struct {
	mu       sync.Mutex
	editor   *config.SettingsEditor
	current  config.Config
	tx       *config.SettingsTransaction
	snapshot config.SettingsSnapshot
	status   web.SettingsApplyStatus
	activate chan struct{}
	sent     bool
	server   *web.Server
}

func newSettingsRuntime(c config.Config) *settingsRuntime {
	return &settingsRuntime{editor: config.NewSettingsEditor(c.SourcePath()), current: c, status: web.SettingsApplyStatus{Status: "idle"}}
}

func sameSettingsInfrastructure(current, candidate config.Config) bool {
	rebased, err := config.SettingsFromConfig(current).ApplyTo(candidate)
	return err == nil && reflect.DeepEqual(current, rebased)
}

func (s *settingsRuntime) unchangedInfrastructure() error {
	disk, err := config.Load(s.current.SourcePath())
	if err != nil {
		return config.ErrSettingsUnavailable
	}
	// Even visible fields can be edited externally. Never report disk-only
	// values as applied, or treat saving them as a runtime no-op.
	if !reflect.DeepEqual(s.current, disk) {
		return config.ErrSettingsConflict
	}
	return nil
}

func (s *settingsRuntime) SettingsSnapshot() (config.SettingsSnapshot, web.SettingsApplyStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx != nil {
		return s.snapshot, s.status, nil
	}
	if err := s.unchangedInfrastructure(); err != nil {
		return config.SettingsSnapshot{}, s.status, err
	}
	snapshot, err := s.editor.Snapshot()
	return snapshot, s.status, err
}

func (s *settingsRuntime) ValidateSettings(settings config.EditableSettings, revision string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx != nil {
		return config.ErrSettingsBusy
	}
	if err := s.unchangedInfrastructure(); err != nil {
		return err
	}
	return s.editor.Validate(settings, revision)
}

func (s *settingsRuntime) SaveSettings(settings config.EditableSettings, revision string) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx != nil {
		return false, "", config.ErrSettingsBusy
	}
	if err := s.unchangedInfrastructure(); err != nil {
		return false, "", err
	}
	tx, err := s.editor.Stage(settings, revision)
	if err != nil {
		// Failed staging can leave durable intent; undo only matching owned data.
		if recoverErr := s.editor.Recover(); recoverErr != nil {
			return false, "", recoverErr
		}
		return false, "", err
	}
	if !sameSettingsInfrastructure(s.current, tx.Config) {
		if err := tx.Rollback(); err != nil {
			return false, "", err
		}
		return false, "", config.ErrSettingsConflict
	}
	if !tx.Changed {
		return false, tx.Revision(), nil
	}
	s.tx = tx
	s.snapshot = config.SettingsSnapshot{Settings: config.SettingsFromConfig(tx.Config), Revision: tx.Revision(), PoolSize: tx.Config.Pool.Size}
	s.status = web.SettingsApplyStatus{Status: "applying", Revision: tx.Revision()}
	return true, tx.Revision(), nil
}

func (s *settingsRuntime) ActivateSettings() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx != nil && !s.sent && s.activate != nil {
		s.sent = true
		close(s.activate)
	}
}

func (s *settingsRuntime) applying() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tx != nil
}

func (s *settingsRuntime) ready(tx *config.SettingsTransaction, readiness error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if readiness != nil {
		return fmt.Errorf("settings runtime reconciliation failed")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.current = tx.Config
	s.tx = nil
	s.status = web.SettingsApplyStatus{Status: "applied", Revision: tx.Revision()}
	return nil
}

func (s *settingsRuntime) rollback() (config.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tx == nil {
		return s.current, nil
	}
	revision := s.tx.Revision()
	if err := s.tx.Rollback(); err != nil {
		s.status = web.SettingsApplyStatus{Status: "failed", Error: "configuration recovery requires operator reconciliation", Revision: revision}
		return config.Config{}, err
	}
	s.tx = nil
	s.status = web.SettingsApplyStatus{Status: "rolled_back", Error: "settings could not be applied; previous configuration restored", Revision: revision}
	return s.current, nil
}

func serveConfigured(ctx context.Context, c config.Config, version string) error {
	if c.SourcePath() == "" {
		_, err := serveRuntime(ctx, c, version, nil, nil, false)
		return err
	}
	s := newSettingsRuntime(c)
	// Called only after both original ownership locks have been acquired.
	if err := s.editor.Recover(); err != nil {
		return err
	}
	var err error
	lockedConfig := c
	c, err = config.Load(c.SourcePath())
	if err != nil {
		return fmt.Errorf("controller configuration unavailable after recovery")
	}
	if !sameSettingsInfrastructure(lockedConfig, c) {
		return fmt.Errorf("controller infrastructure changed after ownership acquisition")
	}
	s.current = c
	var candidate *config.SettingsTransaction
	reloading := false
	for {
		s.mu.Lock()
		s.activate, s.sent = make(chan struct{}), false
		s.mu.Unlock()
		reload, runErr := serveRuntime(ctx, c, version, s, candidate, reloading)
		if ctx.Err() != nil {
			if _, err := s.rollback(); err != nil {
				return err
			}
			return runErr
		}
		s.mu.Lock()
		next := s.tx
		s.mu.Unlock()
		if next == nil {
			return runErr
		}
		if runErr != nil || !reload {
			c, err = s.rollback()
			if err != nil {
				return err
			}
			candidate = nil
		} else {
			c, candidate = next.Config, next
		}
		reloading = true
	}
}
