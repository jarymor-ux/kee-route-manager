package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
)

type auditContextKey struct{}
type auditIdentity struct{ actor, operation, target string }

// Every protected route must appear here; unknown routes fail closed. Lists
// express alternative permissions, never a blanket administrator bypass.
var routePermissions = map[string][]string{
	"/api/v1/auth/logout": {}, "/api/v1/session": {},
	"/api/v1/status":                   {},
	"/api/v1/nodes":                    {"vpn.view"},
	"/api/v1/subscriptions":            {"subscriptions.view", "subscriptions.manage"},
	"/api/v1/subscriptions/save":       {"subscriptions.manage"},
	"/api/v1/subscriptions/delete":     {"subscriptions.manage"},
	"/api/v1/events":                   {"events.view"},
	"/api/v1/router/metrics":           {"router.view"},
	"/api/v1/router/metrics/history":   {"router.view"},
	"/api/v1/router/clients":           {"router.clients"},
	"/api/v1/router/logs":              {"router.system"},
	"/api/v1/router/diagnostics":       {"router.system"},
	"/api/v1/actions/benchmark":        {"vpn.control"},
	"/api/v1/actions/benchmark/cancel": {"vpn.control"},
	"/api/v1/actions/xray-restart":     {"vpn.control"},
	"/api/v1/actions/reboot":           {"router.reboot"},
	"/api/v1/actions/wake":             {"router.wake"},
	"/api/v1/actions/policy":           {"router.policy"},
	"/api/v1/actions/switch":           {"vpn.control"},
	"/api/v1/actions/direct":           {"vpn.control"},
	"/api/v1/update/check":             {"updates.manage"},
	"/api/v1/update/status":            {"updates.manage"},
	"/api/v1/update/apply":             {"updates.manage"},
	"/api/v1/users":                    {"users.manage"},
	"/api/v1/users/save":               {"users.manage"},
	"/api/v1/users/delete":             {"users.manage"},
}

func allowedRoute(user auth.User, path string) bool {
	permissions, ok := routePermissions[path]
	if !ok {
		return false
	}
	if len(permissions) == 0 {
		return true
	}
	for _, permission := range permissions {
		if user.Has(permission) {
			return true
		}
	}
	return false
}
func sessionResponse(user auth.User, session auth.Session) map[string]any {
	return map[string]any{"user": user, "username": user.Username, "permissions": user.Permissions, "csrf": session.CSRF, "expires_at": session.ExpiresAt}
}
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"users": s.users.List(), "permissions": auth.Permissions(), "roles": auth.Roles()})
}
func (s *Server) saveUser(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var input auth.UserInput
	if err := decodeBody(w, r, &input); err != nil {
		return
	}
	user, err := s.users.SaveAs(input, session.UserID, session.Revision)
	if err != nil {
		userChangeError(w, err)
		return
	}
	if audit, ok := r.Context().Value(auditContextKey{}).(*auditIdentity); ok {
		audit.target = user.ID
	}
	kind := "user.updated"
	if input.ID == "" {
		kind = "user.created"
	}
	s.auditUser(w, session, kind, user.ID)
	writeJSON(w, 200, map[string]any{"ok": true, "user": user})
}
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, session auth.Session) {
	var input auth.UserDeleteInput
	if err := decodeBody(w, r, &input); err != nil {
		return
	}
	if err := s.users.DeleteAs(input, session.UserID, session.Revision); err != nil {
		userChangeError(w, err)
		return
	}
	if audit, ok := r.Context().Value(auditContextKey{}).(*auditIdentity); ok {
		audit.target = input.ID
	}
	s.auditUser(w, session, "user.deleted", input.ID)
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) auditUser(w http.ResponseWriter, session auth.Session, kind, target string) {
	if manager, ok := s.mgr.(interface{ RecordAudit(event.Event) }); ok {
		manager.RecordAudit(event.Event{Level: "info", Type: kind, Message: kind, Fields: map[string]any{"actor_id": session.UserID, "target_id": target, "request_id": w.Header().Get("X-Request-ID")}})
	}
}
func userChangeError(w http.ResponseWriter, err error) {
	if errors.Is(err, auth.ErrUserPermission) {
		jsonError(w, 403, "permission denied")
		return
	}
	if errors.Is(err, auth.ErrUserConflict) {
		writeJSON(w, 409, map[string]any{"error": auth.ErrUserConflict.Error(), "code": "user_changed"})
		return
	}
	if errors.Is(err, auth.ErrUserValidation) {
		jsonError(w, 400, err.Error())
		return
	}
	jsonError(w, 503, "user storage unavailable")
}
func (s *Server) cancelBenchmark(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	manager, ok := s.mgr.(interface{ CancelBenchmark(context.Context) error })
	if !ok {
		jsonError(w, 501, "benchmark cancellation unavailable")
		return
	}
	if err := manager.CancelBenchmark(r.Context()); err != nil {
		operationError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"accepted": true})
}
func (s *Server) metricsHistory(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	manager, ok := s.mgr.(interface{ MetricsHistory() []platform.Metrics })
	if !ok {
		jsonError(w, 501, "metrics history unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"samples": manager.MetricsHistory()})
}
func operationError(w http.ResponseWriter, err error) {
	code, message, publicCode := http.StatusConflict, "operation conflict", "conflict"
	switch {
	case errors.Is(err, operation.ErrBusy):
		message, publicCode = "operation busy", "busy"
	case errors.Is(err, operation.ErrSuperseded):
		message, publicCode = "settings changed; benchmark result discarded", "settings_changed"
	case errors.Is(err, context.Canceled):
		message, publicCode = "operation canceled", "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		code, message, publicCode = http.StatusServiceUnavailable, "operation unavailable", "unavailable"
	}
	writeJSON(w, code, map[string]any{"error": message, "code": publicCode})
}
