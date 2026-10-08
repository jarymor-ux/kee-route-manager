package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type testSettingsController struct {
	snapshot                                               config.SettingsSnapshot
	apply                                                  SettingsApplyStatus
	snapshotErr, errorResult                               error
	changed                                                bool
	revision                                               string
	snapshotCalls, saveCalls, validateCalls, activateCalls int
	receivedSettings                                       config.EditableSettings
	receivedRevision                                       string
	onActivate                                             func()
}

func (c *testSettingsController) SettingsSnapshot() (config.SettingsSnapshot, SettingsApplyStatus, error) {
	c.snapshotCalls++
	return c.snapshot, c.apply, c.snapshotErr
}
func (c *testSettingsController) SaveSettings(s config.EditableSettings, revision string) (bool, string, error) {
	c.saveCalls++
	c.receivedSettings = s
	c.receivedRevision = revision
	return c.changed, c.revision, c.errorResult
}
func (c *testSettingsController) ValidateSettings(s config.EditableSettings, revision string) error {
	c.validateCalls++
	c.receivedSettings = s
	c.receivedRevision = revision
	return c.errorResult
}
func (c *testSettingsController) ActivateSettings() {
	c.activateCalls++
	if c.onActivate != nil {
		c.onActivate()
	}
}
func (c *testSettingsController) calls() int {
	return c.snapshotCalls + c.saveCalls + c.validateCalls + c.activateCalls
}

func newSettingsTestController() *testSettingsController {
	c := config.Default()
	c.Subscriptions.Sources = []config.Source{{ID: "private-source", URL: "https://provider.invalid/private-subscription-token", Headers: map[string]string{"Authorization": "Bearer private-source-header"}, Enabled: true}}
	c.API.TLS.KeyFile = "/private/controller.key"
	c.Platform.XrayRestartCommand = []string{"secret-maintenance-command"}
	return &testSettingsController{snapshot: config.SettingsSnapshot{Settings: config.SettingsFromConfig(c), Revision: "before-revision", PoolSize: c.Pool.Size}, apply: SettingsApplyStatus{Status: "idle"}, revision: "after-revision", changed: true}
}
func settingsRequestBody(t *testing.T, c *testSettingsController) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"settings": c.snapshot.Settings, "revision": c.snapshot.Revision})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func settingsTestLogin(t *testing.T, s *Server, permissions []string) auth.Session {
	t.Helper()
	if _, err := s.users.Save(auth.UserInput{Username: "settings-operator", Password: "settings-test-password", Enabled: true, Permissions: permissions}); err != nil {
		t.Fatal(err)
	}
	return loginTestUser(t, s, "settings-operator", "settings-test-password")
}
func assertSettingsNoCalls(t *testing.T, c *testSettingsController) {
	t.Helper()
	if c.calls() != 0 {
		t.Fatalf("rejected request reached settings controller: %+v", c)
	}
}

func TestSettingsRoutesRequireManagementPermissions(t *testing.T) {
	cases := []struct {
		name        string
		permissions []string
		allowed     bool
	}{
		{"config manager", []string{"config.manage"}, true},
		{"user manager", []string{"users.manage"}, true},
		{"viewer", auth.Roles()["viewer"], false},
		{"router operator", auth.Roles()["router-operator"], false},
		{"vpn operator", auth.Roles()["vpn-operator"], false},
		{"update manager", []string{"updates.manage"}, false},
		{"no permissions", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newUsersTestServer(t)
			session := settingsTestLogin(t, s, tc.permissions)
			for _, route := range []struct {
				method, path string
				code         int
			}{{"GET", "/api/v1/settings", 200}, {"POST", "/api/v1/settings/validate", 200}, {"POST", "/api/v1/settings/save", 202}} {
				controller := newSettingsTestController()
				s.UseSettings(controller)
				response := userRequest(t, s, session, route.method, route.path, settingsRequestBody(t, controller))
				if tc.allowed {
					if response.Code != route.code {
						t.Fatalf("authorized %s: %d %s", route.path, response.Code, response.Body)
					}
					if controller.calls() == 0 {
						t.Fatal("authorized route did not reach controller")
					}
				} else {
					if response.Code != 403 {
						t.Fatalf("forbidden %s: %d", route.path, response.Code)
					}
					assertSettingsNoCalls(t, controller)
				}
			}
		})
	}
	s, _ := newUsersTestServer(t)
	controller := newSettingsTestController()
	s.UseSettings(controller)
	for _, path := range []string{"/api/v1/settings", "/api/v1/settings/save", "/api/v1/settings/validate"} {
		method := "POST"
		if path == "/api/v1/settings" {
			method = "GET"
		}
		response := userRequest(t, s, auth.Session{}, method, path, settingsRequestBody(t, controller))
		if response.Code != 401 {
			t.Fatalf("anonymous %s: %d", path, response.Code)
		}
	}
	assertSettingsNoCalls(t, controller)
}

