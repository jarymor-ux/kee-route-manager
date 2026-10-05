package cli

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func captureOutput(t *testing.T, run func() error) (string, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = old }()
	runErr := run()
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), runErr
}

func configFile(t *testing.T, socket string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `instance:
  role: controller
platform:
  kind: linux-systemd
paths:
  state_dir: untouched-state
  cache_dir: untouched-cache
  log_file: untouched.log
  run_dir: ` + filepath.Dir(socket) + `
api:
  unix_socket: ` + socket + `
web:
  credentials_file: credentials.json
subscriptions:
  sources:
    - id: provider
      enabled: true
      url: https://subscription.example.invalid/SYNTHETIC_URL_SECRET
      headers:
        Authorization: SYNTHETIC_HEADER_SECRET
targets:
  - id: score
    url: https://score.example.invalid
    role: score
    weight: 1
    policy: 2xx3xx
    max_response_bytes: 64KiB
  - id: health
    url: https://health.example.invalid
    role: health
    weight: 1
    policy: 2xx3xx
    max_response_bytes: 64KiB
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func unixServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	// A short directory avoids the smaller Unix-socket pathname limit on macOS.
	dir, err := os.MkdirTemp("", "krm-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "control.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		l.Close()
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return path
}

func TestDaemonCommandsUseExistingSocket(t *testing.T) {
	for _, tc := range []struct {
		command, path, method string
		args                  []string
		body                  string
	}{
		{"status", "/api/v1/status", "GET", nil, ""},
		{"doctor", "/api/v1/status", "GET", nil, ""},
		{"ready", "/healthz", "GET", nil, ""},
		{"benchmark", "/api/v1/actions/benchmark", "POST", nil, ""},
		{"switch", "/api/v1/actions/switch", "POST", []string{"--slot", "0"}, `{"index":0}`},
		{"direct", "/api/v1/actions/direct", "POST", nil, ""},
		{"restore-xray", "/api/v1/actions/restore-xray", "POST", nil, ""},
		{"update-check", "/api/v1/update/check", "GET", nil, ""},
		{"update-status", "/api/v1/update/status", "GET", nil, ""},
		{"update-apply", "/api/v1/update/apply", "POST", nil, `{}`},
		{"update-apply", "/api/v1/update/apply", "POST", []string{"--target-version", "1.2.3-rc.1"}, `{"version":"1.2.3-rc.1"}`},
	} {
		t.Run(tc.command, func(t *testing.T) {
			requests := make(chan struct{}, 1)
			socket := unixServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil || r.Method != tc.method || r.URL.Path != tc.path || string(b) != tc.body {
					t.Errorf("request=%s %s body=%s err=%v", r.Method, r.URL.Path, b, err)
				}
				requests <- struct{}{}
				_, _ = w.Write([]byte(`{"ok":true,"operation_id":"op-existing-daemon"}`))
			}))
			args := append([]string{tc.command, "--socket", socket, "--config", "/nonexistent/config.yaml"}, tc.args...)
			out, err := captureOutput(t, func() error { return Run(context.Background(), args, "test", "commit", "build") })
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err = json.Unmarshal([]byte(out), &decoded); err != nil || decoded["operation_id"] != "op-existing-daemon" {
				t.Fatalf("output=%q err=%v", out, err)
			}
			select {
			case <-requests:
			default:
				t.Fatal("did not use existing daemon")
			}
		})
	}
}

func TestConfigSocketAndOfflineValidation(t *testing.T) {
	socket := unixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"version":"existing"}`)) }))
	path := configFile(t, socket)
	for _, command := range []string{"validate", "status"} {
		out, err := captureOutput(t, func() error {
			return Run(context.Background(), []string{command, "--config", path}, "test", "commit", "build")
		})
		if err != nil {
			t.Fatal(err)
		}
		if command == "validate" {
			if !strings.Contains(out, "Configuration is valid") || strings.Contains(out, "SYNTHETIC_URL_SECRET") || strings.Contains(out, "SYNTHETIC_HEADER_SECRET") {
				t.Fatalf("unsafe validation output: %s", out)
			}
		} else if !strings.Contains(out, "existing") {
			t.Fatalf("daemon response missing: %s", out)
		}
	}
	for _, name := range []string{"untouched-state", "untouched-cache", "untouched.log"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), name)); !os.IsNotExist(err) {
			t.Fatalf("CLI touched runtime %s: %v", name, err)
		}
	}
}

