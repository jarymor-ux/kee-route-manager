package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
)

func githubFixture(tag string, pre bool) githubRelease {
	base := "https://github.com/owner/repo/releases/download/" + tag + "/"
	channel := "stable"
	if pre {
		channel = "rc"
	}
	return githubRelease{TagName: tag, Prerelease: pre, Assets: []githubAsset{{Name: "manifest-" + channel + ".json", URL: base + "manifest-" + channel + ".json"}, {Name: "manifest-" + channel + ".json.sig", URL: base + "manifest-" + channel + ".json.sig"}}}
}
func TestGitHubChannelSelectionUsesPrereleaseAndSemVer(t *testing.T) {
	rs := []githubRelease{githubFixture("v1.0.0-rc.9", true), githubFixture("v1.0.0", false), githubFixture("v1.0.0-rc.10", true), githubFixture("v1.1.0-rc.1", true)}
	rs[3].Draft = true
	for _, tc := range []struct{ channel, want string }{{"rc", "v1.0.0-rc.10"}, {"stable", "v1.0.0"}} {
		_, _, tag, err := releaseManifest(rs, "owner/repo", tc.channel)
		if err != nil || tag != tc.want {
			t.Fatalf("channel %s: %s,%v", tc.channel, tag, err)
		}
	}
}
func TestGitHubRejectsUnpinnedManifestAndInconsistentTag(t *testing.T) {
	rs := []githubRelease{githubFixture("v1.0.0-rc.10", true)}
	rs[0].Assets[0].URL = "https://attacker.example/manifest-rc.json"
	if _, _, _, err := releaseManifest(rs, "owner/repo", "rc"); err == nil {
		t.Fatal("foreign asset accepted")
	}
	rs = []githubRelease{githubFixture("v1.0.0", true)}
	if _, _, _, err := releaseManifest(rs, "owner/repo", "rc"); err == nil {
		t.Fatal("stable SemVer published as RC accepted")
	}
}

func TestGitHubDiscoveryVerifiesSignedVersionAndDowngradePolicy(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	cfg := config.Default().Update
	cfg.Enabled = true
	cfg.GitHubRepository = "owner/repo"
	cfg.PublicKey = base64.RawStdEncoding.EncodeToString(pub)
	for _, tc := range []struct {
		name, version, tag string
		wantErr, available bool
	}{{"upgrade", "1.0.0-rc.10", "v1.0.0-rc.10", false, true}, {"tag mismatch", "1.0.0-rc.11", "v1.0.0-rc.10", true, false}, {"downgrade", "1.0.0-rc.8", "v1.0.0-rc.8", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			u := New(cfg, t.TempDir(), "1.0.0-rc.9")
			m := Manifest{SchemaVersion: 1, Version: tc.version, Channel: "rc", MinConfigSchema: 1, Assets: []Asset{{OS: runtime.GOOS, Arch: runtime.GOARCH, Component: "daemon", Size: 1, SHA256: strings.Repeat("0", 64), URL: "https://github.com/owner/repo/releases/download/v1.0.0-rc.10/daemon"}}}
			body, _ := json.Marshal(m)
			signature := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body)))
			listing, _ := json.Marshal([]githubRelease{githubFixture(tc.tag, true)})
			u.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				b := body
				if r.URL.Host == "api.github.com" {
					b = listing
					if r.Header.Get("X-GitHub-Api-Version") == "" {
						t.Fatal("API version missing")
					}
				}
				if strings.HasSuffix(r.URL.Path, ".sig") {
					b = signature
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header), Request: r}, nil
			})}
			result, err := u.Check(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("error %v", err)
			}
			if err == nil && result.Available != tc.available {
				t.Fatalf("available=%v", result.Available)
			}
		})
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
