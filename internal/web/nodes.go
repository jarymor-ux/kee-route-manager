package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
)

type nodeAutoSelectController interface {
	SetNodeAutoSelect(context.Context, string, bool) error
}

func (s *Server) nodeAutoSelect(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	manager, ok := s.mgr.(nodeAutoSelectController)
	if !ok {
		jsonError(w, http.StatusNotImplemented, "node selection management unavailable")
		return
	}
	var input *struct {
		ID      string `json:"id"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decodeBody(w, r, &input); err != nil {
		return
	}
	if input == nil || input.ID == "" || input.Enabled == nil {
		jsonError(w, http.StatusBadRequest, "id and enabled are required")
		return
	}
	if err := manager.SetNodeAutoSelect(r.Context(), input.ID, *input.Enabled); err != nil {
		switch {
		case errors.Is(err, core.ErrActiveNode):
			writeJSON(w, http.StatusConflict, map[string]string{"code": "node_active", "error": "active node cannot be excluded"})
		case errors.Is(err, core.ErrNodeNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "node_not_found", "error": "node not found"})
		default:
			operationError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