func TestCommandErrorsAndCancellation(t *testing.T) {
	for _, args := range [][]string{
		nil, {"unknown", "--socket", "/missing"}, {"switch", "--socket", "/missing"}, {"switch", "--slot", "-1", "--socket", "/missing"},
		{"status", "extra"}, {"status", "--bad"}, {"update-apply", "--socket", "/missing"}, {"status", "--config", "/missing"},
		{"update-status", "--target-version", "1.2.3", "--socket", "/missing"},
		{"validate", "extra"}, {"validate", "--bad"}, {"passwd"}, {"passwd", "--password-stdin", "extra"},
	} {
		if err := Run(context.Background(), args, "test", "commit", "build"); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
	for _, response := range []struct {
		code int
		body string
	}{{409, `{"error":"operation unavailable"}`}, {200, "invalid JSON"}} {
		socket := unixServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(response.code)
			_, _ = w.Write([]byte(response.body))
		}))
		if err := Run(context.Background(), []string{"benchmark", "--socket", socket}, "", "", ""); err == nil {
			t.Fatalf("accepted failed controller response: %+v", response)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, []string{"status", "--socket", "/missing"}, "", "", ""); err == nil {
		t.Fatal("ignored cancellation")
	}
	for _, command := range []string{"version", "--version", "-version"} {
		out, err := captureOutput(t, func() error {
			return Run(context.Background(), []string{command}, "v-test", "test-commit", "test-build")
		})
		if err != nil || !strings.Contains(out, "kee-route-managerctl v-test (test-commit, test-build,") {
			t.Fatalf("version=%q err=%v", out, err)
		}
	}
}

func TestRouteCandidatesSelectOnlyExplicitRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routing.json")
	body := `{"routing":{"rules":[{"outboundTag":"implicit"},{"inboundTag":["redirect"],"balancerTag":"pool"},{"inboundTag":["redirect"],"outboundTag":"proxy"},{"inboundTag":["tproxy"],"outboundTag":"direct"},{"inboundTag":["redirect"],"outboundTag":"proxy","balancerTag":"pool"}]},"outbounds":[{"settings":{"id":"SYNTHETIC_NODE_SECRET"}}]}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := captureOutput(t, func() error {
		return Run(context.Background(), []string{"route-candidates", "--file", path}, "", "", "")
	})
	if err != nil {
		t.Fatal(err)
	}
	var candidates []struct {
		Index    int      `json:"rule_index"`
		Inbound  []string `json:"inbound_tags"`
		Outbound string   `json:"outbound_tag"`
	}
	if err := json.Unmarshal([]byte(out), &candidates); err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Index != 2 || candidates[0].Outbound != "proxy" || !reflect.DeepEqual(candidates[0].Inbound, []string{"redirect"}) || candidates[1].Index != 3 || candidates[1].Outbound != "direct" || strings.Contains(out, "SYNTHETIC_NODE_SECRET") {
		t.Fatalf("candidates=%s", out)
	}
	for _, invalid := range []string{`{}`, `null`, `{"routing": {"rules": "invalid"}}`, body + ` {}`, `// comment\n` + body, strings.Repeat(" ", 4<<20) + body} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if err := routeCandidates([]string{"--file", path}); err == nil {
			t.Fatalf("accepted invalid routing input of %d bytes", len(invalid))
		}
	}
	for _, args := range [][]string{nil, {"--file", "/missing"}, {"--file", path, "extra"}, {"--unknown"}} {
		if err := routeCandidates(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestPasswordStdinCreatesPrivateCredentialsOnly(t *testing.T) {
	path := configFile(t, filepath.Join(t.TempDir(), "control.sock"))
	password := "cli-test-password-only"
	input, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err = input.WriteString(password + "\r\n"); err != nil {
		t.Fatal(err)
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = input
	defer func() { os.Stdin = old }()
	args := []string{"passwd", "--config", path, "--username", "admin", "--password-stdin"}
	out, err := captureOutput(t, func() error { return Run(context.Background(), args, "", "", "") })
	if err != nil || out != "" {
		t.Fatalf("passwd=%q err=%v", out, err)
	}
	credentials := filepath.Join(filepath.Dir(path), "credentials.json")
	c, err := auth.LoadCredentials(credentials)
	if err != nil || !auth.Verify(c, "admin", password) {
		t.Fatalf("credentials not usable: %v", err)
	}
	info, err := os.Stat(credentials)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("credential permissions: %v %v", info, err)
	}
	before, err := os.ReadFile(credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := input.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := input.WriteString(strings.Repeat("x", 1027)); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), args, "", "", ""); err == nil {
		t.Fatal("oversized password accepted")
	}
	after, err := os.ReadFile(credentials)
	if err != nil || string(after) != string(before) {
		t.Fatal("invalid password overwrote credentials")
	}
}

func TestInitConfigCommandRunsOfflineWithInjectedIO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	input := strings.Join([]string{
		"2", "1", "", "", "", "", "proxy-main",
		"https://sub.example.test/main", "Main", "", "n", "n",
		"https://score.example.test/ping",
		"https://health.example.test/ping", "n", "",
		"n", "n", "y",
	}, "\n") + "\n"
	var out strings.Builder
	if err := runWithIO(context.Background(), []string{"init-config", "--output", path}, "test", "commit", "build", strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config was not created: %v", err)
	}
	if !strings.Contains(out.String(), "Create configuration?") {
		t.Fatalf("wizard output missing: %s", out.String())
	}
}

