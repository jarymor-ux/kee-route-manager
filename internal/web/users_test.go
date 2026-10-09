package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
)

type permissionsController struct {
	fakeSubscriptionController
	calls  int
	audits []event.Event
}

func (f *permissionsController) RecordAudit(entry event.Event) { f.audits = append(f.audits, entry) }
func (f *permissionsController) touch()                        { f.calls++ }
func (f *permissionsController) Status(ctx context.Context) core.Status {
	f.touch()
	return f.fakeController.Status(ctx)
}
func (f *permissionsController) Nodes() []model.NodeView          { f.touch(); return nil }
func (f *permissionsController) Events(uint64, int) []event.Event { f.touch(); return nil }
func (f *permissionsController) Metrics() platform.Metrics        { f.touch(); return platform.Metrics{} }
func (f *permissionsController) MetricsHistory() []platform.Metrics {
	f.touch()
	return []platform.Metrics{}
}
func (f *permissionsController) Clients() core.ClientsSnapshot {
	f.touch()
	return core.ClientsSnapshot{}
}
func (f *permissionsController) SystemLogs(context.Context, int) (string, error) {
	f.touch()
	return "safe", nil
}
func (f *permissionsController) Diagnostics(context.Context) (string, error) {
	f.touch()
	return "safe", nil
}
func (f *permissionsController) RequestBenchmark(context.Context, string) (*operation.Operation, error) {
	f.touch()
	return &operation.Operation{ID: "op-permission"}, nil
}
func (f *permissionsController) CancelBenchmark(context.Context) error { f.touch(); return nil }
func (f *permissionsController) RestartXray(context.Context) error     { f.touch(); return nil }
func (f *permissionsController) Reboot(context.Context) error          { f.touch(); return nil }
func (f *permissionsController) Wake(context.Context, string) error    { f.touch(); return nil }
func (f *permissionsController) SetPolicy(context.Context, string, string) error {
	f.touch()
	return nil
}
func (f *permissionsController) SwitchSlot(context.Context, int) error { f.touch(); return nil }
func (f *permissionsController) SwitchDirect(context.Context) error    { f.touch(); return nil }
func (f *permissionsController) SubscriptionSources() []config.Source  { f.touch(); return f.sources }
func (f *permissionsController) SaveSubscription(context.Context, config.Source) error {
	f.touch()
	return nil
}
func (f *permissionsController) DeleteSubscription(context.Context, string) error {
	f.touch()
	return nil
}

