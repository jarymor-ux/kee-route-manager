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
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type stagingFixture struct {
	u               *Updater
	manifest        Manifest
	data, signature []byte
	private         ed25519.PrivateKey
	public          string
	payloads        map[string][]byte
	requests        atomic.Int64
	assetHandler    func(http.ResponseWriter, *http.Request, string)
}

func newStagingFixture(t *testing.T) *stagingFixture {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &stagingFixture{private: private, public: base64.RawStdEncoding.EncodeToString(pub), payloads: map[string][]byte{}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			_, _ = w.Write(f.data)
		case "/sig":
			_, _ = w.Write(f.signature)
		default:
			component := strings.TrimPrefix(r.URL.Path, "/")
			f.requests.Add(1)
			if f.assetHandler != nil {
				f.assetHandler(w, r, component)
				return
			}
			_, _ = w.Write(f.payloads[component])
		}
	}))
	t.Cleanup(server.Close)
	p, err := nativePlatform()
	if err != nil {
		t.Fatal(err)
	}
	f.manifest = Manifest{SchemaVersion: 1, UpdateProtocol: 1, Version: "1.2.0-rc.1", Channel: "rc", MinConfigSchema: 1}
	for _, component := range componentOrder {
		payload := []byte("signed raw binary fixture " + component)
		f.payloads[component] = payload
		f.manifest.Assets = append(f.manifest.Assets, Asset{Name: component + "-" + p.os + "-" + p.arch, Component: component, OS: p.os, Arch: p.arch, GOARM: p.arm, URL: server.URL + "/" + component, SHA256: digest(payload), Size: int64(len(payload))})
	}
	f.sign(t)
	c := config.Default().Update
	c.Enabled, c.PublicKey, c.ManifestURL, c.SignatureURL = true, f.public, server.URL+"/manifest", server.URL+"/sig"
	c.InstallDir = filepath.Join(t.TempDir(), "updates")
	f.u = New(c, t.TempDir(), "1.1.0-rc.1")
	f.u.client.Transport = server.Client().Transport
	return f
}

func (f *stagingFixture) sign(t *testing.T) {
	t.Helper()
	var err error
	f.data, err = json.Marshal(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	f.signature = ed25519.Sign(f.private, f.data)
}

func assertNoPublishedStage(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "releases"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed staging left a release or temporary directory: %v", entries)
	}
}

