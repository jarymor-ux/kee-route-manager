package web

import (
	"encoding/pem"
	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	webui "github.com/jarymor-ux/kee-route-manager/internal/web/ui"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegressionProxyClientLoginBudgets(t *testing.T) {
	c := config.Default()
	c.Web.CredentialsFile = filepath.Join(t.TempDir(), "credentials.json")
	if err := auth.CreateCredentials(c.Web.CredentialsFile, "admin", "synthetic-password"); err != nil {
		t.Fatal(err)
	}
	s, err := New(c, &fakeController{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(s.Handler())
	defer upstream.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	uc := config.Default()
	uc.UIProxy.Upstream = upstream.URL
	uc.UIProxy.UpstreamCAFile = ca
	uc.Web.TLS.Enabled = false
	proxy, err := webui.ProxyHandler(uc)
	if err != nil {
		t.Fatal(err)
	}
	request := func(remote, body, forwarded string) int {
		r := httptest.NewRequest("POST", "http://ui.local/api/v1/auth/login", strings.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Origin", "http://ui.local")
		r.Header.Set("X-Forwarded-For", forwarded)
		r.Header.Set("Forwarded", "for="+forwarded)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 8; i++ {
		if code := request("192.0.2.1:3000", "!", "198.51.100.1"); code != 400 {
			t.Fatalf("attempt %d=%d", i, code)
		}
	}
	valid := `{"username":"admin","password":"synthetic-password"}`
	// Arbitrary forwarded headers cannot reset the attacker's actual edge quota.
	if code := request("192.0.2.1:3001", valid, "198.51.100.99"); code != 429 {
		t.Fatalf("spoofed identity bypass: %d", code)
	}
	if code := request("192.0.2.2:3002", valid, "198.51.100.1"); code != 200 {
		t.Fatalf("unrelated victim locked out: %d", code)
	}
	// Successful authentication resets only this client's edge failure quota.
	for i := 0; i < 8; i++ {
		if code := request("192.0.2.2:3002", valid, "198.51.100.1"); code != 200 {
			t.Fatalf("successful client quota not reset at %d: %d", i, code)
		}
	}
	// Direct API clients retain their own quota and cannot spoof an edge identity.
	for i := 0; i < 8; i++ {
		r := httptest.NewRequest("POST", "https://controller.local/api/v1/auth/login", strings.NewReader("!"))
		r.RemoteAddr = "192.0.2.3:4000"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	r := httptest.NewRequest("POST", "https://controller.local/api/v1/auth/login", strings.NewReader(valid))
	r.RemoteAddr = "192.0.2.3:4001"
	r.Header.Set("X-Forwarded-For", "192.0.2.4")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatal("API forwarded header bypass", w.Code)
	}
}
