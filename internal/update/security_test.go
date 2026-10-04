package update

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSemVerPrecedence(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.0.0-rc.1", "1.0.0-rc.2", -1}, {"1.0.0-rc.9", "1.0.0-rc.10", -1},
		{"1.0.0-rc.10", "1.0.0", -1}, {"1.0.0+build1", "1.0.0+build2", 0},
		{"v1.2.0", "1.1.9", 1}, {"1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("%s vs %s: %d", tc.a, tc.b, got)
		}
	}
}

func TestRejectMalformedSemVer(t *testing.T) {
	for _, v := range []string{"1.0", "01.0.0", "1.0.0-rc.01", "1.0.0+", "1.0.0-x..y", "1.0.0 garbage"} {
		if _, err := parseSemVer(v); err == nil {
			t.Errorf("accepted %s", v)
		}
	}
}
func TestDowngradeAndMetadataPrecedence(t *testing.T) {
	if compareVersions("1.0.0-rc.1", "1.0.0-rc.2") >= 0 {
		t.Fatal("downgrade accepted as upgrade")
	}
	if compareVersions("1.0.0+build1", "1.0.0+build2") != 0 {
		t.Fatal("build metadata changed precedence")
	}
}
func TestSecureUpdateURL(t *testing.T) {
	for _, raw := range []string{"http://example.test/manifest", "https://user:pass@example.test/manifest", "https:///manifest"} {
		if secureURL(raw) {
			t.Errorf("unsafe %s", raw)
		}
	}
}

func TestUpdateRejectsHTTPSDowngradeRedirect(t *testing.T) {
	plainHit := false
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { plainHit = true; w.Write([]byte("untrusted")) }))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, plain.URL, http.StatusFound) }))
	defer secure.Close()
	cfg := config.Default().Update
	cfg.Enabled = true
	cfg.ManifestURL = secure.URL
	u := New(cfg, "1.0.0-rc.2")
	u.client.Transport = secure.Client().Transport
	if _, err := u.Check(context.Background()); err == nil {
		t.Fatal("downgrade redirect accepted")
	}
	if plainHit {
		t.Fatal("plaintext request was sent")
	}
}
