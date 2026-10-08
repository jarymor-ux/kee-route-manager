package core

import "github.com/jarymor-ux/kee-route-manager/internal/event"

// RecordAudit uses the daemon's existing bounded, redacted event journal.
func (m *Manager) RecordAudit(entry event.Event) { _, _ = m.store.Append(entry) }
