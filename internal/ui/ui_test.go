package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestStaticAssets(t *testing.T) {
	server := httptest.NewServer(StaticHandler())
	defer server.Close()
	for _, path := range []string{"/", "/assets/app.css", "/assets/app.js", "/manifest.webmanifest", "/sw.js"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
	}
	resp, err := http.Get(server.URL + "/assets/missing.js")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset status = %d", resp.StatusCode)
	}
}

func TestProxyRewritesOriginAndReferer(t *testing.T) {
	var gotOrigin, gotReferer string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrigin = r.Header.Get("Origin")
		gotReferer = r.Header.Get("Referer")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Instance.Role = "ui-proxy"
	cfg.UIProxy.Upstream = upstream.URL
	cfg.UIProxy.InsecureTLS = true
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
