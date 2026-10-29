package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/event"
)

type nodeSelectionController struct {
	Controller
	id      string
	enabled bool
	calls   int
	err     error
	audits  []event.Event
}

func (c *nodeSelectionController) SetNodeAutoSelect(_ context.Context, id string, enabled bool) error {
	c.id, c.enabled = id, enabled
	c.calls++
	return c.err
}
func (c *nodeSelectionController) RecordAudit(e event.Event) { c.audits = append(c.audits, e) }

func TestNodeAutoSelectAPIRequiresExplicitFields(t *testing.T) {
	for _, local := range []bool{false, true} {
		for _, body := range []string{`{}`, `null`, `{"id":"fallback"}`, `{"id":"fallback","enabled":null}`, `{"enabled":false}`, `{"id":"","enabled":false}`, `{"id":"fallback","enabled":0}`, `{"id":"fallback","enabled":false,"extra":1}`, `{"id":"fallback","enabled":false} {}`} {
			t.Run(body+map[bool]string{false: "/network", true: "/local"}[local], func(t *testing.T) {
				c := &nodeSelectionController{}
				s, session := actionServer(t, c)
				s.cfg.API.TLS.Enabled = true
				h := s.Handler()
				if local {
					h = s.LocalHandler()
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, actionRequest("/api/v1/nodes/auto-select", body, session))
				if w.Code != http.StatusBadRequest || c.calls != 0 {
					t.Fatalf("invalid preference reached core: status=%d calls=%d", w.Code, c.calls)
				}
			})
		}
	}
}

func TestNodeAutoSelectAPIAuthorizationAndDispatch(t *testing.T) {
	for _, scenario := range []string{"success", "local", "csrf", "origin", "method", "permission"} {
		t.Run(scenario, func(t *testing.T) {
			c := &nodeSelectionController{}
			s, session := actionServer(t, c)
			s.cfg.API.TLS.Enabled = true
			r := actionRequest("/api/v1/nodes/auto-select", `{"id":"fallback","enabled":false}`, session)
			want := http.StatusForbidden
			h := s.Handler()
			switch scenario {
			case "success":
				want = http.StatusOK
			case "local":
				h, want = s.LocalHandler(), http.StatusOK
			case "csrf":
				r.Header.Del("X-KRM-CSRF")
			case "origin":
				r.Header.Set("Origin", "https://foreign.example")
			case "method":
				r.Method, want = http.MethodGet, http.StatusMethodNotAllowed
			case "permission":
				user, _ := s.users.Current(session.UserID, session.Revision)
				if _, err := s.users.Save(auth.UserInput{ID: user.ID, Username: user.Username, Enabled: true, Permissions: []string{"users.manage", "vpn.view"}}); err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != want {
				t.Fatalf("status=%d want=%d: %s", w.Code, want, w.Body)
			}
			if want == http.StatusOK {
				if c.calls != 1 || c.id != "fallback" || c.enabled {
					t.Fatal("preference dispatched incorrectly")
				}
				if scenario == "success" && (len(c.audits) != 1 || c.audits[0].Type != "api.action" || c.audits[0].Fields["path"] != "/api/v1/nodes/auto-select") {
					t.Fatal("accepted preference was not audited")
				}
			} else if c.calls != 0 {
				t.Fatal("unauthorized preference reached controller")
			}
		})
	}
}

func TestNodeAutoSelectAPIReportsActiveConflictWithoutBackendDetails(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{core.ErrActiveNode, http.StatusConflict, "node_active"},
		{core.ErrNodeNotFound, http.StatusNotFound, "node_not_found"},
		{errors.New("private filesystem details"), http.StatusConflict, "conflict"},
	} {
		c := &nodeSelectionController{err: tc.err}
		s, session := actionServer(t, c)
		s.cfg.API.TLS.Enabled = true
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, actionRequest("/api/v1/nodes/auto-select", `{"id":"fallback","enabled":false}`, session))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(w.Body.String(), "private filesystem") {
			t.Fatalf("incorrect public error: %d %s", w.Code, w.Body)
		}
	}
}
