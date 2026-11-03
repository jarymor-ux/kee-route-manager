package web

import (
	"context"
	"net/http"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
)

type subscriptionUpdateController interface {
	SubscriptionUpdateStatus(context.Context) core.SubscriptionUpdateStatus
	SetSubscriptionAutoUpdate(context.Context, bool) error
	RefreshSubscriptions(context.Context) error
}

func (s *Server) subscriptionResponse(ctx context.Context, sources any) map[string]any {
	result := map[string]any{"sources": sources}
	if manager, ok := s.mgr.(subscriptionUpdateController); ok {
		status := manager.SubscriptionUpdateStatus(ctx)
		for id, state := range status.States {
			state.Name = redact.Text(state.Name)
			state.LastError = redact.Text(state.LastError)
			status.States[id] = state
		}
		result["auto_update_enabled"] = status.AutoUpdateEnabled
		result["cache_enabled"] = status.CacheEnabled
		result["states"] = status.States
	}
	return result
}

func (s *Server) subscriptionAutoUpdate(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	manager, ok := s.mgr.(subscriptionUpdateController)
	if !ok {
		jsonError(w, http.StatusNotImplemented, "subscription update management unavailable")
		return
	}
	var input *struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeBody(w, r, &input); err != nil {
		return
	}
	if input == nil || input.Enabled == nil {
		jsonError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	if err := manager.SetSubscriptionAutoUpdate(r.Context(), *input.Enabled); err != nil {
		operationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) subscriptionRefresh(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	manager, ok := s.mgr.(subscriptionUpdateController)
	if !ok {
		jsonError(w, http.StatusNotImplemented, "subscription update management unavailable")
		return
	}
	var input *struct{}
	if err := decodeBody(w, r, &input); err != nil {
		return
	}
	if input == nil {
		jsonError(w, http.StatusBadRequest, "JSON object is required")
		return
	}
	if err := manager.RefreshSubscriptions(r.Context()); err != nil {
		operationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
