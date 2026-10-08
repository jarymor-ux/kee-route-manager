package control

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
	"github.com/jarymor-ux/kee-route-manager/internal/tlsutil"
)

func TestTrialDaemonHelper(t *testing.T) {
	path := os.Getenv("KRM_TEST_TRIAL_CONFIG")
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	var c config.Config
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err != nil {
		os.Exit(3)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := Serve(ctx, c, "trial-fixture"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func trialFixture(t *testing.T) (config.Config, string, *http.Client) {
	t.Helper()
	c := isolatedDaemonConfig(t)
	if err := os.WriteFile(filepath.Join(c.Paths.CacheDir, "nodes.json"), []byte("[]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(c.Paths.StateDir, "commands-executed")
	status := filepath.Join(filepath.Dir(c.Paths.StateDir), "status-fixture")
	if err := os.WriteFile(status, []byte("#!/bin/sh\nprintf called >> '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c.Platform.XrayStatusCommand = []string{status}
	c.API.Enabled = true
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.API.Listen = l.Addr().String()
	l.Close()
	c.API.TLS.CertFile, c.API.TLS.KeyFile = filepath.Join(c.Paths.StateDir, "api.crt"), filepath.Join(c.Paths.StateDir, "api.key")
	if err = auth.CreateCredentials(c.Web.CredentialsFile, "admin", "trial-fixture-password"); err != nil {
		t.Fatal(err)
	}
	if err = tlsutil.EnsureTLS(c.API.TLS, c.API.Listen); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(c.API.TLS.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(b) {
		t.Fatal("certificate unavailable")
	}
	cl := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	t.Cleanup(cl.CloseIdleConnections)
	return c, marker, cl
}

func protectedTrialTree(t *testing.T, c config.Config) map[string][32]byte {
	t.Helper()
	result := map[string][32]byte{}
	for _, dir := range []string{c.Paths.StateDir, c.Paths.CacheDir, c.Xray.ConfigDir} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() || d.Name() == "daemon.lock" || d.Name() == ".krm-daemon.lock" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err == nil {
				result[path] = sha256.Sum256(b)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func startTrialProcess(t *testing.T, c config.Config, nonce string) (*exec.Cmd, <-chan error) {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(c.Paths.StateDir), "trial-input.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTrialDaemonHelper$")
	cmd.Env = append(os.Environ(), "KRM_TEST_TRIAL_CONFIG="+path, "KRM_UPDATE_TRIAL="+nonce)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("trial did not stop")
		}
	})
	return cmd, done
}

func waitTrialHealth(t *testing.T, c config.Config, done ...<-chan error) map[string]any {
	t.Helper()
	cl := client.New(c.API.UnixSocket)
	defer cl.Close()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(done) > 0 && done[0] != nil {
			select {
			case err, ok := <-done[0]:
				if !ok || err == nil {
					t.Fatal("trial process exited before health became available")
				}
				t.Fatalf("trial process exited before health became available: %v", err)
			default:
			}
		}

		// The production health handler allows trialReady up to five seconds.
		// Give a single probe enough time to complete under the race detector.
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		b, err := cl.Do(ctx, "GET", "/healthz", nil)
		cancel()
		if err == nil {
			var health map[string]any
			if err = json.Unmarshal(b, &health); err != nil {
				t.Fatal(err)
			}
			return health
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("trial health never became available")
	return nil
}

func TestTrialProcessStaysReadOnlyAndActivatesWithContinuousOwnership(t *testing.T) {
	c, marker, public := trialFixture(t)
	nonce := strings.Repeat("a", 64)
	before := protectedTrialTree(t, c)
	cmd, done := startTrialProcess(t, c, nonce)
	health := waitTrialHealth(t, c, done)
	if health["status"] != "trial_ready" || health["version"] != "trial-fixture" || health["update_nonce"] != nonce || health["pid"] != float64(cmd.Process.Pid) || health["reconciled"] != true {
		t.Fatalf("unexpected health: %#v", health)
	}
	local := client.New(c.API.UnixSocket)
	defer local.Close()
	for _, path := range []string{"/api/v1/actions/benchmark", "/api/v1/actions/direct", "/internal/update/prepare"} {
		if _, err := local.Do(context.Background(), "POST", path, nil); err == nil {
			t.Fatalf("trial accepted %s", path)
		}
	}
	if _, err := local.Do(context.Background(), "POST", "/internal/update/activate", map[string]string{"nonce": strings.Repeat("b", 64)}); err == nil {
		t.Fatal("wrong nonce accepted")
	}
	resp, err := public.Post("https://"+c.API.Listen+"/internal/update/activate", "application/json", strings.NewReader(`{"nonce":"`+nonce+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("public activation code=%d", resp.StatusCode)
	}
	for i := 0; i < 3; i++ {
		waitTrialHealth(t, c, done)
	}
	if after := protectedTrialTree(t, c); !reflect.DeepEqual(before, after) {
		t.Fatal("trial changed persistent state/cache/Xray tree")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("trial executed configured process commands")
	}
	if _, err := os.Stat(c.Paths.LogFile); !os.IsNotExist(err) {
		t.Fatal("trial initialized normal logging")
	}
	if _, err := local.Do(context.Background(), "POST", "/internal/update/activate", map[string]string{"nonce": nonce}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := local.Do(context.Background(), "GET", "/api/v1/status", nil)
		if err == nil && strings.Contains(string(b), `"trial-fixture"`) {
			break
		}
		if time.Now().Add(20 * time.Millisecond).After(deadline) {
			t.Fatal("same process failed activation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("normal lifecycle did not start after activation")
	}
	for _, path := range []string{filepath.Join(c.Paths.StateDir, "daemon.lock"), filepath.Join(c.Xray.ConfigDir, ".krm-daemon.lock")} {
		owner, err := daemonlock.Acquire(path)
		if err == nil {
			owner.Close()
			t.Fatal("activation lost ownership")
		}
	}
}

func TestTrialRefusesDriftAtHealthAndActivation(t *testing.T) {
	c, _, _ := trialFixture(t)
	nonce := strings.Repeat("c", 64)
	_, done := startTrialProcess(t, c, nonce)
	waitTrialHealth(t, c, done)
	path := filepath.Join(c.Paths.StateDir, "state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	before := protectedTrialTree(t, c)
	cl := client.New(c.API.UnixSocket)
	defer cl.Close()
	for _, request := range []struct {
		method, path string
		body         any
	}{{"GET", "/healthz", nil}, {"POST", "/internal/update/activate", map[string]string{"nonce": nonce}}} {
		if _, err := cl.Do(context.Background(), request.method, request.path, request.body); err == nil {
			t.Fatalf("accepted changed state at %s", request.path)
		}
	}
	if !reflect.DeepEqual(before, protectedTrialTree(t, c)) {
		t.Fatal("trial tried to recover changed primary state")
	}
}

func TestPrepareIsLocalOnlyAndFreezesEveryMutation(t *testing.T) {
	c, _, public := trialFixture(t)
	cmd, done := startTrialProcess(t, c, "")
	waitTrialHealth(t, c, done)
	resp, err := public.Post("https://"+c.API.Listen+"/internal/update/prepare", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("public prepare reachable: %d", resp.StatusCode)
	}
	local := client.New(c.API.UnixSocket)
	defer local.Close()
	body, err := local.Do(context.Background(), "POST", "/internal/update/prepare", nil)
	if err != nil {
		t.Fatal(err)
	}
	var prepared struct {
		Prepared bool   `json:"prepared"`
		Version  string `json:"version"`
		PID      int    `json:"pid"`
	}
	if err = json.Unmarshal(body, &prepared); err != nil || !prepared.Prepared || prepared.Version != "trial-fixture" || prepared.PID != cmd.Process.Pid {
		t.Fatalf("bad prepare result %s: %v", body, err)
	}
	before := protectedTrialTree(t, c)
	for _, path := range []string{"/api/v1/actions/benchmark", "/api/v1/actions/direct", "/api/v1/actions/restore-xray", "/api/v1/actions/switch", "/api/v1/update/apply"} {
		if _, err := local.Do(context.Background(), "POST", path, nil); err == nil {
			t.Fatalf("prepared daemon accepted %s", path)
		}
	}
	if _, err := local.Do(context.Background(), "GET", "/api/v1/status", nil); err != nil {
		t.Fatal("prepared daemon lost read-only status")
	}
	if _, err := local.Do(context.Background(), "POST", "/internal/update/prepare", nil); err != nil {
		t.Fatal("prepare is not idempotent")
	}
	if !reflect.DeepEqual(before, protectedTrialTree(t, c)) {
		t.Fatal("prepared daemon changed routing state")
	}
}

func TestTrialSelectionValidatesActualAPIOverride(t *testing.T) {
	for _, tc := range []struct {
		response string
		valid    bool
	}{
		{`{"balancer":{"override":{"target":"slot-0"},"principleTarget":{"tag":["krm-persisted-selection"]}}}`, true},
		{`{"balancer":{"override":{"target":"wrong-slot"},"principleTarget":{"tag":["krm-persisted-selection"]}}}`, false},
		{`{"balancer":{"principleTarget":{"tag":["krm-persisted-selection"]}}}`, true},
		{`{"balancer":{"principle_target":{"tag":["krm-persisted-selection"]}}}`, true},
		{`{"balancer":{"principleTarget":{"tag":["krm-persisted-selection","foreign"]}}}`, false},
		{`{"balancer":{"principleTarget":{"tag":["wrong-slot"]}}}`, false},
		{`{"balancer":null}`, false},
		{`{}`, false},
		{`not json`, false},
	} {
		if err := validateTrialSelection([]byte(tc.response), "slot-0"); (err == nil) != tc.valid {
			t.Fatalf("response=%s err=%v", tc.response, err)
		}
	}
}

func TestTrialStartupRejectsUnsafeStateWithoutRepair(t *testing.T) {
	for _, failure := range []string{"missing-tls", "running-operation", "orphan-routing", "orphan-fragment", "invalid-nonce"} {
		t.Run(failure, func(t *testing.T) {
			c, marker, _ := trialFixture(t)
			if err := os.MkdirAll(c.Xray.ConfigDir, 0700); err != nil {
				t.Fatal(err)
			}
			nonce := strings.Repeat("e", 64)
			switch failure {
			case "missing-tls":
				if err := os.Remove(c.API.TLS.KeyFile); err != nil {
					t.Fatal(err)
				}
			case "running-operation":
				if err := os.WriteFile(filepath.Join(c.Paths.StateDir, "operation.json"), []byte(`{"status":"running"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "orphan-routing":
				if err := os.WriteFile(filepath.Join(c.Xray.ConfigDir, "05_routing.json"), []byte(`{"routing":{"rules":[{"balancerTag":"`+c.Xray.BalancerTag+`"}]}}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "orphan-fragment":
				if err := os.WriteFile(filepath.Join(c.Xray.ConfigDir, "00_90_kee_route_manager_api.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid-nonce":
				nonce = strings.Repeat("G", 64)
			}
			before := protectedTrialTree(t, c)
			_, done := startTrialProcess(t, c, nonce)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unsafe trial succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("unsafe trial stayed alive")
			}
			if !reflect.DeepEqual(before, protectedTrialTree(t, c)) {
				t.Fatal("unsafe trial repaired state")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("unsafe trial invoked service command")
			}
		})
	}
}

func TestTrialRejectsCorruptUsersWithoutWritingMigration(t *testing.T) {
	c, _, _ := trialFixture(t)
	if err := os.MkdirAll(c.Xray.ConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := trialReady(context.Background(), c, "trial-fixture"); err != nil {
		t.Fatal(err)
	}
	sidecar := c.Web.CredentialsFile + ".users.json"
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatal("read-only trial persisted migrated users")
	}
	if err := os.WriteFile(sidecar, []byte("invalid users fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	before := protectedTrialTree(t, c)
	if err := trialReady(context.Background(), c, "trial-fixture"); err == nil {
		t.Fatal("trial accepted corrupt users and would fail after activation")
	}
	if after := protectedTrialTree(t, c); !reflect.DeepEqual(before, after) {
		t.Fatal("failed trial modified private state")
	}
}

func TestTrialRefusesRunningCompanionOperation(t *testing.T) {
	c, _, _ := trialFixture(t)
	if err := os.WriteFile(filepath.Join(c.Paths.StateDir, "operation.json"), []byte(`{"status":"succeeded","operations":[{"status":"succeeded"},{"status":"running"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	before := protectedTrialTree(t, c)
	if err := trialReady(context.Background(), c, "trial-fixture"); err == nil || !strings.Contains(err.Error(), "quiescent") {
		t.Fatalf("trial accepted unfinished companion: %v", err)
	}
	if after := protectedTrialTree(t, c); !reflect.DeepEqual(before, after) {
		t.Fatal("trial repaired operation record")
	}
}
