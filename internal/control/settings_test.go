package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
	"go.yaml.in/yaml/v3"
)

func settingsDaemonFile(t *testing.T, c config.Config, path string) config.Config {
	t.Helper()
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	c, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func settingsDaemonConfig(t *testing.T) config.Config {
	t.Helper()
	c := isolatedDaemonConfig(t)
	c.Subscriptions.CacheEnabled = false
	c.Subscriptions.Sources = []config.Source{{ID: "primary", URL: "https://subscription.invalid/private-token", Enabled: false, Headers: map[string]string{"Authorization": "Bearer synthetic-private"}}}
	c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "route.json")
	c.Targets = []config.Target{
		{ID: "score", URL: "https://score.invalid/", Role: "score", Weight: 1, Policy: "exact:204", MaxResponseBytes: 64 << 10},
		{ID: "health_a", URL: "https://health-a.invalid/", Role: "health", Weight: 1, Policy: "exact:204", MaxResponseBytes: 64 << 10},
		{ID: "health_b", URL: "https://health-b.invalid/", Role: "health", Weight: 1, Policy: "exact:204", MaxResponseBytes: 64 << 10},
	}
	return settingsDaemonFile(t, c, filepath.Join(filepath.Dir(c.Paths.StateDir), "config.yaml"))
}

func TestSettingsRejectsExternalVisibleAndInfrastructureChanges(t *testing.T) {
	for _, change := range []string{"visible", "infrastructure"} {
		t.Run(change, func(t *testing.T) {
			c := settingsDaemonConfig(t)
			s := newSettingsRuntime(c)
			snapshot, _, err := s.SettingsSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			modified := c
			if change == "visible" {
				modified.Benchmark.FullInterval = config.Dur(9 * time.Minute)
			} else {
				modified.Paths.StateDir += "-different"
			}
			settingsDaemonFile(t, modified, c.SourcePath())
			if _, _, err := s.SettingsSnapshot(); !errors.Is(err, config.ErrSettingsConflict) {
				t.Fatalf("disk-only settings shown as active: %v", err)
			}
			if _, _, err := s.SaveSettings(snapshot.Settings, snapshot.Revision); !errors.Is(err, config.ErrSettingsConflict) {
				t.Fatalf("external settings adopted: %v", err)
			}
		})
	}
}

func TestSettingsStartupRefusesNewInfrastructureUnderOldLocks(t *testing.T) {
	c := settingsDaemonConfig(t)
	modified := c
	modified.Paths.StateDir += "-unowned"
	settingsDaemonFile(t, modified, c.SourcePath())
	err := Serve(context.Background(), c, "settings-fixture")
	if err == nil || !strings.Contains(err.Error(), "infrastructure changed") {
		t.Fatalf("startup drift accepted: %v", err)
	}
	if _, err := os.Stat(modified.Paths.StateDir); !os.IsNotExist(err) {
		t.Fatal("constructed state without its owner lock")
	}
}