func newUsersTestServer(t *testing.T) (*Server, *permissionsController) {
	t.Helper()
	c := config.Default()
	c.API.TLS.Enabled = false
	c.Web.CredentialsFile = filepath.Join(t.TempDir(), "credentials.json")
	if err := auth.CreateCredentials(c.Web.CredentialsFile, "admin", "synthetic-password"); err != nil {
		t.Fatal(err)
	}
	controller := &permissionsController{fakeSubscriptionController: fakeSubscriptionController{fakeController: &fakeController{}, sources: []config.Source{{ID: "primary", Name: "Primary", Enabled: true, URL: "https://example.invalid/private-secret", Headers: map[string]string{"Authorization": "Bearer private-secret"}}}}}
	s, err := New(c, controller, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, controller
}
func userRequest(t *testing.T, s *Server, session auth.Session, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:32100"
	request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session.ID})
	request.Header.Set("X-KRM-CSRF", session.CSRF)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	return response
}
func loginTestUser(t *testing.T, s *Server, username, password string) auth.Session {
	t.Helper()
	w := userRequest(t, s, auth.Session{}, "POST", "/api/v1/auth/login", `{"username":"`+username+`","password":"`+password+`"}`)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	var response struct {
		User        auth.User `json:"user"`
		Permissions []string  `json:"permissions"`
		CSRF        string    `json:"csrf"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.User.ID == "" || response.CSRF == "" {
		t.Fatal("missing user/session identity")
	}
	cookie := w.Result().Cookies()[0]
	session, ok := s.sessions.Get(cookie.Value)
	if !ok {
		t.Fatal("missing session")
	}
	return session
}

func TestEveryNetworkRouteChecksGranularPermissions(t *testing.T) {
	s, controller := newUsersTestServer(t)
	user, err := s.users.Save(auth.UserInput{Username: "operator", Password: "operator-password", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	session := loginTestUser(t, s, "operator", "operator-password")
	// Independent API matrix: positive requests may fail payload/capability
	// validation, but must reach the authorized handler; denials never call core.
	routes := []struct{ method, path, permission, alternative string }{
		{"GET", "/api/v1/status", "vpn.view", "router.view"},
		{"GET", "/api/v1/nodes", "vpn.view", ""},
		{"GET", "/api/v1/subscriptions", "subscriptions.view", "subscriptions.manage"},
		{"POST", "/api/v1/subscriptions/save", "subscriptions.manage", ""},
		{"POST", "/api/v1/subscriptions/delete", "subscriptions.manage", ""},
		{"GET", "/api/v1/events", "events.view", ""},
		{"GET", "/api/v1/router/metrics", "router.view", ""},
		{"GET", "/api/v1/router/metrics/history", "router.view", ""},
		{"GET", "/api/v1/router/clients", "router.clients", ""},
		{"GET", "/api/v1/router/logs", "router.system", ""},
		{"GET", "/api/v1/router/diagnostics", "router.system", ""},
		{"POST", "/api/v1/actions/benchmark", "vpn.control", ""},
		{"POST", "/api/v1/actions/benchmark/cancel", "vpn.control", ""},
		{"POST", "/api/v1/actions/xray-restart", "vpn.control", ""},
		{"POST", "/api/v1/actions/reboot", "router.reboot", ""},
		{"POST", "/api/v1/actions/wake", "router.wake", ""},
		{"POST", "/api/v1/actions/policy", "router.policy", ""},
		{"POST", "/api/v1/actions/switch", "vpn.control", ""},
		{"POST", "/api/v1/actions/direct", "vpn.control", ""},
		{"GET", "/api/v1/update/check", "updates.manage", ""},
		{"GET", "/api/v1/update/status", "updates.manage", ""},
		{"POST", "/api/v1/update/apply", "updates.manage", ""},
		{"POST", "/api/v1/update/channel", "updates.manage", ""},
		{"GET", "/api/v1/users", "users.manage", ""},
		{"POST", "/api/v1/users/save", "users.manage", ""},
		{"POST", "/api/v1/users/delete", "users.manage", ""},
	}
	for _, permission := range auth.Permissions() {
		user, err = s.users.Save(auth.UserInput{ID: user.ID, Username: user.Username, Enabled: true, Permissions: []string{permission}})
		if err != nil {
			t.Fatal(err)
		}
		for _, route := range routes {
			t.Run(permission+route.path, func(t *testing.T) {
				before := controller.calls
				w := userRequest(t, s, session, route.method, route.path, `{}`)
				allowed := route.path == "/api/v1/status" || permission == route.permission || permission == route.alternative
				if allowed {
					if w.Code == 401 || w.Code == 403 || w.Code == 404 {
						t.Fatalf("authorized: %d %s", w.Code, w.Body)
					}
				} else if w.Code != 403 || controller.calls != before {
					t.Fatalf("unauthorized request reached handler: %d %s", w.Code, w.Body)
				}
			})
		}
	}
	if allowedRoute(auth.User{Permissions: auth.Permissions()}, "/api/v1/unknown") {
		t.Fatal("unknown route did not fail closed")
	}
}

func TestPermissionsRevokedOnActiveSessionAndSecretsFiltered(t *testing.T) {
	s, _ := newUsersTestServer(t)
	user, err := s.users.Save(auth.UserInput{Username: "operator", Password: "operator-password", Enabled: true, Permissions: auth.Permissions()})
	if err != nil {
		t.Fatal(err)
	}
	session := loginTestUser(t, s, user.Username, "operator-password")
	w := userRequest(t, s, session, "GET", "/api/v1/subscriptions", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "private-secret") {
		t.Fatalf("manager subscription editor: %d %s", w.Code, w.Body)
	}
	user, err = s.users.Save(auth.UserInput{ID: user.ID, Username: user.Username, Enabled: true, Permissions: []string{"subscriptions.view", "router.view"}})
	if err != nil {
		t.Fatal(err)
	}
	w = userRequest(t, s, session, "GET", "/api/v1/subscriptions", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), `"url"`) || strings.Contains(w.Body.String(), `"headers"`) {
		t.Fatalf("reader secret leak: %d %s", w.Code, w.Body)
	}
	w = userRequest(t, s, session, "POST", "/api/v1/actions/benchmark", "")
	if w.Code != 403 {
		t.Fatal("revoked permission usable", w.Code)
	}
	w = userRequest(t, s, session, "GET", "/api/v1/session", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "vpn.control") {
		t.Fatal("session permissions stale", w.Body)
	}
	w = userRequest(t, s, session, "GET", "/api/v1/status", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"state"`) {
		t.Fatal("router-only status exposed VPN state", w.Body)
	}
	if _, err = s.users.Save(auth.UserInput{ID: user.ID, Username: user.Username, Enabled: false, Permissions: user.Permissions}); err != nil {
		t.Fatal(err)
	}
	w = userRequest(t, s, session, "GET", "/api/v1/session", "")
	if w.Code != 401 {
		t.Fatal("blocked session retained", w.Code)
	}
}

