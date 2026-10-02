package core

import "fmt"

// PrepareUpdate stops admission, joins every scheduler and operation, and flushes
// the single owner's state. It is irreversible for this Manager: on any failure
// the launcher must restart the old daemon, never resume this instance's loops.
func (m *Manager) PrepareUpdate() error {
	m.Stop()
	if err := m.store.Flush(); err != nil {
		return err
	}
	if tx, err := m.store.PendingTransaction(); err != nil {
		return err
	} else if tx != nil {
		return fmt.Errorf("unfinished routing transaction; restart current version before updating")
	}
	return nil
}
