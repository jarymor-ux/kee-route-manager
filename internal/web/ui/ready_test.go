package ui

import (
	"context"
	"encoding/pem"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestReadyVerifiesStaticControllerAndTrust(t *testing.T) {
	health := `{"role":"controller","status":"safe_degraded"}`
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>UI</html>"))
			return
		}
		if r.URL.Path != "/healthz" {
			t.Errorf("unexpected readiness request %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(health))
	}))
	defer srv.Close()
	c := config.Default()
	c.Instance.Role = "ui"
	u, _ := url.Parse(srv.URL)
	c.Web.Listen = u.Host
	c.Web.TLS.CertFile = filepath.Join(t.TempDir(), "ui.crt")
	if e := os.WriteFile(c.Web.TLS.CertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	if e := Ready(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	health = `{"role":"other","status":"ok"}`
	if e := Ready(context.Background(), c); e == nil {
		t.Fatal("accepted unrelated upstream health")
	}
	c.Web.TLS.CertFile = filepath.Join(t.TempDir(), "missing")
	if e := Ready(context.Background(), c); e == nil {
		t.Fatal("readiness generated missing certificate")
	}
	if _, e := os.Stat(c.Web.TLS.CertFile); !os.IsNotExist(e) {
		t.Fatal("readiness created certificate")
	}
}
func TestReadyRejectsDegradedController(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>UI</html>"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := config.Default()
	c.Instance.Role = "ui"
	c.Web.TLS.Enabled = false
	u, _ := url.Parse(srv.URL)
	c.Web.Listen = u.Host
	if e := Ready(context.Background(), c); e == nil {
		t.Fatal("accepted unavailable controller")
	}
}
