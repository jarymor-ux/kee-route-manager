package web

import (
	"errors"
	"net/http"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type SettingsApplyStatus struct {
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Revision string `json:"revision,omitempty"`
}

// The daemon supplies this service. The static UI only proxies these requests.
type SettingsController interface {
	SettingsSnapshot() (config.SettingsSnapshot, SettingsApplyStatus, error)
	SaveSettings(config.EditableSettings, string) (bool, string, error)
	ValidateSettings(config.EditableSettings, string) error
	ActivateSettings()
}

func (s *Server) UseSettings(settings SettingsController) { s.settings = settings }

// Rebuilding a controller runtime does not revoke otherwise valid sessions.
// This is called only after all handlers of the previous runtime have drained.
func (s *Server) ReuseAuthentication(previous *Server) {
	if previous == nil {
		return
	}
	// Keep freshly loaded users: password or permission changes on disk must
	// still revoke sessions by revision even across a settings reload.
	s.sessions, s.limiter = previous.sessions, previous.limiter
}

func (s *Server) settingsSnapshot(w http.ResponseWriter, _ *http.Request, _ auth.Session) {
	w.Header().Set("Cache-Control", "no-store")
	if s.settings == nil {
		jsonError(w, http.StatusServiceUnavailable, "settings editor unavailable")
		return
	}
	snapshot, apply, err := s.settings.SettingsSnapshot()
	if err != nil {
		settingsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		config.SettingsSnapshot
		Apply SettingsApplyStatus `json:"apply"`
	}{snapshot, apply})
}

func (s *Server) settingsChange(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	w.Header().Set("Cache-Control", "no-store")
	if s.settings == nil {
		jsonError(w, http.StatusServiceUnavailable, "settings editor unavailable")
		return
	}
	var input *struct {
		Settings *config.EditableSettings `json:"settings"`
		Revision string                   `json:"revision"`
	}
	if err := decodeBody(w, r, &input); err != nil {
		return
	}
	if input == nil || input.Settings == nil || input.Revision == "" {
		settingsError(w, config.ErrSettingsInvalid)
		return
	}
	if r.URL.Path == "/api/v1/settings/validate" {
		if err := s.settings.ValidateSettings(*input.Settings, input.Revision); err != nil {
			settingsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"valid": true})
		return
	}
	changed, revision, err := s.settings.SaveSettings(*input.Settings, input.Revision)
	if err != nil {
		settingsError(w, err)
		return
	}
	status := http.StatusOK
	if changed {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]any{"accepted": changed, "changed": changed, "revision": revision})
	if changed {
		// serveHTTP drains this response before rebuilding the runtime.
		s.settings.ActivateSettings()
	}
}

func settingsError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusServiceUnavailable, "unavailable", "settings editor unavailable"
	switch {
	case errors.Is(err, config.ErrSettingsConflict):
		status, code, message = http.StatusConflict, "settings_conflict", "settings changed; reload before saving"
	case errors.Is(err, config.ErrSettingsBusy):
		status, code, message = http.StatusConflict, "settings_busy", "settings are being applied"
	case errors.Is(err, config.ErrSettingsInvalid):
		status, code, message = http.StatusBadRequest, "settings_invalid", "invalid settings; check values and limits"
	}
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}