func TestSettingsMutationsEnforceCSRFOriginAndMethods(t *testing.T) {
	s, _ := newUsersTestServer(t)
	session := settingsTestLogin(t, s, []string{"config.manage"})
	for _, path := range []string{"/api/v1/settings/save", "/api/v1/settings/validate"} {
		for _, tc := range []struct {
			name, method string
			modify       func(*http.Request)
			code         int
		}{
			{"missing CSRF", "POST", func(r *http.Request) { r.Header.Del("X-KRM-CSRF") }, 403},
			{"forged CSRF", "POST", func(r *http.Request) { r.Header.Set("X-KRM-CSRF", "forged") }, 403},
			{"cross origin", "POST", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.invalid") }, 403},
			{"GET", "GET", func(*http.Request) {}, 405},
		} {
			t.Run(path+tc.name, func(t *testing.T) {
				controller := newSettingsTestController()
				s.UseSettings(controller)
				request := httptest.NewRequest(tc.method, "http://127.0.0.1"+path, strings.NewReader(settingsRequestBody(t, controller)))
				request.RemoteAddr = "127.0.0.1:32100"
				request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session.ID})
				request.Header.Set("X-KRM-CSRF", session.CSRF)
				tc.modify(request)
				response := httptest.NewRecorder()
				s.Handler().ServeHTTP(response, request)
				if response.Code != tc.code {
					t.Fatalf("got %d %s", response.Code, response.Body)
				}
				assertSettingsNoCalls(t, controller)
			})
		}
	}
	controller := newSettingsTestController()
	s.UseSettings(controller)
	if response := userRequest(t, s, session, "POST", "/api/v1/settings", ""); response.Code != 405 {
		t.Fatalf("snapshot POST: %d", response.Code)
	}
	assertSettingsNoCalls(t, controller)
}

