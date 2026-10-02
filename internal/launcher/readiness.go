package launcher

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
)

type readiness struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	Nonce      string `json:"update_nonce"`
	PID        int    `json:"pid"`
	Reconciled bool   `json:"reconciled"`
}

func checkTrial(ctx context.Context, c config.Config, uiConfig, version, nonce string, daemon, ui *child) error {
	if !daemon.alive() || uiConfig != "" && !ui.alive() {
		return errors.New("candidate process exited")
	}
	cl := client.New(c.API.UnixSocket)
	defer cl.Close()
	b, err := cl.Do(ctx, "GET", "/healthz", nil)
	if err != nil {
		return err
	}
	verify := func(b []byte) error {
		var h readiness
		if err := json.Unmarshal(b, &h); err != nil {
			return err
		}
		if h.Status != "trial_ready" || h.Version != version || h.Nonce != nonce || h.PID != daemon.cmd.Process.Pid || !h.Reconciled {
			return errors.New("candidate readiness identity or reconciliation mismatch")
		}
		return nil
	}
	if err = verify(b); err != nil {
		return err
	}
	if c.API.Enabled {
		b, err = networkHealth(ctx, c.API.Listen, c.API.TLS)
		if err != nil {
			return fmt.Errorf("candidate API: %w", err)
		}
		if err = verify(b); err != nil {
			return err
		}
	}
	if uiConfig != "" {
		uc, err := config.Load(uiConfig)
		if err != nil {
			return err
		}
		b, err = networkHealth(ctx, uc.Web.Listen, uc.Web.TLS)
		if err != nil {
			return fmt.Errorf("candidate UI: %w", err)
		}
		return verify(b)
	}
	return nil
}

func networkHealth(ctx context.Context, address string, c config.TLS) ([]byte, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	t := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	defer t.CloseIdleConnections()
	if c.Enabled {
		scheme = "https"
		ca, err := os.ReadFile(c.CertFile)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return nil, errors.New("invalid readiness TLS trust certificate")
		}
		t.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", scheme+"://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		return nil, err
	}
	h := &http.Client{Transport: t, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("readiness redirect refused") }}
	resp, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("readiness HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<10))
}
