package components

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type fakeRoundTrip func(*http.Request) (*http.Response, error)

func (f fakeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSystemBackendDetectsExistingXrayAndFetchesReleaseMetadata(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "xray")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'Xray 26.9.9 (Xray, Penetrates Everything.)'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Platform.Kind = "linux-systemd"
	cfg.Xray.Binary = binary
	httpClient := &http.Client{Transport: fakeRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" || !strings.Contains(r.URL.Path, "/XTLS/Xray-core/") {
			t.Errorf("untrusted version source %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v26.9.9"}`)), Header: make(http.Header)}, nil
	})}
	backend := NewSystemBackend(cfg, httpClient)
	installed, err := backend.Installed(context.Background(), Xray)
	if err != nil || installed != "26.9.9" {
		t.Fatalf("installed %q %v", installed, err)
	}
	latest, err := backend.Latest(context.Background(), Xray)
	if err != nil || latest != "26.9.9" {
		t.Fatalf("latest %q %v", latest, err)
	}
}
