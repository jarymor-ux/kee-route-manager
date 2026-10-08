package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/redact"
)

// launcherRequest talks only to the configured owner-only local launcher. It
// forwards its acceptance/status, never downloads or executes an update here.
func (s *Server) launcherRequest(ctx context.Context, method, path string, body any) (int, json.RawMessage, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
	}
	socket := s.cfg.Update.LauncherSocket
	transport := &http.Transport{DisableCompression: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if err := trustedLauncherSocket(socket); err != nil {
			return nil, err
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	timeout := 10 * time.Second
	if path == "/check" {
		timeout = 2*time.Minute + 10*time.Second
	}
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, method, "http://launcher"+path, bytes.NewReader(data))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return 0, nil, err
	}
	if len(data) > 4<<20 || !json.Valid(data) || response.StatusCode < 200 || response.StatusCode >= 600 || (response.StatusCode >= 300 && response.StatusCode < 400) {
		return 0, nil, fmt.Errorf("invalid update launcher response")
	}
	if response.StatusCode >= 400 {
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &failure) != nil || failure.Error == "" {
			return 0, nil, fmt.Errorf("invalid update launcher error")
		}
		data, err = json.Marshal(map[string]string{"error": redact.Text(failure.Error)})
		if err != nil {
			return 0, nil, err
		}
	} else if path == "/apply" || path == "/panel/apply" || path == "/panel/confirm" {
		var result struct {
			Accepted bool `json:"accepted"`
		}
		if response.StatusCode != http.StatusAccepted || json.Unmarshal(data, &result) != nil || !result.Accepted {
			return 0, nil, fmt.Errorf("update launcher did not accept the request")
		}
	} else if response.StatusCode != http.StatusOK || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return 0, nil, fmt.Errorf("invalid update launcher status")
	}
	return response.StatusCode, json.RawMessage(data), nil
}

func trustedLauncherSocket(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("update launcher socket must be absolute")
	}
	for i, p := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 ||
			(i == 0 && !info.IsDir()) || (i == 1 && info.Mode()&os.ModeSocket == 0) {
			return fmt.Errorf("untrusted update launcher socket")
		}
	}
	return nil
}
