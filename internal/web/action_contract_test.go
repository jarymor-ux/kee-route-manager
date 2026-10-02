package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type actionController struct {
	Controller
	calls []string
	index int
	err   error
}

func (c *actionController) SwitchSlot(_ context.Context, index int) error {
	c.index = index
	c.calls = append(c.calls, "switch")
	return c.err
}
func (c *actionController) SwitchDirect(context.Context) error {
	c.calls = append(c.calls, "direct")
	return c.err
}
func (c *actionController) RestartXray(context.Context) error {
	c.calls = append(c.calls, "restart")
	return c.err
}
func (c *actionController) Reboot(context.Context) error {
	c.calls = append(c.calls, "reboot")
	return c.err
}
func (c *actionController) Wake(_ context.Context, mac string) error {
	c.calls = append(c.calls, "wake:"+mac)
	return c.err
}
func (c *actionController) SetPolicy(_ context.Context, mac, policy string) error {
	c.calls = append(c.calls, "policy:"+mac+":"+policy)
	return c.err
}
func (c *actionController) RestoreOriginalXray(context.Context) error {
	c.calls = append(c.calls, "restore")
	return c.err
}

func actionServer(t *testing.T, c Controller) (*Server, auth.Session) {
	t.Helper()
	cfg := config.Default()
	cfg.API.Enabled = false
	s, err := New(cfg, c, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.sessions.Create("admin", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	return s, session
}

func actionRequest(path, body string, session auth.Session) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://controller.test"+path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.10:4567"
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session.ID})
	r.Header.Set("X-KRM-CSRF", session.CSRF)
	r.Header.Set("Origin", "https://controller.test")
	return r
}

func TestSwitchRequiresExplicitNonnegativeIndex(t *testing.T) {
	for _, local := range []bool{false, true} {
		for _, body := range []string{`{}`, `null`, `{"index":null}`, `{"index":-1}`, `{"index":1.5}`, `{"index":"0"}`, `{"index":0,"extra":1}`, `{"index":0} {}`} {
			t.Run(body+map[bool]string{false: "/network", true: "/local"}[local], func(t *testing.T) {
				c := &actionController{}
				s, session := actionServer(t, c)
				h := s.Handler()
				if local {
					h = s.LocalHandler()
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, actionRequest("/api/v1/actions/switch", body, session))
				if w.Code != http.StatusBadRequest || len(c.calls) != 0 {
					t.Fatalf("invalid selection changed route: status=%d calls=%v index=%d", w.Code, c.calls, c.index)
				}
			})
		}
	}
}

func TestMutationAuthorizationAndDispatch(t *testing.T) {
	cases := []struct {
		path, body, call string
		success          int
	}{
		{"switch", `{"index":0}`, "switch", 200},
		{"direct", `{}`, "direct", 200},
		{"xray-restart", `{}`, "restart", 200},
		{"reboot", `{}`, "reboot", 202},
		{"wake", `{"mac":"02:00:00:00:00:01"}`, "wake:02:00:00:00:00:01", 200},
		{"policy", `{"mac":"02:00:00:00:00:01","policy":"default"}`, "policy:02:00:00:00:00:01:default", 200},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			for _, invalid := range []string{"cookie", "csrf", "origin", "method", "address", "controller", ""} {
				t.Run(invalid, func(t *testing.T) {
					c := &actionController{}
					s, session := actionServer(t, c)
					r := actionRequest("/api/v1/actions/"+tc.path, tc.body, session)
					want := tc.success
					switch invalid {
					case "cookie":
						r.Header.Del("Cookie")
						want = 401
					case "csrf":
						r.Header.Set("X-KRM-CSRF", "incorrect")
						want = 403
					case "origin":
						r.Header.Set("Origin", "https://attacker.test")
						want = 403
					case "method":
						r.Method = http.MethodGet
						want = 405
					case "address":
						r.RemoteAddr = "192.0.2.11:4567"
						want = 401
					case "controller":
						c.err = errors.New("SYNTHETIC_PRIVATE_FAILURE")
						want = 409
					}
					w := httptest.NewRecorder()
					s.Handler().ServeHTTP(w, r)
					if w.Code != want || strings.Contains(w.Body.String(), "SYNTHETIC_PRIVATE_FAILURE") {
						t.Fatalf("status=%d body=%s", w.Code, w.Body)
					}
					if invalid == "" || invalid == "controller" {
						if len(c.calls) != 1 || c.calls[0] != tc.call {
							t.Fatalf("dispatch=%v", c.calls)
						}
					} else if len(c.calls) != 0 {
						t.Fatalf("unauthorized mutation: %v", c.calls)
					}
					if invalid == "address" {
						if _, ok := s.sessions.Get(session.ID); ok {
							t.Fatal("changed-address session remained usable")
						}
					}
				})
			}
		})
	}
}

func TestLocalRestoreAndDisabledUpdate(t *testing.T) {
	c := &actionController{}
	s, session := actionServer(t, c)
	for _, tc := range []struct {
		local bool
		path  string
		want  int
	}{
		{false, "/api/v1/actions/restore-xray", 404},
		{true, "/api/v1/actions/restore-xray", 200},
		{false, "/api/v1/update/apply", 501},
		{true, "/api/v1/update/apply", 501},
	} {
		h := s.Handler()
		if tc.local {
			h = s.LocalHandler()
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, actionRequest(tc.path, `{}`, session))
		if w.Code != tc.want {
			t.Fatalf("%s local=%t: %d", tc.path, tc.local, w.Code)
		}
	}
	if len(c.calls) != 1 || c.calls[0] != "restore" {
		t.Fatalf("unexpected side effects: %v", c.calls)
	}
	c.err = errors.New("private restore error")
	w := httptest.NewRecorder()
	s.LocalHandler().ServeHTTP(w, actionRequest("/api/v1/actions/restore-xray", `{}`, session))
	if w.Code != 409 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("restore failure: %d %s", w.Code, w.Body)
	}
}