func TestUserCRUDSessionPasswordAndCSRF(t *testing.T) {
	s, controller := newUsersTestServer(t)
	adminSession := loginTestUser(t, s, "admin", "synthetic-password")
	w := userRequest(t, s, adminSession, "POST", "/api/v1/users/save", `{"username":"created-user","password":"new-user-password","enabled":true,"permissions":["vpn.view"]}`)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var result struct {
		User auth.User `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	userSession := loginTestUser(t, s, "created-user", "new-user-password")
	w = userRequest(t, s, adminSession, "GET", "/api/v1/users", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "new-user-password") {
		t.Fatal("user list leaked credential", w.Body)
	}
	w = userRequest(t, s, adminSession, "POST", "/api/v1/users/save", `{"id":"`+result.User.ID+`","username":"created-user","password":"replacement-password","enabled":true,"permissions":["vpn.view"]}`)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	stale, _ := json.Marshal(auth.UserInput{ID: result.User.ID, Username: result.User.Username, Enabled: true, Permissions: []string{"vpn.view"}, ExpectedUpdatedAt: &result.User.UpdatedAt})
	w = userRequest(t, s, adminSession, "POST", "/api/v1/users/save", string(stale))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "user_changed") {
		t.Fatal("stale editor overwrote password update", w.Code, w.Body)
	}
	w = userRequest(t, s, userSession, "GET", "/api/v1/session", "")
	if w.Code != 401 {
		t.Fatal("password change kept old session", w.Code)
	}
	userSession = loginTestUser(t, s, "created-user", "replacement-password")
	noCSRF := adminSession
	noCSRF.CSRF = "forged"
	w = userRequest(t, s, noCSRF, "POST", "/api/v1/users/delete", `{"id":"`+result.User.ID+`"}`)
	if w.Code != 403 {
		t.Fatal("user deletion bypassed CSRF", w.Code)
	}
	w = userRequest(t, s, adminSession, "POST", "/api/v1/users/delete", `{"id":"`+result.User.ID+`"}`)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w = userRequest(t, s, userSession, "GET", "/api/v1/session", ""); w.Code != 401 {
		t.Fatal("deleted user retained access")
	}
	w = userRequest(t, s, adminSession, "POST", "/api/v1/users/delete", `{"id":"`+adminSession.UserID+`"}`)
	if w.Code != 400 {
		t.Fatal("last administrator deleted", w.Code)
	}
	if len(controller.audits) != 3 {
		t.Fatalf("audit count %d, want create/update/delete", len(controller.audits))
	}
	for i, kind := range []string{"user.created", "user.updated", "user.deleted"} {
		entry := controller.audits[i]
		if entry.Type != kind || entry.Fields["actor_id"] != adminSession.UserID || entry.Fields["target_id"] != result.User.ID || len(entry.Fields["request_id"].(string)) != 32 {
			t.Fatalf("invalid user audit: %+v", entry)
		}
		data, _ := json.Marshal(entry)
		if strings.Contains(string(data), "password") {
			t.Fatal("audit leaked password")
		}
	}
}

func TestPublicOperationErrors(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    int
		message string
	}{
		{operation.ErrBusy, 409, "operation busy"},
		{context.Canceled, 409, "operation canceled"},
		{context.DeadlineExceeded, 503, "operation unavailable"},
		{platform.ErrPanelDNSConflict, 409, "dns_conflict"},
		{platform.ErrPanelDNSDrift, 409, "dns_drift"},
		{platform.ErrPanelDNSPending, 409, "dns_pending"},
		{platform.ErrPanelDNSUnavailable, 503, "dns_unavailable"},
	} {
		w := httptest.NewRecorder()
		operationError(w, tc.err)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.message) {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
}

func TestSessionWithoutUserBindingCannotAccessAPI(t *testing.T) {
	s, _ := newUsersTestServer(t)
	session, err := s.sessions.Create("admin", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	w := userRequest(t, s, session, "GET", "/api/v1/users", "")
	if w.Code != 401 {
		t.Fatal("unbound legacy session gained rights", w.Code)
	}
	s.sessions = auth.NewSessionStore(time.Hour)
}

func TestControlActionsBridgeActorRequestAndOperationAudit(t *testing.T) {
	s, controller := newUsersTestServer(t)
	session := loginTestUser(t, s, "admin", "synthetic-password")
	w := userRequest(t, s, session, "POST", "/api/v1/actions/benchmark", `{}`)
	if w.Code != 202 || len(controller.audits) != 1 {
		t.Fatalf("benchmark/audit: %d %+v", w.Code, controller.audits)
	}
	entry := controller.audits[0]
	if entry.Type != "api.action" || entry.OperationID != "op-permission" || entry.Fields["operation_id"] != "op-permission" || entry.Fields["actor_id"] != session.UserID || entry.Fields["path"] != "/api/v1/actions/benchmark" || entry.Fields["request_id"] != w.Header().Get("X-Request-ID") {
		t.Fatalf("invalid action attribution: %+v", entry)
	}
	forged := session
	forged.CSRF = "forged"
	w = userRequest(t, s, forged, "POST", "/api/v1/actions/policy", `{"mac":"02:00:00:00:00:01","policy":"default"}`)
	if w.Code != 403 || len(controller.audits) != 1 {
		t.Fatal("rejected action emitted accepted audit")
	}
}
