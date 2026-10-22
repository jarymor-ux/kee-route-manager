package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestPlainHTTPProxyClearsUpstreamSecureSessionCookie(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name: "krm_session", Value: "session-id", Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
		})
		http.SetCookie(w, &http.Cookie{Name: "other", Value: "untouched", Secure: true})
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	c := config.Default()
	c.UIProxy.Upstream = upstream.URL
	c.Web.TLS.Enabled = false
	proxy, err := ProxyHandler(c)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://192.168.1.1:9444/api/v1/auth/login", strings.NewReader("{}"))
	req.Header.Set("Origin", "http://192.168.1.1:9444")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 2 || cookies[0].Name != "krm_session" || cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || !cookies[1].Secure {
		t.Fatalf("incorrect session boundary cookies: %#v", cookies)
	}
}
