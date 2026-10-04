package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestSignedManifestValidationAndAvailability(t *testing.T) {
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		change    func(*Manifest, *config.Update)
		current   string
		trailer   string
		wantErr   bool
		available bool
	}{
		{name: "upgrade", available: true},
		{name: "same version", current: "1.0.0-rc.2"},
		{name: "older release", current: "1.0.0-rc.3"},
		{name: "explicit downgrade", current: "1.0.0-rc.3", change: func(_ *Manifest, c *config.Update) { c.AllowDowngrade = true }, available: true},
		{name: "metadata is not an upgrade", current: "1.0.0-rc.2+build", change: func(m *Manifest, _ *config.Update) { m.Version += "+other" }},
		{name: "invalid current", current: "garbage", wantErr: true},
		{name: "invalid version", change: func(m *Manifest, _ *config.Update) { m.Version = "1.0" }, wantErr: true},
		{name: "future schema", change: func(m *Manifest, _ *config.Update) { m.SchemaVersion++ }, wantErr: true},
		{name: "incompatible config", change: func(m *Manifest, _ *config.Update) { m.MinConfigSchema = config.SchemaVersion + 1 }, wantErr: true},
		{name: "wrong channel", change: func(m *Manifest, _ *config.Update) { m.Channel = "stable" }, wantErr: true},
		{name: "prerelease on stable", change: func(m *Manifest, c *config.Update) { m.Channel = "stable"; c.Channel = "stable" }, wantErr: true},
		{name: "missing daemon", change: func(m *Manifest, _ *config.Update) { m.Assets[0].Component = "ui" }, wantErr: true},
		{name: "wrong platform", change: func(m *Manifest, _ *config.Update) { m.Assets[0].Arch = "unsupported" }, wantErr: true},
		{name: "empty binary", change: func(m *Manifest, _ *config.Update) { m.Assets[0].Size = 0 }, wantErr: true},
		{name: "oversized binary", change: func(m *Manifest, _ *config.Update) { m.Assets[0].Size = 200<<20 + 1 }, wantErr: true},
		{name: "short checksum", change: func(m *Manifest, _ *config.Update) { m.Assets[0].SHA256 = "abc" }, wantErr: true},
		{name: "nonhex checksum", change: func(m *Manifest, _ *config.Update) { m.Assets[0].SHA256 = strings.Repeat("z", 64) }, wantErr: true},
		{name: "plaintext binary", change: func(m *Manifest, _ *config.Update) { m.Assets[0].URL = "http://example.invalid/binary" }, wantErr: true},
		{name: "credentialed binary", change: func(m *Manifest, _ *config.Update) { m.Assets[0].URL = "https://user:pass@example.invalid/binary" }, wantErr: true},
		{name: "trailing signed JSON", trailer: "{}", wantErr: true},
		{name: "invalid public key", change: func(_ *Manifest, c *config.Update) { c.PublicKey = "invalid" }, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default().Update
			cfg.Enabled, cfg.Channel = true, "rc"
			cfg.GitHubRepository = ""
			cfg.ManifestURL, cfg.SignatureURL = "https://example.invalid/manifest", "https://example.invalid/sig"
			cfg.PublicKey = base64.RawStdEncoding.EncodeToString(pub)
			m := Manifest{SchemaVersion: 1, Version: "1.0.0-rc.2", Channel: "rc", MinConfigSchema: 1, Assets: []Asset{{Component: "daemon", OS: runtime.GOOS, Arch: runtime.GOARCH, URL: "https://example.invalid/binary", Size: 128, SHA256: strings.Repeat("1", 64)}}}
			if tc.change != nil {
				tc.change(&m, &cfg)
			}
			body, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			body = append(body, tc.trailer...)
			// Raw 64-byte signatures exercise the other supported wire encoding.
			sig := ed25519.Sign(private, body)
			current := tc.current
			if current == "" {
				current = "1.0.0-rc.1"
			}
			u := New(cfg, current)
			u.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				data := body
				if r.URL.Path == "/sig" {
					data = sig
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header), Request: r}, nil
			})
			result, err := u.Check(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("validation error = %v; want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && (result.Available != tc.available || result.CurrentVersion != current || result.LatestVersion != m.Version || result.Asset != m.Assets[0]) {
				t.Fatalf("incorrect availability or selected artifact: %+v", result)
			}
			if tc.wantErr && result.Available {
				t.Fatal("invalid release exposed as available")
			}
		})
	}
}

func TestUpdateDownloadLimitsStatusAndCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("12345"))
	}))
	defer server.Close()
	u := New(config.Default().Update, "1.0.0-rc.2")
	u.client.Transport = server.Client().Transport
	for _, tc := range []struct {
		path    string
		max     int64
		wantErr bool
	}{
		{"/ok", 5, false}, {"/ok", 4, true}, {"/error", 100, true},
	} {
		body, err := u.download(context.Background(), server.URL+tc.path, tc.max)
		if (err != nil) != tc.wantErr || err == nil && string(body) != "12345" {
			t.Fatalf("path=%s max=%d got %q %v", tc.path, tc.max, body, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := u.download(ctx, server.URL, 5); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
