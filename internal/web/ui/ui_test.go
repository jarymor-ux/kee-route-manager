package ui

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestProxyTrustAndPin(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer upstream.Close()
	der := upstream.Certificate().Raw
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(upstream.Certificate().RawSubjectPublicKeyInfo)
	c := config.Default()
	c.UIProxy.Upstream = upstream.URL
	c.UIProxy.UpstreamCAFile = ca
	c.UIProxy.UpstreamSPKISHA256 = base64.StdEncoding.EncodeToString(digest[:])
	for _, valid := range []bool{true, false} {
		if !valid {
			c.UIProxy.UpstreamSPKISHA256 = base64.StdEncoding.EncodeToString(make([]byte, 32))
		}
		h, e := ProxyHandler(c)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/status", nil))
		want := 200
		if !valid {
			want = 502
		}
		if r.Code != want {
			t.Fatalf("pin_valid=%t status=%d body=%s", valid, r.Code, r.Body)
		}
	}
}
func TestProxyMarksSessionCookieSecureAtHTTPSBoundary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     "krm_session",
			Value:    "session-id",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	c := config.Default()
	c.UIProxy.Upstream = upstream.URL
	c.Web.TLS.Enabled = true
	h, err := ProxyHandler(c)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "https://krm.local/api/v1/auth/login", strings.NewReader("{}"))
	req.Host = "krm.local"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "krm_session" || !cookies[0].Secure {
		t.Fatalf("session cookie not secured at HTTPS boundary: %#v", cookies)
	}
}

func TestProxyRejectsForeignOriginBeforeRewrite(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer upstream.Close()
	c := config.Default()
	c.UIProxy.Upstream = upstream.URL
	c.Web.TLS.Enabled = false
	h, e := ProxyHandler(c)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "http://krm.local/api/v1/actions/direct", nil)
	r.Header.Set("Origin", "http://evil.local")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatalf("origin bypass: code=%d calls=%d", w.Code, calls)
	}
}
func TestTLSMinimumAndInvalidCA(t *testing.T) {
	c := config.UIProxy{UpstreamCAFile: filepath.Join(t.TempDir(), "ca")}
	if e := os.WriteFile(c.UpstreamCAFile, []byte("invalid"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := upstreamTLS(c); e == nil {
		t.Fatal("accepted invalid CA")
	}
	c.UpstreamCAFile = ""
	c.UpstreamSPKISHA256 = "bad"
	if _, e := upstreamTLS(c); e == nil {
		t.Fatal("accepted invalid pin")
	}
}
func TestServiceWorkerAssets(t *testing.T) {
	b, e := staticFS.ReadFile("static/sw.js")
	if e != nil {
		t.Fatal(e)
	}
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	scripts := regexp.MustCompile(`<script\b[^>]*src="/assets/([a-z-]+\.js)"[^>]*>`).FindAllStringSubmatch(string(index), -1)
	var scriptNames []string
	paths := []string{"/", "/assets/app.css", "/manifest.webmanifest"}
	for _, script := range scripts {
		if !strings.Contains(script[0], " defer") {
			t.Fatal("workspace scripts must defer until DOM is ready")
		}
		scriptNames = append(scriptNames, script[1])
		paths = append(paths, "/assets/"+script[1])
	}
	want := "app.js,workspace.js,charts.js,dialogs.js,settings.js,panel.js,boot.js"
	if strings.Join(scriptNames, ",") != want {
		t.Fatalf("workspace initialization order: %v", scriptNames)
	}
	for _, path := range paths {
		if !strings.Contains(string(b), "'"+path+"'") {
			t.Fatalf("service worker missing %s", path)
		}
		w := httptest.NewRecorder()
		StaticHandler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("service worker asset %s status=%d", path, w.Code)
		}
	}
}
