package control

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
)

func isolatedDaemonConfig(t *testing.T) config.Config {
	t.Helper()
	dir, err := os.MkdirTemp("", "krm-life-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	c := config.Default()
	c.API.Enabled = false
	c.Web.Enabled = false
	c.Paths.StateDir, c.Paths.CacheDir = filepath.Join(dir, "state"), filepath.Join(dir, "cache")
	c.Paths.RunDir, c.Paths.LogFile = filepath.Join(dir, "run"), filepath.Join(dir, "daemon.log")
	c.API.UnixSocket = filepath.Join(c.Paths.RunDir, "control.sock")
	c.Web.CredentialsFile = filepath.Join(dir, "credentials.json")
	c.Xray.ConfigDir, c.Xray.ManagedDir = filepath.Join(dir, "xray"), filepath.Join(dir, "xray")
	// Every process invocation is an inert fixture; existing mode never changes
	// host firewall state. A paused store prevents startup routing/benchmarks.
	c.Platform.Kind = "linux-systemd"
	c.Platform.Linux.FirewallMode = "existing"
	c.Platform.XrayStatusCommand = []string{"/bin/true"}
	c.Platform.XrayRestartCommand = []string{"/bin/false"}
	c.Xray.Binary = "/bin/false"
	st, err := store.New(c.Paths.StateDir, c.Paths.CacheDir, model.NewState("lifecycle", c.Xray.SlotTagPrefix, c.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(state *model.State) error { state.AutomaticRoutingPaused = true; return nil }); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDaemonNetworkAPIRequiresSessionAndStops(t *testing.T) {
	c := isolatedDaemonConfig(t)
	c.API.Enabled = true
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.API.Listen = l.Addr().String()
	_ = l.Close()
	c.API.TLS.CertFile, c.API.TLS.KeyFile = filepath.Join(c.Paths.StateDir, "api.crt"), filepath.Join(c.Paths.StateDir, "api.key")
	if err := auth.CreateCredentials(c.Web.CredentialsFile, "admin", "lifecycle-fixture-password"); err != nil {
		t.Fatal(err)
	}
	if err := web.EnsureTLS(c.API.TLS, c.API.Listen); err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(c.API.TLS.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("invalid public certificate")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, Proxy: nil}
	defer transport.CloseIdleConnections()
	cl := &http.Client{Transport: transport, Timeout: 500 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, c, "network-lifecycle") }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("daemon did not stop")
			}
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	var response *http.Response
	for time.Now().Before(deadline) {
		response, err = cl.Get("https://" + c.API.Listen + "/api/v1/status")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("API failed to start: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("network API exposed unauthenticated state: %d", response.StatusCode)
	}
	response, err = cl.Get("https://" + c.API.Listen + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health returned %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		finished = true
		if err != nil {
			t.Fatalf("daemon exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not join shutdown")
	}
	response, err = cl.Get("https://" + c.API.Listen + "/healthz")
	if err == nil {
		response.Body.Close()
		t.Fatal("network API remained available after daemon stopped")
	}
}

func TestDaemonLifecycleReleasesSocketAndBothOwnerLocks(t *testing.T) {
	c := isolatedDaemonConfig(t)
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Serve(ctx, c, "lifecycle") }()
		finished := false
		stop := func() {
			cancel()
			if !finished {
				select {
				case err := <-done:
					finished = true
					if err != nil {
						t.Errorf("daemon exit: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("daemon failed to join scheduler/server shutdown")
				}
			}
		}
		t.Cleanup(stop)
		cl := client.New(c.API.UnixSocket)
		deadline := time.Now().Add(3 * time.Second)
		var response json.RawMessage
		var err error
		for time.Now().Before(deadline) {
			requestCtx, requestCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			response, err = cl.Do(requestCtx, "GET", "/api/v1/status", nil)
			requestCancel()
			if err == nil {
				break
			}
			select {
			case err := <-done:
				finished = true
				cancel()
				t.Fatalf("daemon stopped before serving: %v", err)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
		cl.Close()
		if err != nil {
			stop()
			t.Fatalf("daemon never accepted local client: %v", err)
		}
		var status struct {
			Version string      `json:"version"`
			State   model.State `json:"state"`
		}
		if err := json.Unmarshal(response, &status); err != nil || status.Version != "lifecycle" || !status.State.AutomaticRoutingPaused {
			stop()
			t.Fatalf("unexpected owner status: %s (%v)", response, err)
		}
		stop()
		if _, err := os.Lstat(c.API.UnixSocket); !os.IsNotExist(err) {
			t.Fatalf("socket retained after shutdown: %v", err)
		}
		for _, path := range []string{filepath.Join(c.Paths.StateDir, "daemon.lock"), filepath.Join(c.Xray.ConfigDir, ".krm-daemon.lock")} {
			lock, err := daemonlock.Acquire(path)
			if err != nil {
				t.Fatalf("owner lock retained after shutdown: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDaemonStartupFailureReleasesResources(t *testing.T) {
	for _, failure := range []string{"platform", "credentials", "role"} {
		t.Run(failure, func(t *testing.T) {
			c := isolatedDaemonConfig(t)
			switch failure {
			case "platform":
				c.Platform.Kind = "invalid"
			case "credentials":
				c.API.Enabled = true
			case "role":
				c.Instance.Role = "ui"
			}
			if err := Serve(context.Background(), c, "test"); err == nil {
				t.Fatal("startup failure accepted")
			}
			if _, err := os.Lstat(c.API.UnixSocket); !os.IsNotExist(err) {
				t.Fatalf("failed startup retained socket: %v", err)
			}
			lock, err := daemonlock.Acquire(filepath.Join(c.Paths.StateDir, "daemon.lock"))
			if err != nil {
				t.Fatalf("failed startup retained owner lock: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAlternateStateCannotShareTunnelOwner(t *testing.T) {
	c := isolatedDaemonConfig(t)
	owner, err := daemonlock.Acquire(filepath.Join(c.Xray.ConfigDir, ".krm-daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := Serve(context.Background(), c, "test"); err == nil || !strings.Contains(err.Error(), "tunnel ownership") {
		t.Fatalf("second tunnel owner accepted: %v", err)
	}
	if _, err := os.Stat(c.Paths.RunDir); !os.IsNotExist(err) {
		t.Fatalf("nonowner initialized socket runtime: %v", err)
	}
}