func TestInitConfigCommandPinsPlatformWithoutPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	input := strings.Join([]string{
		"2", "", "", "", "", "proxy-main",
		"https://sub.example.test/main", "Main", "", "n", "n",
		"https://score.example.test/ping",
		"https://health.example.test/ping", "n", "",
		"n", "n", "y",
	}, "\n") + "\n"
	var out strings.Builder
	if err := runWithIO(context.Background(), []string{"init-config", "--platform", "openwrt", "--output", path}, "test", "commit", "build", strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Platform.Kind != "openwrt" {
		t.Fatalf("platform=%q, want openwrt", cfg.Platform.Kind)
	}
}

func TestInitConfigLocalUIPair(t *testing.T) {
	for _, platform := range []string{"keenetic", "openwrt", "linux-systemd"} {
		t.Run(platform, func(t *testing.T) {
			dir := t.TempDir()
			corePath, uiPath := filepath.Join(dir, "core.yaml"), filepath.Join(dir, "ui.yaml")
			input := strings.Join([]string{"2", "", "", "", "", "proxy-main", "https://sub.example.test/main", "Main", "n", "n", "n", "https://score.example.test/ping", "https://health.example.test/ping", "n", "", "n", "n", "y"}, "\n") + "\n"
			var out strings.Builder
			err := runWithIO(context.Background(), []string{"init-config", "--platform", platform, "--output", corePath, "--ui-output", uiPath, "--tls-dir", filepath.Join(dir, "tls")}, "test", "commit", "build", strings.NewReader(input), &out)
			if err != nil {
				t.Fatal(err)
			}
			core, err := config.Load(corePath)
			if err != nil {
				t.Fatal(err)
			}
			ui, err := config.Load(uiPath)
			if err != nil {
				t.Fatal(err)
			}
			if !core.API.TLS.Enabled || core.API.Listen != "127.0.0.1:9443" || core.Update.AutoApply || core.Benchmark.Speed.Enabled {
				t.Fatal("unsafe controller defaults")
			}
			prefix := ""
			if platform == "keenetic" {
				prefix = "/opt"
			}
			if ui.UIProxy.Upstream != "https://127.0.0.1:9443" || ui.UIProxy.InsecureTLS || ui.UIProxy.UpstreamCAFile != prefix+"/etc/kee-route-manager-ui/controller-ca.crt" {
				t.Fatal("invalid local UI trust")
			}
			for _, path := range []string{corePath, uiPath, filepath.Join(dir, "tls", "tls.key")} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("private file permissions: %s", path)
				}
			}
		})
	}
}

func TestInitConfigLocalUIRefusesInvalidOrExistingOutputs(t *testing.T) {
	for _, scenario := range []string{"missing-platform", "missing-ui", "missing-tls", "overwrite", "core-exists", "ui-exists", "tls-exists", "ui-symlink", "same-output"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			core, ui, tls := filepath.Join(dir, "core"), filepath.Join(dir, "ui"), filepath.Join(dir, "tls")
			args := []string{"init-config", "--platform", "openwrt", "--output", core, "--ui-output", ui, "--tls-dir", tls}
			switch scenario {
			case "missing-platform":
				args[2] = ""
			case "missing-ui":
				args[6] = ""
			case "missing-tls":
				args[8] = ""
			case "overwrite":
				args = append(args, "--overwrite")
			case "core-exists":
				if err := os.WriteFile(core, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ui-exists":
				if err := os.WriteFile(ui, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "tls-exists":
				if err := os.Mkdir(tls, 0700); err != nil {
					t.Fatal(err)
				}
			case "ui-symlink":
				if err := os.Symlink(filepath.Join(dir, "missing"), ui); err != nil {
					t.Fatal(err)
				}
			case "same-output":
				args[6] = core
			}
			var out strings.Builder
			if err := runWithIO(context.Background(), args, "test", "commit", "build", strings.NewReader(""), &out); err == nil {
				t.Fatal("unsafe pair accepted")
			}
			if strings.Contains(out.String(), "Choose language") {
				t.Fatal("invalid pair started wizard")
			}
			for _, path := range []string{core, ui} {
				if data, err := os.ReadFile(path); err == nil && string(data) != "preserve" {
					t.Fatal("existing output changed")
				}
			}
		})
	}
}
