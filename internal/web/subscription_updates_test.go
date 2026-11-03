package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

type subscriptionUpdatesController struct {
	Controller
	enabled            bool
	toggles, refreshes int
	err                error
}

func (c *subscriptionUpdatesController) SetSubscriptionAutoUpdate(_ context.Context, enabled bool) error {
	c.enabled = enabled
	c.toggles++
	return c.err
}
func (c *subscriptionUpdatesController) RefreshSubscriptions(context.Context) error {
	c.refreshes++
	return c.err
}
func (c *subscriptionUpdatesController) SubscriptionSources() []config.Source {
	return []config.Source{{ID: "a", Name: "Provider", URL: "https://fixture.invalid/private-subscription", Headers: map[string]string{"X-HWID": "private-header"}, Enabled: true}}
}
func (c *subscriptionUpdatesController) SaveSubscription(context.Context, config.Source) error {
	return nil
}
func (c *subscriptionUpdatesController) DeleteSubscription(context.Context, string) error { return nil }
func (c *subscriptionUpdatesController) SubscriptionUpdateStatus(context.Context) core.SubscriptionUpdateStatus {
	return core.SubscriptionUpdateStatus{AutoUpdateEnabled: c.enabled, CacheEnabled: true, States: map[string]model.SourceState{"a": {ID: "a", Status: "cached", NodeCount: 2}}}
}

func TestSubscriptionUpdateControlsRequireAuthorizationAndValidInput(t *testing.T) {
	for _, path := range []string{"/api/v1/subscriptions/auto-update", "/api/v1/subscriptions/refresh"} {
		for _, scenario := range []string{"success", "local", "csrf", "origin", "method", "permission", "null", "missing", "unknown", "trailing"} {
			t.Run(path+"/"+scenario, func(t *testing.T) {
				c := &subscriptionUpdatesController{}
				s, session := actionServer(t, c)
				s.cfg.API.TLS.Enabled = true
				body := `{}`
				if strings.HasSuffix(path, "auto-update") {
					body = `{"enabled":false}`
				}
				want := http.StatusForbidden
				h := s.Handler()
				switch scenario {
				case "success":
					want = http.StatusOK
				case "local":
					want = http.StatusOK
					h = s.LocalHandler()
				case "null":
					body = `null`
					want = http.StatusBadRequest
				case "missing":
					body = `{}`
					want = http.StatusBadRequest
					if strings.HasSuffix(path, "refresh") {
						body = `[]`
					}
				case "unknown":
					body = `{"extra":true}`
					want = http.StatusBadRequest
				case "trailing":
					body += ` {}`
					want = http.StatusBadRequest
				case "permission":
					user, _ := s.users.Current(session.UserID, session.Revision)
					if _, err := s.users.Save(auth.UserInput{ID: user.ID, Username: user.Username, Enabled: true, Permissions: []string{"users.manage", "subscriptions.view"}}); err != nil {
						t.Fatal(err)
					}
				}
				r := actionRequest(path, body, session)
				if scenario == "csrf" {
					r.Header.Del("X-KRM-CSRF")
				}
				if scenario == "origin" {
					r.Header.Set("Origin", "https://foreign.invalid")
				}
				if scenario == "method" {
					r.Method = http.MethodGet
					want = http.StatusMethodNotAllowed
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("status=%d want=%d: %s", w.Code, want, w.Body)
				}
				calls := c.toggles + c.refreshes
				if (want == http.StatusOK && calls != 1) || (want != http.StatusOK && calls != 0) || c.enabled {
					t.Fatal("invalid dispatch")
				}
			})
		}
	}
}

func TestSubscriptionUpdateStatusReaderGetsMetadataWithoutSecrets(t *testing.T) {
	c := &subscriptionUpdatesController{}
	s, session := actionServer(t, c)
	user, _ := s.users.Current(session.UserID, session.Revision)
	if _, err := s.users.Save(auth.UserInput{ID: user.ID, Username: user.Username, Enabled: true, Permissions: []string{"users.manage", "subscriptions.view"}}); err != nil {
		t.Fatal(err)
	}
	user = s.users.List()[0]
	session, err := s.sessions.CreateForUser(user, session.Revision, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	r := actionRequest("/api/v1/subscriptions", `{}`, session)
	r.Method = http.MethodGet
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var response struct {
		AutoUpdateEnabled bool                         `json:"auto_update_enabled"`
		States            map[string]model.SourceState `json:"states"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.AutoUpdateEnabled || response.States["a"].NodeCount != 2 || strings.Contains(w.Body.String(), "private-") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reader response: %d %s", w.Code, w.Body)
	}
}

func TestSubscriptionUpdateErrorDoesNotExposeBackendDetails(t *testing.T) {
	c := &subscriptionUpdatesController{err: errors.New("private subscription URL/header or filesystem")}
	s, session := actionServer(t, c)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, actionRequest("/api/v1/subscriptions/refresh", `{}`, session))
	if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("unredacted error: %d %s", w.Code, w.Body)
	}
}
