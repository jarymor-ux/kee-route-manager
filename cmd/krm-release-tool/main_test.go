package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/update"
)

func releaseFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func readReleaseFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestManifestDescribesAndSignsExactArtifacts(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.Mkdir(dist, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"kee-route-managerd-linux-amd64":         "daemon payload",
		"kee-route-manager-ui-linux-arm64":       "ui payload",
		"kee-route-managerctl-linux-armv7":       "ctl payload",
		"krm-release-tool-linux-mipsle":          "tool payload",
		"kee-route-manager-launcher-linux-amd64": "launcher payload",
		"release-files.tar.gz":                   "archive payload",
	}
	for name, data := range files {
		releaseFile(t, filepath.Join(dist, name), []byte(data), 0600)
	}
	for _, name := range []string{"manifest-rc.json", "SHA256SUMS"} {
		releaseFile(t, filepath.Join(dist, name), []byte("stale metadata"), 0600)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "fixture-private")
	releaseFile(t, key, []byte(base64.RawStdEncoding.EncodeToString(private)), 0600)
	out := filepath.Join(dir, "manifest.json")
	base := "https://example.invalid/releases/v1.0.0-rc.2"
	if err := manifest([]string{"--version", "1.0.0-rc.2", "--base-url", base, "--dist", dist, "--out", out, "--private", key}); err != nil {
		t.Fatal(err)
	}
	body := readReleaseFile(t, out)
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(readReleaseFile(t, out+".sig"))))
	if err != nil || !ed25519.Verify(pub, body, sig) {
		t.Fatalf("manifest signature invalid: %v", err)
	}
	var got update.Manifest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	var protocol struct {
		UpdateProtocol int `json:"update_protocol"`
	}
	if err := json.Unmarshal(body, &protocol); err != nil || protocol.UpdateProtocol != 1 {
		t.Fatalf("missing signed update protocol: %d %v", protocol.UpdateProtocol, err)
	}
	if got.Version != "1.0.0-rc.2" || got.Channel != "rc" || got.SchemaVersion != 1 || got.MinConfigSchema != 1 || got.PublishedAt.IsZero() || len(got.Assets) != len(files) {
		t.Fatalf("manifest metadata: %+v", got)
	}
	wantKinds := map[string]string{"kee-route-managerd-linux-amd64": "daemon/amd64", "kee-route-manager-ui-linux-arm64": "ui/arm64", "kee-route-managerctl-linux-armv7": "ctl/arm", "krm-release-tool-linux-mipsle": "release-tool/mipsle", "kee-route-manager-launcher-linux-amd64": "launcher/amd64", "release-files.tar.gz": "file/"}
	for i, asset := range got.Assets {
		data, ok := files[asset.Name]
		digest := sha256.Sum256([]byte(data))
		if !ok || asset.Size != int64(len(data)) || asset.SHA256 != hex.EncodeToString(digest[:]) || asset.URL != base+"/"+asset.Name || asset.Component+"/"+asset.Arch != wantKinds[asset.Name] {
			t.Errorf("incorrect artifact: %+v", asset)
		}
		if asset.Arch == "arm" && asset.GOARM != "7" || asset.Arch != "" && asset.OS != "linux" {
			t.Errorf("incorrect platform: %+v", asset)
		}
		if i > 0 && got.Assets[i-1].Name >= asset.Name {
			t.Error("assets not deterministically sorted")
		}
	}
	body[0] ^= 1
	if ed25519.Verify(pub, body, sig) {
		t.Fatal("altered manifest signature accepted")
	}
}

func TestManifestRejectsAmbiguousAssetsAndURLs(t *testing.T) {
	for _, tc := range []struct{ name, base, asset, kind string }{
		{"credentials", "https://user:password@example.invalid/v1", "file", "regular"},
		{"query", "https://example.invalid/v1?x=y", "file", "regular"},
		{"empty query", "https://example.invalid/v1?", "file", "regular"},
		{"fragment", "https://example.invalid/v1#x", "file", "regular"},
		{"empty fragment", "https://example.invalid/v1#", "file", "regular"},
		{"http", "http://example.invalid/v1", "file", "regular"},
		{"asset fragment", "https://example.invalid/v1", "artifact#other", "regular"},
		{"asset query", "https://example.invalid/v1", "artifact?other", "regular"},
		{"asset escape", "https://example.invalid/v1", "artifact%2Fother", "regular"},
		{"space", "https://example.invalid/v1", "artifact other", "regular"},
		{"architecture", "https://example.invalid/v1", "kee-route-managerd-linux-unknown", "regular"},
		{"launcher architecture", "https://example.invalid/v1", "kee-route-manager-launcher-linux-unknown", "regular"},
		{"symlink", "https://example.invalid/v1", "artifact", "symlink"},
		{"empty", "https://example.invalid/v1", "", "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist, out := t.TempDir(), filepath.Join(t.TempDir(), "manifest.json")
			switch tc.kind {
			case "regular":
				releaseFile(t, filepath.Join(dist, tc.asset), []byte("fixture"), 0600)
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				releaseFile(t, target, []byte("fixture"), 0600)
				if err := os.Symlink(target, filepath.Join(dist, tc.asset)); err != nil {
					t.Fatal(err)
				}
			}
			if err := manifest([]string{"--version", "1.0.0-rc.2", "--base-url", tc.base, "--dist", dist, "--out", out}); err == nil {
				t.Fatal("unsafe release input accepted")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("rejected input created a manifest: %v", err)
			}
		})
	}
}

func TestKeygenCreatesPrivateNonOverwritingPair(t *testing.T) {
	dir := t.TempDir()
	pub, private := filepath.Join(dir, "public"), filepath.Join(dir, "private")
	args := []string{"--public", pub, "--private", private}
	if err := keygen(args); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(private)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private key permissions: %v %v", info, err)
	}
	public := readReleaseFile(t, pub)
	if err := keygen(args); err == nil {
		t.Fatal("key rotation silently overwrote existing identity")
	}
	if string(readReleaseFile(t, pub)) != string(public) {
		t.Fatal("public identity changed")
	}
	input, out := filepath.Join(dir, "payload"), filepath.Join(dir, "signature")
	releaseFile(t, input, []byte("release fixture"), 0600)
	if err := sign([]string{"--private", private, "--input", input, "--out", out}); err != nil {
		t.Fatal(err)
	}
	pk, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(public)))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(readReleaseFile(t, out))))
	if err != nil || !ed25519.Verify(pk, []byte("release fixture"), sig) {
		t.Fatalf("key pair/signature mismatch: %v", err)
	}
}

func TestSignRejectsMissingArgumentsAndInvalidKeys(t *testing.T) {
	if err := sign(nil); err == nil {
		t.Fatal("missing arguments accepted")
	}
	dir := t.TempDir()
	key, input, out := filepath.Join(dir, "key"), filepath.Join(dir, "input"), filepath.Join(dir, "sig")
	releaseFile(t, key, []byte("invalid key"), 0600)
	releaseFile(t, input, []byte("payload"), 0600)
	if err := signFiles(key, input, out); err == nil {
		t.Fatal("malformed key accepted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("signature created on failure")
	}
}
