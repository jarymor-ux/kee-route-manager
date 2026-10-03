package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSignedManifest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var manifest []byte
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, r *http.Request) { w.Write(manifest) })
	mux.HandleFunc("/sig", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(base64.RawStdEncoding.EncodeToString(ed25519.Sign(priv, manifest))))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	m := Manifest{SchemaVersion: 1, Version: "1.0.1", Channel: "rc", PublishedAt: time.Now(), MinConfigSchema: 1, Assets: []Asset{{OS: runtime.GOOS, Arch: runtime.GOARCH, URL: srv.URL + "/asset", SHA256: strings.Repeat("0", 64), Size: 1}}}
	manifest, _ = json.Marshal(m)
	c := config.Default().Update
	c.Enabled = true
	c.Channel = "rc"
	c.ManifestURL = srv.URL + "/manifest"
	c.SignatureURL = srv.URL + "/sig"
	c.PublicKey = base64.RawStdEncoding.EncodeToString(pub)
	u := New(c, "1.0.0-rc.1")
	u.client = srv.Client()
	r, e := u.Check(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !r.Available || r.LatestVersion != "1.0.1" {
		t.Fatalf("bad result %#v", r)
	}
}

func TestRejectsTamperedManifest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	original := []byte(`{"schema_version":1,"version":"1.0.1","channel":"rc","published_at":"2026-10-02T00:00:00Z","min_config_schema":1,"assets":[]}`)
	signature := base64.RawStdEncoding.EncodeToString(ed25519.Sign(priv, original))
	tampered := []byte(`{"schema_version":1,"version":"9.9.9","channel":"rc","published_at":"2026-10-02T00:00:00Z","min_config_schema":1,"assets":[]}`)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sig" {
			_, _ = w.Write([]byte(signature))
			return
		}
		_, _ = w.Write(tampered)
	}))
	defer server.Close()
	cfg := config.Default().Update
	cfg.Enabled = true
	cfg.Channel = "rc"
	cfg.ManifestURL = server.URL + "/manifest"
	cfg.SignatureURL = server.URL + "/sig"
	cfg.PublicKey = base64.RawStdEncoding.EncodeToString(pub)
	u := New(cfg, t.TempDir(), "1.0.0-rc.1")
	u.client = server.Client()
	if _, err := u.Check(context.Background()); err == nil {
		t.Fatal("tampered manifest accepted")
	}
}
