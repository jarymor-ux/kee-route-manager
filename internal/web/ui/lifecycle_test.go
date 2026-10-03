package ui

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/tlsutil"
)

func TestTLSIdentityAndPermissionsPersistAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	c := config.TLS{Enabled: true, AutoGenerate: true, CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem"), Hosts: []string{"router.test", "2001:db8::1"}}
	if err := tlsutil.EnsureTLS(c, "127.0.0.1:9444"); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"router.test", "2001:db8::1", "localhost", "127.0.0.1", "::1"} {
		if err := cert.VerifyHostname(host); err != nil {
			t.Fatalf("TLS host %s: %v", host, err)
		}
	}
	before, err := os.ReadFile(c.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := tlsutil.EnsureTLS(c, "127.0.0.1:9444"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(c.CertFile)
	if err != nil || string(after) != string(before) {
		t.Fatal("restart unexpectedly rotated trusted TLS identity")
	}
	info, err := os.Stat(c.KeyFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("TLS key permissions: %v %v", info, err)
	}
	c.AutoGenerate = false
	c.CertFile = filepath.Join(dir, "missing.pem")
	if err := tlsutil.EnsureTLS(c, "127.0.0.1:9444"); err == nil {
		t.Fatal("missing externally managed TLS identity accepted")
	}
}

func TestUIServeLifecycleAndVerifiedReadiness(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"role":"controller","status":"safe_degraded"}`))
	}))
	defer upstream.Close()
	for _, encrypted := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "HTTPS"}[encrypted], func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := l.Addr().String()
			_ = l.Close()
			dir := t.TempDir()
			c := config.Default()
			c.Instance.Role = "ui"
			c.Paths.LogFile = filepath.Join(dir, "ui.log")
			c.Web.Listen = address
			c.Web.TLS = config.TLS{Enabled: encrypted, AutoGenerate: true, CertFile: filepath.Join(dir, "ui.crt"), KeyFile: filepath.Join(dir, "ui.key")}
			c.UIProxy.Upstream = upstream.URL
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- Serve(ctx, c) }()
			finished := false
			defer func() {
				cancel()
				if !finished {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("UI did not shut down")
					}
				}
			}()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				probeCtx, stop := context.WithTimeout(context.Background(), 150*time.Millisecond)
				err = Ready(probeCtx, c)
				stop()
				if err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				t.Fatalf("UI never served verified static and health endpoints: %v", err)
			}
			cancel()
			select {
			case err := <-done:
				finished = true
				if err != nil {
					t.Fatalf("UI shutdown: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("UI failed to exit after cancellation")
			}
			probeCtx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if err := Ready(probeCtx, c); err == nil {
				t.Fatal("stopped UI still accepted requests")
			}
		})
	}
}