func TestSettingsSnapshotExposesOnlyEditableFields(t *testing.T) {
	s, _ := newUsersTestServer(t)
	session := settingsTestLogin(t, s, []string{"config.manage"})
	controller := newSettingsTestController()
	s.UseSettings(controller)
	response := userRequest(t, s, session, "GET", "/api/v1/settings", "")
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("snapshot %d %s", response.Code, response.Body)
	}
	for _, forbidden := range []string{"private-subscription-token", "private-source-header", "controller.key", "secret-maintenance-command", `"sources"`, `"headers"`, `"tls"`, `"commands"`, `"paths"`, `"api"`, `"xray"`, `"targets"`, `"platform"`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("snapshot exposed %s", forbidden)
		}
	}
	var body struct {
		config.SettingsSnapshot
		Apply SettingsApplyStatus `json:"apply"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.PoolSize != 5 || body.Revision != controller.snapshot.Revision || body.Apply.Status != "idle" || body.Settings.Benchmark.FullInterval.Duration != controller.snapshot.Settings.Benchmark.FullInterval.Duration {
		t.Fatalf("wrong snapshot %+v", body)
	}
	if controller.snapshotCalls != 1 || controller.saveCalls != 0 || controller.activateCalls != 0 {
		t.Fatal("snapshot mutated settings")
	}
}

func TestSettingsMalformedAndIncompleteBodiesNeverReachController(t *testing.T) {
	s, _ := newUsersTestServer(t)
	session := settingsTestLogin(t, s, []string{"config.manage"})
	original := newSettingsTestController()
	valid := settingsRequestBody(t, original)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(valid), &envelope); err != nil {
		t.Fatal(err)
	}
	missingGroup := func(group string) string {
		var groups map[string]json.RawMessage
		_ = json.Unmarshal(envelope["settings"], &groups)
		delete(groups, group)
		raw, _ := json.Marshal(map[string]any{"settings": groups, "revision": "before-revision"})
		return string(raw)
	}
	bodies := map[string]string{
		"empty": "", "null": "null", "empty object": "{}", "missing settings": `{"revision":"before-revision"}`, "null settings": `{"settings":null,"revision":"before-revision"}`,
		"missing revision": `{"settings":` + string(envelope["settings"]) + `}`, "empty revision": `{"settings":` + string(envelope["settings"]) + `,"revision":""}`,
		"unknown envelope": strings.TrimSuffix(valid, "}") + `,"private":"secret-input-value"}`,
		"trailing JSON":    valid + ` {}`, "unknown editable field": strings.Replace(valid, `"benchmark":{`, `"benchmark":{"temporary_proxy_port_start":20000,`, 1),
		"unused health interval": strings.Replace(valid, `"health":{`, `"health":{"interval":"15s",`, 1),
		"hidden pool size":       strings.Replace(valid, `"pool":{`, `"pool":{"size":20,`, 1),
		"null benchmark":         strings.Replace(valid, string(envelope["settings"]), `{"benchmark":null,"health":{},"failover":{},"subscriptions":{},"pool":{}}`, 1),
	}
	for _, group := range []string{"benchmark", "health", "failover", "subscriptions", "pool"} {
		bodies["missing "+group] = missingGroup(group)
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			for _, path := range []string{"/api/v1/settings/save", "/api/v1/settings/validate"} {
				controller := newSettingsTestController()
				s.UseSettings(controller)
				response := userRequest(t, s, session, "POST", path, body)
				if response.Code != 400 {
					t.Fatalf("%s: %d %s", path, response.Code, response.Body)
				}
				assertSettingsNoCalls(t, controller)
				if strings.Contains(response.Body.String(), "secret-input-value") {
					t.Fatal("bad JSON error echoed submitted secret")
				}
			}
		})
	}
}

func TestSettingsControllerErrorsHaveStableCodesAndNoActivation(t *testing.T) {
	s, _ := newUsersTestServer(t)
	session := settingsTestLogin(t, s, []string{"config.manage"})
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"stale", config.ErrSettingsConflict, 409, "settings_conflict"},
		{"invalid", config.ErrSettingsInvalid, 400, "settings_invalid"},
		{"busy", config.ErrSettingsBusy, 409, "settings_busy"},
		{"unavailable", config.ErrSettingsUnavailable, 503, "unavailable"},
		{"unexpected", fmt.Errorf("private-path: secret-controller-error"), 503, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/api/v1/settings/save", "/api/v1/settings/validate"} {
				controller := newSettingsTestController()
				controller.errorResult = fmt.Errorf("operation: %w", tc.err)
				s.UseSettings(controller)
				response := userRequest(t, s, session, "POST", path, settingsRequestBody(t, controller))
				var body map[string]string
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if response.Code != tc.status || body["code"] != tc.code || controller.activateCalls != 0 {
					t.Fatalf("%s: %d %+v", path, response.Code, body)
				}
				if strings.Contains(response.Body.String(), "secret-controller-error") || strings.Contains(response.Body.String(), "private-path") {
					t.Fatal("error exposed private details")
				}
			}
		})
	}
}

func TestSettingsValidateNeverSavesOrActivates(t *testing.T) {
	s, _ := newUsersTestServer(t)
	session := settingsTestLogin(t, s, []string{"config.manage"})
	controller := newSettingsTestController()
	s.UseSettings(controller)
	response := userRequest(t, s, session, "POST", "/api/v1/settings/validate", settingsRequestBody(t, controller))
	var result map[string]bool
	_ = json.Unmarshal(response.Body.Bytes(), &result)
	if response.Code != 200 || !result["valid"] || controller.validateCalls != 1 || controller.saveCalls != 0 || controller.activateCalls != 0 || controller.receivedRevision != "before-revision" || controller.receivedSettings.Benchmark.FullInterval != controller.snapshot.Settings.Benchmark.FullInterval {
		t.Fatalf("validate: %d %s", response.Code, response.Body)
	}
}

func TestSettingsSaveWritesAcceptedReplyBeforeActivationAndNoopDoesNotActivate(t *testing.T) {
	s, _ := newUsersTestServer(t)
	session := settingsTestLogin(t, s, []string{"config.manage"})
	for _, changed := range []bool{true, false} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			controller := newSettingsTestController()
			controller.changed = changed
			if !changed {
				controller.revision = controller.snapshot.Revision
			}
			s.UseSettings(controller)
			response := httptest.NewRecorder()
			controller.onActivate = func() {
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Errorf("activation preceded complete JSON response: %v", err)
				}
				if response.Code != 202 || body["revision"] != "after-revision" || body["accepted"] != true {
					t.Errorf("activation preceded accepted reply: %d %s", response.Code, response.Body)
				}
			}
			request := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/settings/save", strings.NewReader(settingsRequestBody(t, controller)))
			request.RemoteAddr = "127.0.0.1:32100"
			request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session.ID})
			request.Header.Set("X-KRM-CSRF", session.CSRF)
			s.Handler().ServeHTTP(response, request)
			var body struct {
				Accepted, Changed bool
				Revision          string
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			expectedCode, expectedActivations := 200, 0
			if changed {
				expectedCode, expectedActivations = 202, 1
			}
			if response.Code != expectedCode || body.Accepted != changed || body.Changed != changed || body.Revision != controller.revision || controller.saveCalls != 1 || controller.activateCalls != expectedActivations || controller.validateCalls != 0 || controller.receivedRevision != "before-revision" || controller.receivedSettings.Benchmark.FullInterval != controller.snapshot.Settings.Benchmark.FullInterval {
				t.Fatalf("save %d %s calls=%+v", response.Code, response.Body, controller)
			}
		})
	}
}

func TestSettingsReloadReusesSessionsButFreshUserStoreEnforcesRevocation(t *testing.T) {
	for _, change := range []string{"none", "permissions", "password", "disabled"} {
		t.Run(change, func(t *testing.T) {
			previous, manager := newUsersTestServer(t)
			session := settingsTestLogin(t, previous, []string{"config.manage"})
			// Model a concurrent disk change without mutating the old runtime's store.
			diskUsers, err := auth.LoadUsers(previous.cfg.Web.CredentialsFile)
			if err != nil {
				t.Fatal(err)
			}
			input := auth.UserInput{ID: session.UserID, Username: "settings-operator", Enabled: true, Permissions: []string{"config.manage"}}
			switch change {
			case "permissions":
				input.Permissions = []string{"vpn.view"}
			case "password":
				input.Password = "replacement-settings-password"
			case "disabled":
				input.Enabled = false
			}
			if change != "none" {
				if _, err = diskUsers.Save(input); err != nil {
					t.Fatal(err)
				}
			}
			replacement, err := New(previous.cfg, manager, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			replacement.ReuseAuthentication(previous)
			if replacement.sessions != previous.sessions || replacement.limiter != previous.limiter || replacement.users == previous.users {
				t.Fatal("authentication reload replaced sessions or retained stale users")
			}
			controller := newSettingsTestController()
			replacement.UseSettings(controller)
			response := userRequest(t, replacement, session, "GET", "/api/v1/settings", "")
			switch change {
			case "none":
				if response.Code != 200 {
					t.Fatalf("valid session lost after reload: %d %s", response.Code, response.Body)
				}
			case "permissions":
				if response.Code != 403 {
					t.Fatalf("fresh permission revocation ignored: %d %s", response.Code, response.Body)
				}
				assertSettingsNoCalls(t, controller)
				sessionResponse := userRequest(t, replacement, session, "GET", "/api/v1/session", "")
				if sessionResponse.Code != 200 || strings.Contains(sessionResponse.Body.String(), "config.manage") {
					t.Fatal("session permission snapshot stale", sessionResponse.Body)
				}
			default:
				if response.Code != 401 {
					t.Fatalf("fresh credential revision ignored: %d %s", response.Code, response.Body)
				}
				assertSettingsNoCalls(t, controller)
			}
		})
	}
}
