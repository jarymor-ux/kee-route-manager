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
)

type panelTestController struct {
	*permissionsController
	aliases  int
	dnsError error
}

func (p *panelTestController) PanelDNSAutomatic() bool { return true }
func (p *panelTestController) EnsurePanelAlias(_ context.Context, hostname, ip string) error {
	if hostname != "alice.jopa" || ip != "192.168.1.1" {
		return errors.New("unexpected alias")
	}
	p.aliases++
	return p.dnsError
}

func TestPanelApplyPinsPreparedRevisionAndVerifiesDNSBeforeQueuing(t *testing.T) {
	s, controller := newUsersTestServer(t)
	p := &panelTestController{permissionsController: controller}
	s.mgr = p
	session := loginTestUser(t, s, "admin", "synthetic-password")
	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	panel := config.PanelStatus{Supported: true, Hostname: "alice.jopa", ListenIP: "192.168.1.1", Port: 9445, URL: "https://alice.jopa:9445", Revision: revision, Status: "prepared"}
	posts := 0
	s.cfg.Update.LauncherSocket = launcherSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/panel/status" {
			writeJSON(w, 200, panel)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/panel/apply" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var input config.PanelRevision
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Revision != revision {
			t.Error("revision not forwarded")
		}
		posts++
		writeJSON(w, 202, map[string]bool{"accepted": true})
	}))
	for _, tc := range []struct {
		name, body           string
		dnsError             error
		want, aliases, posts int
	}{
		{"stale", `{"revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`, nil, 409, 0, 0},
		{"dns-error", `{"revision":"` + revision + `"}`, errors.New("token=private-dns-secret"), 409, 1, 0},
		{"accepted", `{"revision":"` + revision + `"}`, nil, 202, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.aliases, posts, p.dnsError = 0, 0, tc.dnsError
			w := userRequest(t, s, session, "POST", "/api/v1/panel/apply", tc.body)
			if w.Code != tc.want || p.aliases != tc.aliases || posts != tc.posts || strings.Contains(w.Body.String(), "private-dns-secret") {
				t.Fatalf("status=%d aliases=%d posts=%d body=%s", w.Code, p.aliases, posts, w.Body)
			}
		})
	}
	panel.Hostname = "192.168.1.1"
	p.aliases, posts = 0, 0
	w := userRequest(t, s, session, "POST", "/api/v1/panel/apply", `{"revision":"`+revision+`"}`)
	if w.Code != 202 || p.aliases != 0 || posts != 1 {
		t.Fatal("port-only IP change attempted DNS mutation")
	}
	panel.Hostname = "192.168.1.2"
	posts = 0
	w = userRequest(t, s, session, "POST", "/api/v1/panel/apply", `{"revision":"`+revision+`"}`)
	if w.Code != 400 || posts != 0 {
		t.Fatal("foreign panel IP accepted")
	}
}

func TestPanelAPIRequiresManagementAndCSRFAndStrictInput(t *testing.T) {
	s, controller := newUsersTestServer(t)
	p := &panelTestController{permissionsController: controller}
	s.mgr = p
	user, err := s.users.Save(auth.UserInput{Username: "panel-user", Password: "panel-user-password", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	session := loginTestUser(t, s, user.Username, "panel-user-password")
	calls := 0
	s.cfg.Update.LauncherSocket = launcherSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeJSON(w, 200, config.PanelStatus{Supported: true, Status: "idle", Hostname: "alice.jopa", ListenIP: "192.168.1.1", Port: 443, URL: "https://alice.jopa"})
	}))
	for _, permission := range []string{"", "vpn.control", "router.system", "updates.manage", "config.manage", "users.manage"} {
		grants := []string{}
		if permission != "" {
			grants = append(grants, permission)
		}
		user, err = s.users.Save(auth.UserInput{ID: user.ID, Username: user.Username, Enabled: true, Permissions: grants})
		if err != nil {
			t.Fatal(err)
		}
		allowed := permission == "config.manage" || permission == "users.manage"
		for _, path := range []string{"status", "prepare", "apply", "confirm"} {
			method, body := "POST", `{}`
			if path == "status" {
				method = "GET"
			}
			w := userRequest(t, s, session, method, "/api/v1/panel/"+path, body)
			if !allowed && w.Code != 403 || allowed && w.Code == 403 {
				t.Fatalf("permission=%s path=%s status=%d", permission, path, w.Code)
			}
		}
	}
	for _, body := range []string{`null`, `{}`, `{"hostname":"alice.jopa","port":443,"path":"/private"}`, `{"hostname":"evil.local","port":443}`, `{"hostname":"alice.jopa","port":9443}`, `{"hostname":"alice.jopa","port":443} {}`} {
		before := calls
		w := userRequest(t, s, session, "POST", "/api/v1/panel/prepare", body)
		if w.Code != 400 || calls != before {
			t.Fatalf("invalid input reached launcher: status=%d body=%s", w.Code, body)
		}
	}
	for _, bad := range []string{"csrf", "origin"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/v1/panel/prepare", strings.NewReader(`{"hostname":"alice.jopa","port":443}`))
		r.RemoteAddr = "127.0.0.1:32100"
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session.ID})
		r.Header.Set("X-KRM-CSRF", session.CSRF)
		if bad == "csrf" {
			r.Header.Del("X-KRM-CSRF")
		} else {
			r.Header.Set("Origin", "https://attacker.invalid")
		}
		w := httptest.NewRecorder()
		before := calls
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 || calls != before {
			t.Fatal("unsafe panel mutation authorized")
		}
	}
	w := userRequest(t, s, session, "GET", "/api/v1/panel/status", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `"dns_automatic":true`) {
		t.Fatal("missing DNS capability or private-cache policy")
	}
}

func TestPanelStatusOldLauncherIsExplicitlyUnsupported(t *testing.T) {
	s, _ := newUsersTestServer(t)
	session := loginTestUser(t, s, "admin", "synthetic-password")
	s.cfg.Update.LauncherSocket = launcherSocket(t, http.NotFoundHandler())
	w := userRequest(t, s, session, "GET", "/api/v1/panel/status", "")
	var status config.PanelStatus
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Supported || status.Status != "idle" {
		t.Fatal("old launcher advertised address support")
	}
}
