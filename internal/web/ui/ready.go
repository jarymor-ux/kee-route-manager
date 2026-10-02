package ui

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

// Ready checks the existing UI and its proxied controller. It is read-only and
// verifies the UI's certificate using the configured public certificate file.
func Ready(ctx context.Context, c config.Config) error {
	if c.Instance.Role != "ui" || !c.Web.Enabled || !c.UIProxy.Enabled {
		return fmt.Errorf("UI readiness requires instance.role=ui and enabled UI/web")
	}
	host, port, e := net.SplitHostPort(c.Web.Listen)
	if e != nil {
		return e
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	scheme := "http"
	if c.Web.TLS.Enabled {
		scheme = "https"
		b, e := os.ReadFile(c.Web.TLS.CertFile)
		if e != nil {
			return fmt.Errorf("read UI certificate: %w", e)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(b) {
			return fmt.Errorf("UI certificate file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig, Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSHandshakeTimeout: 3 * time.Second, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	origin := scheme + "://" + net.JoinHostPort(host, port)
	for _, path := range []string{"/", "/healthz"} {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
		if e != nil {
			return e
		}
		resp, e := client.Do(req)
		if e != nil {
			return fmt.Errorf("UI readiness %s: %w", path, e)
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if len(b) > 64<<10 {
			return fmt.Errorf("UI readiness response exceeds limit")
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("UI readiness %s returned HTTP %d", path, resp.StatusCode)
		}
		if path == "/" {
			if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || !strings.Contains(string(b), "<html") {
				return fmt.Errorf("UI static document is unavailable")
			}
			continue
		}
		var health struct {
			Role   string `json:"role"`
			Status string `json:"status"`
		}
		if e = json.Unmarshal(b, &health); e != nil || health.Role != "controller" || (health.Status != "ok" && health.Status != "safe_degraded") {
			return fmt.Errorf("proxied controller readiness is invalid")
		}
	}
	return nil
}
