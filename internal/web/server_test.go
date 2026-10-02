package web

import (
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureTLS(t *testing.T) {
	d := t.TempDir()
	c := config.TLS{Enabled: true, AutoGenerate: true, CertFile: filepath.Join(d, "cert.pem"), KeyFile: filepath.Join(d, "key.pem"), Hosts: []string{"krm.local"}}
	if e := EnsureTLS(c, "127.0.0.1:9443"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(c.CertFile); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(c.KeyFile); e != nil {
		t.Fatal(e)
	}
}

func TestUIProxyRewritesOriginAndReferer(t *testing.T) {
	var gotOrigin, gotReferer string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrigin = r.Header.Get("Origin")
		gotReferer = r.Header.Get("Referer")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Instance.Role = "ui"
	cfg.UIProxy.Upstream = upstream.URL
	cfg.UIProxy.InsecureTLS = true
	cfg.Web.TLS.Enabled = false
	handler, err := ProxyHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(handler)
	defer front.Close()

	req, err := http.NewRequest(http.MethodGet, front.URL+"/api/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", front.URL)
	req.Header.Set("Referer", front.URL+"/router")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if gotOrigin != upstream.URL {
		t.Fatalf("origin = %q, want %q", gotOrigin, upstream.URL)
	}
	if !strings.HasPrefix(gotReferer, upstream.URL) {
		t.Fatalf("referer = %q", gotReferer)
	}
}

func TestEmbeddedAssetContract(t *testing.T) {
	c := config.Default()
	h, err := ProxyHandler(func() config.Config { c.UIProxy.Upstream = "http://127.0.0.1:1"; return c }())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, mime string }{{"/", "text/html"}, {"/assets/app.css", "text/css"}, {"/assets/app.js", "javascript"}, {"/sw.js", "javascript"}, {"/manifest.webmanifest", "manifest+json"}} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRecorder()
			h.ServeHTTP(r, httptest.NewRequest("GET", tc.path, nil))
			if r.Code != 200 || !strings.Contains(r.Header().Get("Content-Type"), tc.mime) {
				t.Fatalf("%s: status=%d type=%q", tc.path, r.Code, r.Header().Get("Content-Type"))
			}
		})
	}
}