func TestSettingsApplyRetainsOwnerSessionAndRollsBackFailedRuntime(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failed-managed-source-validation"}[fail], func(t *testing.T) {
			c := settingsDaemonConfig(t)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			c.API.Enabled, c.API.TLS.Enabled = true, false
			c.API.Listen = listener.Addr().String()
			listener.Close()
			c = settingsDaemonFile(t, c, c.SourcePath())
			if err := auth.CreateCredentials(c.Web.CredentialsFile, "admin", "settings-fixture-password"); err != nil {
				t.Fatal(err)
			}
			managed := []config.Source{{ID: "first", URL: "https://first.invalid/private", Enabled: false}, {ID: "second", URL: "https://second.invalid/private", Enabled: false}}
			if err := subscription.NewSourceStore(c.Paths.StateDir).Save(managed); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(c.SourcePath())
			managedBefore, _ := os.ReadFile(filepath.Join(c.Paths.StateDir, "subscriptions.json"))
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- Serve(ctx, c, "settings-fixture") }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("daemon stop: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("daemon failed to join runtime")
				}
			}()
			jar, _ := cookiejar.New(nil)
			// Password hashing under the race detector takes several seconds; the
			// network timeout must not masquerade as a runtime lifecycle failure.
			client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
			defer client.CloseIdleConnections()
			base := "http://" + c.API.Listen
			var response *http.Response
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
				response, err = client.Get(base + "/healthz")
				if err == nil {
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
					if response.StatusCode == 200 {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil || response == nil || response.StatusCode != 200 {
				t.Fatalf("initial readiness: %v", err)
			}
			ownerBefore, _ := os.ReadFile(filepath.Join(c.Paths.StateDir, "daemon.lock"))
			response, err = client.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"admin","password":"settings-fixture-password"}`))
			if err != nil {
				t.Fatal(err)
			}
			var session struct {
				CSRF string `json:"csrf"`
			}
			err = json.NewDecoder(response.Body).Decode(&session)
			response.Body.Close()
			if err != nil || session.CSRF == "" {
				t.Fatalf("login: %v", err)
			}
			get := func() (config.SettingsSnapshot, web.SettingsApplyStatus, error) {
				response, err := client.Get(base + "/api/v1/settings")
				if err != nil {
					return config.SettingsSnapshot{}, web.SettingsApplyStatus{}, err
				}
				defer response.Body.Close()
				if response.StatusCode != 200 {
					io.Copy(io.Discard, response.Body)
					return config.SettingsSnapshot{}, web.SettingsApplyStatus{}, errors.New("settings not ready")
				}
				var result struct {
					config.SettingsSnapshot
					Apply web.SettingsApplyStatus `json:"apply"`
				}
				err = json.NewDecoder(response.Body).Decode(&result)
				return result.SettingsSnapshot, result.Apply, err
			}
			snapshot, _, err := get()
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Settings.Benchmark.FullInterval = config.Dur(5 * time.Minute)
			if fail {
				snapshot.Settings.Subscriptions.MaxSources = 1
			}
			body, _ := json.Marshal(map[string]any{"settings": snapshot.Settings, "revision": snapshot.Revision})
			request, _ := http.NewRequest("POST", base+"/api/v1/settings/save", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-KRM-CSRF", session.CSRF)
			response, err = client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			var accepted struct {
				Revision string `json:"revision"`
			}
			err = json.NewDecoder(response.Body).Decode(&accepted)
			response.Body.Close()
			if err != nil || response.StatusCode != 202 || accepted.Revision == "" {
				t.Fatalf("save did not acknowledge before reload: %v status=%d", err, response.StatusCode)
			}
			want := "applied"
			if fail {
				want = "rolled_back"
			}
			var after config.SettingsSnapshot
			var apply web.SettingsApplyStatus
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
				after, apply, err = get()
				if err == nil && apply.Status == want && apply.Revision == accepted.Revision {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil || apply.Status != want || apply.Revision != accepted.Revision {
				t.Fatalf("runtime result: %+v %v", apply, err)
			}
			if fail {
				restored, _ := os.ReadFile(c.SourcePath())
				if !bytes.Equal(original, restored) {
					t.Fatal("failed config not restored exactly")
				}
				if after.Revision != snapshot.Revision {
					t.Fatal("rollback revision changed")
				}
			} else {
				if after.Settings.Benchmark.FullInterval.Duration != 5*time.Minute || after.Revision != accepted.Revision {
					t.Fatal("new config not active")
				}
			}
			ownerAfter, _ := os.ReadFile(filepath.Join(c.Paths.StateDir, "daemon.lock"))
			if !bytes.Equal(ownerBefore, ownerAfter) {
				t.Fatal("settings apply released/reacquired ownership")
			}
			if competing, err := daemonlock.Acquire(filepath.Join(c.Paths.StateDir, "daemon.lock")); err == nil {
				competing.Close()
				t.Fatal("second owner admitted during runtime reload")
			}
			managedAfter, _ := os.ReadFile(filepath.Join(c.Paths.StateDir, "subscriptions.json"))
			if !bytes.Equal(managedBefore, managedAfter) {
				t.Fatal("source sidecar changed")
			}
		})
	}
}