func TestStagePublishesCompleteVerifiedBundleWithoutActivating(t *testing.T) {
	f := newStagingFixture(t)
	if err := os.MkdirAll(f.u.cfg.InstallDir, 0700); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(f.u.cfg.InstallDir, "current")
	if err := os.WriteFile(current, []byte("previous-release"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := f.u.Check(context.Background())
	if err != nil || !result.StageSupported || len(result.Assets) != 3 {
		t.Fatalf("complete release not exposed: %+v %v", result, err)
	}
	staged, err := f.u.Stage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if staged.Version != f.manifest.Version || staged.ManifestSHA256 != digest(f.data) || len(staged.Assets) != 3 {
		t.Fatalf("incorrect receipt: %+v", staged)
	}
	for _, component := range componentOrder {
		path := filepath.Join(staged.Directory, componentFiles[component])
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, f.payloads[component]) {
			t.Fatalf("component %s incomplete: %v", component, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("component mode: %v %v", info, err)
		}
	}
	verified, err := VerifyRelease(f.u.cfg.InstallDir, staged.Version, f.public, "rc")
	if err != nil || verified.ManifestSHA256 != staged.ManifestSHA256 || verified.Directory != staged.Directory {
		t.Fatalf("launcher cannot verify stage: %+v %v", verified, err)
	}
	before := f.requests.Load()
	if _, err = f.u.Stage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.requests.Load() != before {
		t.Fatal("immutable verified bundle downloaded again")
	}
	data, _ := os.ReadFile(current)
	if string(data) != "previous-release" {
		t.Fatal("staging activated a release")
	}
	if _, err = os.Stat(f.u.pendingPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging created legacy apply intent")
	}
}

func TestStageRejectsUntrustedOrIncompleteBundles(t *testing.T) {
	for _, name := range []string{"signature", "checksum", "truncated", "oversized", "http-error", "missing-component", "duplicate-component", "wrong-architecture", "wrong-os", "unsafe-name", "unsafe-version", "legacy-protocol", "future-protocol", "incompatible-schema"} {
		t.Run(name, func(t *testing.T) {
			f := newStagingFixture(t)
			switch name {
			case "checksum":
				f.payloads["ui"][0] ^= 1
			case "truncated":
				f.payloads["ui"] = f.payloads["ui"][:len(f.payloads["ui"])-1]
			case "oversized":
				f.payloads["ui"] = append(f.payloads["ui"], 0)
			case "http-error":
				f.assetHandler = func(w http.ResponseWriter, _ *http.Request, _ string) {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
				}
			case "missing-component":
				f.manifest.Assets = f.manifest.Assets[:2]
			case "duplicate-component":
				f.manifest.Assets = append(f.manifest.Assets, f.manifest.Assets[0])
			case "wrong-architecture":
				f.manifest.Assets[1].Arch = "foreign"
			case "wrong-os":
				f.manifest.Assets[1].OS = "foreign"
			case "unsafe-name":
				f.manifest.Assets[1].Name = "../outside"
			case "unsafe-version":
				f.manifest.Version = "../outside"
			case "legacy-protocol":
				f.manifest.UpdateProtocol = 0
			case "future-protocol":
				f.manifest.UpdateProtocol = 2
			case "incompatible-schema":
				f.manifest.MinConfigSchema = 2
			}
			f.sign(t)
			if name == "signature" {
				f.signature[0] ^= 1
			}
			if _, err := f.u.Stage(context.Background()); err == nil {
				t.Fatal("invalid release staged")
			}
			assertNoPublishedStage(t, f.u.cfg.InstallDir)
		})
	}
}

func TestStageChecksStreamLengthWithoutContentLength(t *testing.T) {
	for _, extra := range []int{-1, 1} {
		t.Run(map[int]string{-1: "truncated", 1: "oversized"}[extra], func(t *testing.T) {
			f := newStagingFixture(t)
			f.assetHandler = func(w http.ResponseWriter, _ *http.Request, component string) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				data := append([]byte(nil), f.payloads[component]...)
				if component == "ui" {
					if extra < 0 {
						data = data[:len(data)-1]
					} else {
						data = append(data, 0)
					}
				}
				_, _ = w.Write(data)
			}
			if _, err := f.u.Stage(context.Background()); err == nil {
				t.Fatal("unsigned stream length accepted")
			}
			assertNoPublishedStage(t, f.u.cfg.InstallDir)
		})
	}
}

func TestStageCancellationCleansPartialFilesAndSerializesWriters(t *testing.T) {
	f := newStagingFixture(t)
	entered := make(chan struct{})
	releaseHandler := make(chan struct{})
	t.Cleanup(func() { close(releaseHandler) })
	partial := f.payloads["ui"][:len(f.payloads["ui"])/2]
	f.assetHandler = func(w http.ResponseWriter, _ *http.Request, component string) {
		if component == "ui" {
			w.Header().Set("Content-Length", strconv.Itoa(len(f.payloads[component])))
			_, _ = w.Write(partial)
			w.(http.Flusher).Flush()
			close(entered)
			// Keep the response incomplete until Stage has returned. Returning
			// on request cancellation races a server-generated EOF against the
			// client's cancellation error and tests whichever arrives first.
			<-releaseHandler
			return
		}
		_, _ = w.Write(f.payloads[component])
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, 1)
	go func() { _, err := f.u.Stage(ctx); completed <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("stage did not start")
	}
	entries, err := os.ReadDir(filepath.Join(f.u.cfg.InstallDir, "releases"))
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ".stage-") {
		t.Fatalf("missing temporary stage: %v %v", entries, err)
	}
	partialPath := filepath.Join(f.u.cfg.InstallDir, "releases", entries[0].Name(), componentFiles["ui"])
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(partialPath)
		if err == nil && bytes.Equal(data, partial) {
			break
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("stage did not write the partial asset: %q %v", data, err)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := f.u.Stage(context.Background()); !errors.Is(err, ErrStageBusy) {
		t.Fatalf("concurrent writer accepted: %v", err)
	}
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled stage hung")
	}
	assertNoPublishedStage(t, f.u.cfg.InstallDir)
}

func TestStageCancellationRacingIncompleteResponseNeverPublishes(t *testing.T) {
	f := newStagingFixture(t)
	f.payloads["ui"] = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := f.u.client.Transport
	f.u.client.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(request)
		if err == nil && request.URL.Path == "/ui" {
			// A response already received by the transport may be delivered
			// concurrently with cancellation. Its validation error need not be
			// context.Canceled, but it must never publish an incomplete bundle.
			cancel()
		}
		return response, err
	})
	if _, err := f.u.Stage(ctx); err == nil {
		t.Fatal("incomplete response won cancellation and published a release")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("fixture did not cancel at the response boundary")
	}
	assertNoPublishedStage(t, f.u.cfg.InstallDir)
}

func TestStageVersionPolicyAndImmutableIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, current      string
		downgrade, allowed bool
	}{
		{"same", "1.2.0-rc.1", false, false},
		{"older", "1.3.0-rc.1", false, false},
		{"explicit-downgrade", "1.3.0-rc.1", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStagingFixture(t)
			f.u.current = tc.current
			f.u.cfg.AllowDowngrade = tc.downgrade
			_, err := f.u.Stage(context.Background())
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, ErrNoUpdate) {
					t.Fatalf("version policy bypassed: %v", err)
				}
				assertNoPublishedStage(t, f.u.cfg.InstallDir)
			}
		})
	}
	f := newStagingFixture(t)
	original, err := f.u.Stage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.manifest.PublishedAt = time.Now().UTC()
	f.sign(t)
	if _, err = f.u.Stage(context.Background()); err == nil {
		t.Fatal("published version overwritten with new signed identity")
	}
	current, err := VerifyRelease(f.u.cfg.InstallDir, original.Version, f.public, "rc")
	if err != nil || current.ManifestSHA256 != original.ManifestSHA256 {
		t.Fatalf("immutable release changed: %+v %v", current, err)
	}
}

func TestVerifyReleaseRejectsTamperingAndUnsafeFilesystemObjects(t *testing.T) {
	for _, name := range []string{"binary", "manifest", "signature", "binary-symlink", "manifest-symlink", "directory-symlink", "binary-hardlink", "binary-permissions", "manifest-permissions", "directory-permissions", "wrong-version", "wrong-owner"} {
		t.Run(name, func(t *testing.T) {
			if name == "wrong-owner" && os.Geteuid() != 0 {
				t.Skip("requires root to change ownership; exercised in Linux container")
			}
			f := newStagingFixture(t)
			staged, err := f.u.Stage(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(staged.Directory, "kee-route-managerd")
			metadata := filepath.Join(staged.Directory, "manifest.json")
			version := staged.Version
			switch name {
			case "binary":
				err = os.WriteFile(binary, []byte("tampered"), 0700)
			case "manifest":
				err = os.WriteFile(metadata, []byte("tampered"), 0600)
			case "signature":
				err = os.WriteFile(filepath.Join(staged.Directory, "manifest.sig"), make([]byte, 64), 0600)
			case "binary-symlink", "manifest-symlink":
				path := binary
				if name == "manifest-symlink" {
					path = metadata
				}
				outside := filepath.Join(t.TempDir(), "outside")
				err = os.Rename(path, outside)
				if err == nil {
					err = os.Symlink(outside, path)
				}
			case "directory-symlink":
				outside := filepath.Join(t.TempDir(), "outside")
				err = os.Rename(staged.Directory, outside)
				if err == nil {
					err = os.Symlink(outside, staged.Directory)
				}
			case "binary-hardlink":
				err = os.Link(binary, filepath.Join(t.TempDir(), "alias"))
			case "binary-permissions":
				err = os.Chmod(binary, 0755)
			case "manifest-permissions":
				err = os.Chmod(metadata, 0644)
			case "directory-permissions":
				err = os.Chmod(staged.Directory, 0755)
			case "wrong-owner":
				err = os.Chown(binary, 65534, 65534)
			case "wrong-version":
				version = "9.0.0-rc.1"
				err = os.Rename(staged.Directory, filepath.Join(filepath.Dir(staged.Directory), version))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = VerifyRelease(f.u.cfg.InstallDir, version, f.public, "rc"); err == nil {
				t.Fatal("tampered/private-path bypass accepted")
			}
		})
	}
}

func TestStageRejectsUnsafeInstallPaths(t *testing.T) {
	for _, name := range []string{"relative", "root-symlink", "root-permissions", "releases-symlink", "lock-symlink", "writable-parent"} {
		t.Run(name, func(t *testing.T) {
			f := newStagingFixture(t)
			outside := t.TempDir()
			if name == "relative" {
				f.u.cfg.InstallDir = "relative"
			} else if name == "writable-parent" {
				parent := t.TempDir()
				if err := os.Chmod(parent, 0777); err != nil {
					t.Fatal(err)
				}
				f.u.cfg.InstallDir = filepath.Join(parent, "updates")
			} else {
				if name == "root-symlink" {
					if err := os.Symlink(outside, f.u.cfg.InstallDir); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.MkdirAll(f.u.cfg.InstallDir, 0700); err != nil {
						t.Fatal(err)
					}
					switch name {
					case "root-permissions":
						if err := os.Chmod(f.u.cfg.InstallDir, 0755); err != nil {
							t.Fatal(err)
						}
					case "releases-symlink":
						if err := os.Symlink(outside, filepath.Join(f.u.cfg.InstallDir, "releases")); err != nil {
							t.Fatal(err)
						}
					case "lock-symlink":
						if err := os.Symlink(filepath.Join(outside, "victim"), filepath.Join(f.u.cfg.InstallDir, "stage.lock")); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if _, err := f.u.Stage(context.Background()); err == nil {
				t.Fatal("unsafe install path accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unsafe stage changed outside directory: %v %v", entries, err)
			}
		})
	}
}

func TestImportReleaseUsesSameVerificationWithoutExecutingAssets(t *testing.T) {
	f := newStagingFixture(t)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "manifest-rc.json"), f.data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest-rc.json.sig"), []byte(base64.RawStdEncoding.EncodeToString(f.signature)), 0644); err != nil {
		t.Fatal(err)
	}
	for _, asset := range f.manifest.Assets {
		if err := os.WriteFile(filepath.Join(source, asset.Name), f.payloads[asset.Component], 0644); err != nil {
			t.Fatal(err)
		}
	}
	staged, err := ImportRelease(f.u.cfg.InstallDir, source, f.public, "rc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyRelease(f.u.cfg.InstallDir, staged.Version, f.public, "rc"); err != nil {
		t.Fatal(err)
	}
	if f.requests.Load() != 0 {
		t.Fatal("offline import contacted network")
	}
	asset := f.manifest.Assets[1]
	if err = os.WriteFile(filepath.Join(source, asset.Name), []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "other-updates")
	if _, err = ImportRelease(root, source, f.public, "rc"); err == nil {
		t.Fatal("offline import skipped signed hash validation")
	}
	assertNoPublishedStage(t, root)
}

func TestBundleArchitectureAndImmutableGitHubBinding(t *testing.T) {
	f := newStagingFixture(t)
	manifest := f.manifest
	for i := range manifest.Assets {
		manifest.Assets[i].OS = "linux"
		manifest.Assets[i].Arch = "arm"
		manifest.Assets[i].GOARM = "7"
	}
	if _, err := bundleAssetsFor(manifest, releasePlatform{"linux", "arm", "7"}); err != nil {
		t.Fatal(err)
	}
	if _, err := bundleAssetsFor(manifest, releasePlatform{"linux", "arm", "5"}); err == nil {
		t.Fatal("ARM7 bundle selected for ARM5")
	}
	for i := range manifest.Assets {
		manifest.Assets[i].GOARM = ""
	}
	if _, err := bundleAssetsFor(manifest, releasePlatform{"linux", "arm", "7"}); err == nil {
		t.Fatal("unspecified ARM variant accepted")
	}
	for _, name := range []string{"different-version", "different-repo", "different-name", "mutable-latest", "different-origin"} {
		t.Run(name, func(t *testing.T) {
			f := newStagingFixture(t)
			for i := range f.manifest.Assets {
				a := &f.manifest.Assets[i]
				a.URL = "https://github.com/owner/repo/releases/download/v" + f.manifest.Version + "/" + a.Name
			}
			switch name {
			case "different-version":
				f.manifest.Assets[1].URL = strings.Replace(f.manifest.Assets[1].URL, f.manifest.Version, "9.0.0-rc.1", 1)
			case "different-repo":
				f.manifest.Assets[1].URL = strings.Replace(f.manifest.Assets[1].URL, "owner/repo", "another/repo", 1)
			case "different-name":
				f.manifest.Assets[1].URL += "-other"
			case "mutable-latest":
				f.manifest.Assets[1].URL = "https://github.com/owner/repo/releases/latest/download/" + f.manifest.Assets[1].Name
			case "different-origin":
				f.manifest.Assets[1].URL = "https://other.example/" + f.manifest.Assets[1].Name
			}
			if _, err := bundleAssets(f.manifest); err == nil {
				t.Fatal("mixed or mutable GitHub bundle accepted")
			}
		})
	}
}

func TestVerifyStreamNeverConsumesMoreThanSignedSizePlusOne(t *testing.T) {
	reader := &countingAssetReader{}
	_, err := verifyStream(io.Discard, reader, Asset{Size: 64, SHA256: strings.Repeat("0", 64)})
	if err == nil || reader.bytes != 65 {
		t.Fatalf("unbounded stream read: bytes=%d error=%v", reader.bytes, err)
	}
}

type countingAssetReader struct{ bytes int }

func (r *countingAssetReader) Read(p []byte) (int, error) {
	r.bytes += len(p)
	clear(p)
	return len(p), nil
}

func TestLegacyCheckDoesNotAdvertiseStaging(t *testing.T) {
	f := newStagingFixture(t)
	f.manifest.UpdateProtocol = 0
	f.manifest.Assets = f.manifest.Assets[:1]
	f.sign(t)
	result, err := f.u.Check(context.Background())
	if err != nil || !result.Available || result.StageSupported || result.Asset.Arch != runtime.GOARCH {
		t.Fatalf("legacy discovery contract changed: %+v %v", result, err)
	}
}
