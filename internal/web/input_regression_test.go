package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
)

type inputController struct {
	Controller
	state model.State
	store *store.Store
}

func (c inputController) Status(context.Context) core.Status {
	if c.store != nil {
		return core.Status{State: c.store.State()}
	}
	return core.Status{State: c.state}
}

func (c inputController) Nodes() []model.NodeView {
	var nodes []model.NodeView
	for _, node := range c.store.Nodes() {
		nodes = append(nodes, model.NodeView{ID: node.ID, Label: node.Label})
	}
	return nodes
}

func inputStatusRequest(t *testing.T, state model.State, authenticated bool) *httptest.ResponseRecorder {
	return inputRequest(t, inputController{state: state}, "/api/v1/status", authenticated)
}

func inputRequest(t *testing.T, controller Controller, path string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	cfg := config.Default()
	cfg.API.Enabled = false
	s, err := New(cfg, controller, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://127.0.0.1:9443"+path, nil)
	r.RemoteAddr = "192.0.2.10:5000"
	if authenticated {
		session, err := s.sessions.Create("review", "192.0.2.10")
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session.ID})
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestStatusRedactsSensitiveNodeLabels(t *testing.T) {
	const secret = "11111111-2222-3333-4444-555555555555"
	node, err := subscription.ParseVLESS("vless://"+secret+"@example.test:443?type=ws&security=tls#"+secret+"%20token%3DSYNTHETIC_LABEL_TOKEN", "provider")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "state"), filepath.Join(dir, "cache"), model.NewState("review", "slot-", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceNodes([]model.Node{node}); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *model.State) error {
		s.Pool[0].NodeID, s.Pool[0].Label = node.ID, node.Label
		s.ActiveSlot, s.ActiveNodeID, s.XrayConfigured = 0, node.ID, true
		s.LastBenchmark.WinnerID, s.LastBenchmark.WinnerLabel = node.ID, node.Label
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	controller := inputController{store: st}
	for _, path := range []string{"/api/v1/status", "/api/v1/nodes"} {
		w := inputRequest(t, controller, path, true)
		if w.Code != 200 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "SYNTHETIC_LABEL_TOKEN") {
			t.Errorf("label exposure at %s: status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	private := st.State()
	if private.Pool[0].Label != node.Label || private.LastBenchmark.WinnerLabel != node.Label {
		t.Fatal("public projection changed private labels")
	}
	if w := inputRequest(t, controller, "/api/v1/status", false); w.Code != 401 || strings.Contains(w.Body.String(), secret) {
		t.Fatal("authorization boundary failed")
	}
	if err := st.Update(func(s *model.State) error {
		s.Pool[0].Label, s.LastBenchmark.WinnerLabel = "ordinary-node", "ordinary-node"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w := inputRequest(t, controller, "/api/v1/status", true)
	var response struct {
		State model.State `json:"state"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.State.Pool[0].Label != "ordinary-node" || response.State.LastBenchmark.WinnerLabel != "ordinary-node" {
		t.Fatal("ordinary labels changed")
	}
}

func TestStatusRedactsSubscriptionPathSecrets(t *testing.T) {
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "provider", URL: "http://127.0.0.1:0/sub/SYNTHETIC_PATH_TOKEN?token=SYNTHETIC_QUERY_TOKEN", Enabled: true}}
	result := subscription.New(cfg, nil, t.TempDir()).FetchAll(context.Background(), nil, true)
	if len(result.Errors) != 1 {
		t.Fatalf("expected transport failure: %+v", result)
	}
	state := model.NewState("review", "slot-", 1)
	state.Sources = result.States
	w := inputStatusRequest(t, state, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "SYNTHETIC_PATH_TOKEN") || strings.Contains(w.Body.String(), "SYNTHETIC_QUERY_TOKEN") {
		t.Errorf("protected status secret exposure: code=%d body=%s", w.Code, w.Body.String())
	}
	unauthenticated := inputStatusRequest(t, state, false)
	if unauthenticated.Code != 401 || strings.Contains(unauthenticated.Body.String(), "SYNTHETIC_PATH_TOKEN") {
		t.Fatal("authorization boundary failed")
	}
}

func TestSameOriginRejectsDNSRebindingOnPlaintextAPI(t *testing.T) {
	tests := []struct {
		name   string
		tls    bool
		host   string
		origin string
		want   bool
	}{
		{name: "loopback IPv4 HTTP", host: "127.0.0.1:9443", origin: "http://127.0.0.1:9443", want: true},
		{name: "loopback IPv6 HTTP", host: "[::1]:9443", origin: "http://[::1]:9443", want: true},
		{name: "rebinding hostname HTTP", host: "attacker.example:9443", origin: "http://attacker.example:9443", want: false},
		{name: "trusted hostname HTTPS", tls: true, host: "controller.example:9443", origin: "https://controller.example:9443", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9443/api/v1/auth/login", strings.NewReader("{}"))
			req.Host = tc.host
			req.Header.Set("Origin", tc.origin)
			if got := sameOrigin(req, tc.tls); got != tc.want {
				t.Fatalf("sameOrigin() = %t, want %t for host=%q origin=%q tls=%t", got, tc.want, tc.host, tc.origin, tc.tls)
			}
		})
	}
}
