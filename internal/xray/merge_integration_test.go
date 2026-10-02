package xray

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

// Run with KRM_TEST_XRAY_BINARY pointing at a real Xray executable. All listeners
// and configurations are private test fixtures; no network service is required.
func TestRealXrayMergedRoutingLifecycle(t *testing.T) {
	binary := os.Getenv("KRM_TEST_XRAY_BINARY")
	if binary == "" {
		t.Skip("set KRM_TEST_XRAY_BINARY to exercise Xray's actual multi-file loader")
	}
	for _, baseName := range []string{"05_routing.json", "01_routing.json", ""} {
		t.Run(baseName, func(t *testing.T) {
			c := reproConfig(t)
			c.Xray.Binary = binary
			c.Xray.APIAddress = fmt.Sprintf("127.0.0.1:%d", unusedMergePort(t))
			c.Xray.HealthProxyPort = unusedMergePort(t)
			c.Xray.ProbePortStart = unusedMergePort(t)
			c.Pool.Size = 1
			redirectPort, userPort := unusedMergePort(t), unusedMergePort(t)
			original := pretty(map[string]any{"routing": map[string]any{
				"domainStrategy": "AsIs",
				"balancers":      []any{map[string]any{"tag": "user-balancer", "selector": []string{"legacy"}, "strategy": map[string]any{"type": "random"}}},
				"rules": []any{
					map[string]any{"type": "field", "inboundTag": []string{"redirect"}, "outboundTag": "legacy"},
					map[string]any{"type": "field", "inboundTag": []string{"user"}, "balancerTag": "user-balancer"},
					map[string]any{"type": "field", "network": "tcp,udp", "outboundTag": "blocked"},
				},
			}})
			if baseName != "" {
				c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, baseName)
				c.Xray.Route.ReplaceOutboundTags = []string{"legacy"}
				if err := os.WriteFile(c.Xray.BaseRoutingFile, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			legacy := map[string]any{
				"inbounds": []any{httpInbound("redirect", redirectPort), httpInbound("user", userPort)},
				"outbounds": []any{
					map[string]any{"tag": "legacy", "protocol": "freedom", "settings": map[string]any{}},
					blackholeOutbound("blocked"),
				},
			}
			if err := os.WriteFile(filepath.Join(c.Xray.ConfigDir, "04_outbounds.json"), pretty(legacy), 0600); err != nil {
				t.Fatal(err)
			}
			platform := &mergeProcessPlatform{cfg: c}
			t.Cleanup(platform.stop)
			m := NewManager(c, mergeExecRunner{}, platform)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			// Empty slots are deliberate blackholes: routing to the default freedom
			// outbound instead would turn both health and probes into false successes.
			desired := tunnel.DesiredPool{Selection: tunnel.Selection{Tag: c.Xray.SlotTagPrefix + "0"}}
			if err := m.Bootstrap(ctx, desired); err != nil {
				t.Fatalf("real Xray rejected generated candidate: %v", err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			defer server.Close()
			assertProxy := func(port int, works bool) {
				t.Helper()
				proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
				transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: time.Second}
				response, err := client.Get(server.URL)
				ok := err == nil && response.StatusCode == http.StatusNoContent
				if response != nil {
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
				if ok != works {
					t.Fatalf("proxy %d reachable=%t, want %t (error %v)", port, ok, works, err)
				}
			}
			assertProxy(c.Xray.HealthProxyPort, false)
			assertProxy(c.Xray.ProbePortStart, false)
			if baseName != "" {
				assertProxy(redirectPort, false)
				assertProxy(userPort, true)
			}
			// This is a real RoutingService RPC; a listening API port alone cannot
			// prove that its routing rule survived Xray's config merge.
			if err := m.Direct(ctx); err != nil {
				t.Fatalf("real balancer RPC failed: %v", err)
			}
			assertProxy(c.Xray.HealthProxyPort, true)
			assertProxy(c.Xray.ProbePortStart, false)
			if err := platform.RestartXray(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.WaitReady(ctx, 5*time.Second); err != nil {
				t.Fatal(err)
			}
			assertProxy(c.Xray.HealthProxyPort, true)
			assertProxy(c.Xray.ProbePortStart, false)
			if baseName != "" {
				assertProxy(redirectPort, true)
				assertProxy(userPort, true)
			}
			if err := m.RestoreOriginal(ctx); err != nil {
				t.Fatal(err)
			}
			if baseName != "" {
				data, err := os.ReadFile(c.Xray.BaseRoutingFile)
				if err != nil || string(data) != string(original) {
					t.Fatalf("original user rules/balancer not restored exactly: %s %v", data, err)
				}
			}
			waitMergePort(t, redirectPort)
			assertProxy(redirectPort, true)
			assertProxy(userPort, true)
			for _, path := range m.snapshotPaths() {
				if path != c.Xray.BaseRoutingFile {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("restore left managed fragment %s: %v", path, err)
					}
				}
			}
		})
	}
}

type mergeExecRunner struct{}

func (mergeExecRunner) Run(ctx context.Context, argv []string) ([]byte, error) {
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(child, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return data, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(data)))
	}
	return data, nil
}

type mergeProcessPlatform struct {
	cfg config.Config
	cmd *exec.Cmd
}

func (p *mergeProcessPlatform) stop() {
	if p.cmd != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
		p.cmd = nil
	}
}

func (p *mergeProcessPlatform) RestartXray(ctx context.Context) error {
	p.stop()
	p.cmd = exec.CommandContext(ctx, p.cfg.Xray.Binary, "run", "-confdir", p.cfg.Xray.ConfigDir)
	if err := p.cmd.Start(); err != nil {
		p.cmd = nil
		return err
	}
	return nil
}
func (p *mergeProcessPlatform) XrayRunning(context.Context) bool { return p.cmd != nil }

func unusedMergePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func waitMergePort(t *testing.T, port int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("test Xray listener never became ready")
}
