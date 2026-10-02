package launcher

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control"
	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
	webui "github.com/jarymor-ux/kee-route-manager/internal/web/ui"
)

// The signed subprocess fixture runs the real controller/UI entrypoints. Only
// early candidate failure is injected; preparation, locks, trial and activation
// are production code, using a paused store and inert Xray commands.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "serve" && os.Args[2] == "--config" && os.Getenv("KRM_LAUNCHER_TEST_HELPER") == "1" {
		c, err := config.Load(os.Args[3])
		version := filepath.Base(filepath.Dir(os.Args[0]))
		if err == nil && os.Getenv("KRM_UPDATE_TRIAL") != "" && os.Getenv("KRM_LAUNCHER_FAIL_VERSION") == version {
			os.Exit(42)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer cancel()
		if err == nil {
			if c.Instance.Role == "ui" {
				err = webui.Serve(ctx, c)
			} else {
				err = control.Serve(ctx, c, version)
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type releaseFixture struct {
	c                           config.Config
	configFile, root, seed, bin string
	private                     ed25519.PrivateKey
	payload                     []byte
	server                      *httptest.Server
	data, signature             []byte
	assetRequests               atomic.Int64
	manifestRequests            atomic.Int64
	beforeManifest              func()
	old                         update.StagedRelease
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	root, err := os.MkdirTemp("", "krm-launch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	f := &releaseFixture{root: root, private: private, payload: payload, seed: filepath.Join(root, "seed"), bin: filepath.Join(root, "bin")}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			f.manifestRequests.Add(1)
			if f.beforeManifest != nil {
				f.beforeManifest()
			}
			_, _ = w.Write(f.data)
		case "/signature":
			_, _ = w.Write(f.signature)
		case "/daemon", "/ui", "/ctl":
			f.assetRequests.Add(1)
			_, _ = w.Write(f.payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	// Updater deliberately uses the default transport. This test's trust change
	// is process-local, never parallel, and still verifies the fixture CA.
	previous := http.DefaultTransport
	http.DefaultTransport = f.server.Client().Transport.(*http.Transport).Clone()
	t.Cleanup(func() {
		http.DefaultTransport.(*http.Transport).CloseIdleConnections()
		http.DefaultTransport = previous
	})
	t.Setenv("KRM_LAUNCHER_TEST_HELPER", "1")
	c := config.Default()
	c.Platform.Kind, c.Platform.Linux.FirewallMode = "linux-systemd", "existing"
	c.Platform.XrayStatusCommand, c.Platform.XrayRestartCommand = []string{"/bin/true"}, []string{"/bin/false"}
	c.Xray.Binary = "/bin/false"
	c.Xray.ConfigDir, c.Xray.ManagedDir = filepath.Join(root, "xray"), filepath.Join(root, "xray")
	c.Paths.StateDir, c.Paths.CacheDir, c.Paths.RunDir, c.Paths.LogFile = filepath.Join(root, "state"), filepath.Join(root, "cache"), filepath.Join(root, "run"), filepath.Join(root, "daemon.log")
	c.API.UnixSocket, c.API.Listen = filepath.Join(c.Paths.RunDir, "core.sock"), unusedAddress(t)
	c.API.TLS.CertFile, c.API.TLS.KeyFile = filepath.Join(root, "api.crt"), filepath.Join(root, "api.key")
	c.Web.Enabled, c.Web.CredentialsFile = false, filepath.Join(root, "credentials.json")
	c.Subscriptions.Sources = []config.Source{{ID: "disabled", Name: "fixture", Enabled: false}}
	c.Targets = []config.Target{{ID: "score", Name: "score", Role: "score", URL: "https://127.0.0.1/", Weight: 1, Policy: "2xx3xx", MaxResponseBytes: 1024}, {ID: "health", Name: "health", Role: "health", URL: "https://127.0.0.1/", Weight: 1, Policy: "2xx3xx", MaxResponseBytes: 1024}}
	c.Update.Enabled, c.Update.PublicKey = true, base64.RawStdEncoding.EncodeToString(public)
	c.Update.InstallDir, c.Update.LauncherSocket = filepath.Join(root, "install"), filepath.Join(c.Paths.RunDir, "launcher.sock")
	c.Update.ManifestURL, c.Update.SignatureURL = f.server.URL+"/manifest", f.server.URL+"/signature"
	c.Update.HealthGracePeriod = config.Dur(time.Second)
	f.c, f.configFile = c, filepath.Join(root, "config.yaml")
	for _, p := range []string{f.seed, f.bin, c.Xray.ConfigDir, c.Paths.RunDir} {
		if err = os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.New(c.Paths.StateDir, c.Paths.CacheDir, model.NewState("1.0.0", c.Xray.SlotTagPrefix, c.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Update(func(s *model.State) error { s.AutomaticRoutingPaused = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(c.Paths.CacheDir, "nodes.json"), []byte("[]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = auth.CreateCredentials(c.Web.CredentialsFile, "admin", "launcher-test-password"); err != nil {
		t.Fatal(err)
	}
	if err = web.EnsureTLS(c.API.TLS, c.API.Listen); err != nil {
		t.Fatal(err)
	}
	writeFixtureConfig(t, f.configFile, c)
	if _, err = config.Load(f.configFile); err != nil {
		t.Fatal(err)
	}
	f.sign(t, "1.0.0")
	for _, name := range []string{"daemon", "ui", "ctl"} {
		if err = os.WriteFile(filepath.Join(f.seed, name), payload, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.writeSeed(t)
	f.old, err = update.ImportRelease(c.Update.InstallDir, f.seed, c.Update.PublicKey, c.Update.Channel)
	if err != nil {
		t.Fatal(err)
	}
	f.sign(t, "1.1.0")
	return f
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	_ = l.Close()
	return address
}

func (f *releaseFixture) sign(t *testing.T, version string) {
	t.Helper()
	hash := sha256.Sum256(f.payload)
	m := update.Manifest{SchemaVersion: 1, UpdateProtocol: 1, Version: version, Channel: "rc", MinConfigSchema: 1}
	for _, component := range []string{"daemon", "ui", "ctl"} {
		m.Assets = append(m.Assets, update.Asset{Name: component, Component: component, OS: runtime.GOOS, Arch: runtime.GOARCH, URL: f.server.URL + "/" + component, Size: int64(len(f.payload)), SHA256: hex.EncodeToString(hash[:])})
	}
	var err error
	f.data, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	f.signature = ed25519.Sign(f.private, f.data)
}

func (f *releaseFixture) writeSeed(t *testing.T) {
	t.Helper()
	for name, b := range map[string][]byte{"manifest-rc.json": f.data, "manifest-rc.json.sig": f.signature} {
		if err := os.WriteFile(filepath.Join(f.seed, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFixtureConfig(t *testing.T, path string, c config.Config) {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err = json.Unmarshal(b, &value); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	var emit func(any, int)
	emit = func(v any, indent int) {
		pad := strings.Repeat(" ", indent)
		line := func(prefix string, item any) {
			switch nested := item.(type) {
			case map[string]any:
				if len(nested) > 0 {
					out.WriteString(pad + prefix + "\n")
					emit(item, indent+2)
					return
				}
			case []any:
				if len(nested) > 0 {
					out.WriteString(pad + prefix + "\n")
					emit(item, indent+2)
					return
				}
			}
			b, _ := json.Marshal(item)
			out.WriteString(pad + prefix + " " + string(b) + "\n")
		}
		switch v := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				line(k+":", v[k])
			}
		case []any:
			for _, item := range v {
				line("-", item)
			}
		}
	}
	emit(value, 0)
	if err = os.WriteFile(path, []byte(out.String()), 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *releaseFixture) supervisor(t *testing.T) *supervisor {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &supervisor{c: f.c, configFile: f.configFile, ctx: ctx, output: io.Discard, rec: record{Schema: 1, Active: f.old.Version, ActiveDigest: f.old.ManifestSHA256, Phase: "committed"}, status: Status{Enabled: true, Launcher: true, Phase: "idle"}}
	if err := s.persist(s.rec); err != nil {
		t.Fatal(err)
	}
	if err := setCurrent(f.c.Update.InstallDir, s.rec.Active); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		s.wg.Wait()
		if err := s.stop(); err != nil {
			t.Error(err)
		}
	})
	if err := s.start(s.rec.Active, s.rec.ActiveDigest, ""); err != nil {
		t.Fatal(err)
	}
	waitDaemonVersion(t, f.c, "1.0.0")
	return s
}

func waitDaemonVersion(t *testing.T, c config.Config, version string) {
	t.Helper()
	cl := client.New(c.API.UnixSocket)
	defer cl.Close()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		b, err := cl.Do(ctx, "GET", "/healthz", nil)
		cancel()
		var h readiness
		if err == nil && json.Unmarshal(b, &h) == nil && h.Version == version && (h.Status == "ok" || h.Status == "safe_degraded") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("controller version %s never became active", version)
}
