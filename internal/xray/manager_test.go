package xray

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

type recordingRunner struct {
	mu       sync.Mutex
	commands [][]string
}

func (r *recordingRunner) Run(_ context.Context, command []string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, append([]string(nil), command...))
	return nil, nil
}

type fakePlatform struct{ restarts int }

func (p *fakePlatform) RestartXray(context.Context) error { p.restarts++; return nil }
func (p *fakePlatform) XrayRunning(context.Context) bool  { return true }

func TestBootstrapInstallsManagedConfigAndSelectsInitialSlot(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, "xray")
	stateDir := filepath.Join(tmp, "state")
	runDir := filepath.Join(tmp, "run")
	for _, dir := range []string{configDir, stateDir, runDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	baseRoute := filepath.Join(configDir, "05_routing.json")
	original := `{"routing":{"rules":[{"type":"field","inboundTag":["redirect","tproxy"],"outboundTag":"vless-reality"}]}}`
	if err := os.WriteFile(baseRoute, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeXray := filepath.Join(tmp, "xray-fake")
	if err := os.WriteFile(fakeXray, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	c := config.Default()
	c.Paths.StateDir = stateDir
	c.Paths.RunDir = runDir
	c.Xray.Binary = fakeXray
	c.Xray.ConfigDir = configDir
	c.Xray.ManagedDir = configDir
	c.Xray.BaseRoutingFile = baseRoute
	c.Xray.APIAddress = listener.Addr().String()
	c.Pool.Size = 1

	node := model.Node{ID: "node-1", Label: "Node", Protocol: "vless", Address: "203.0.113.10", Port: 443, UUID: "12345678-1234-1234-1234-123456789abc", Encryption: "none", Flow: "xtls-rprx-vision", Network: "tcp", Security: "reality", ServerName: "example.com", PublicKey: "public-key", Fingerprint: "firefox"}
	slots := []model.Slot{{Index: 0, Tag: c.Xray.SlotTagPrefix + "0", NodeID: node.ID}}
	runner := &recordingRunner{}
	platform := &fakePlatform{}
	manager := NewManager(c, runner, platform)
	if err := manager.Bootstrap(context.Background(), tunnel.DesiredPool{Slots: slots, Nodes: map[string]model.Node{node.ID: node}, Selection: tunnel.Selection{Tag: slots[0].Tag}}); err != nil {
		t.Fatal(err)
	}
	if platform.restarts != 1 {
		t.Fatalf("restarts = %d", platform.restarts)
	}
	patched, err := os.ReadFile(baseRoute)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(patched), `"outboundTag":"vless-reality"`) || !strings.Contains(string(patched), `"balancerTag": "krm-main"`) {
		t.Fatalf("routing file was not patched: %s", patched)
	}
	managed, err := os.ReadFile(filepath.Join(configDir, "04_90_kee_route_manager_outbounds.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(managed, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) == 0 || runner.commands[len(runner.commands)-1][1] != "api" {
		t.Fatalf("initial balancer override was not issued: %#v", runner.commands)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "xray-original", "meta.json")); err != nil {
		t.Fatalf("original snapshot missing: %v", err)
	}

	// User additions and edits after adoption must survive reverse restore.
	var current map[string]any
	if err = json.Unmarshal(patched, &current); err != nil {
		t.Fatal(err)
	}
	routing := current["routing"].(map[string]any)
	routing["domainStrategy"] = "IPIfNonMatch"
	routing["rules"] = append(routing["rules"].([]any), map[string]any{"type": "field", "domain": []string{"example.org"}, "outboundTag": "user-direct"})
	if err = os.WriteFile(baseRoute, pretty(current), 0600); err != nil {
		t.Fatal(err)
	}
	if err = manager.RestoreOriginal(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(baseRoute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(restored), "user-direct") || !strings.Contains(string(restored), "IPIfNonMatch") || strings.Contains(string(restored), `"balancerTag": "krm-main"`) {
		t.Fatalf("restore clobbered user routing edits: %s", restored)
	}
	if err = manager.Bootstrap(context.Background(), tunnel.DesiredPool{Slots: slots, Nodes: map[string]model.Node{node.ID: node}, Selection: tunnel.Selection{Tag: slots[0].Tag}}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitReadyHonorsContext(t *testing.T) {
	c := config.Default()
	c.Xray.APIAddress = "127.0.0.1:1"
	manager := NewManager(c, &recordingRunner{}, &fakePlatform{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := manager.WaitReady(ctx, time.Second); err == nil {
		t.Fatal("expected readiness error")
	}
}

func TestRestoreNeverConfiguredInstallationIsIdempotent(t *testing.T) {
	c := config.Default()
	root := t.TempDir()
	c.Paths.StateDir = root
	c.Xray.ManagedDir = filepath.Join(root, "configs")
	c.Xray.ConfigDir = c.Xray.ManagedDir
	c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "routing.json")
	p := &fakePlatform{}
	m := NewManager(c, &recordingRunner{}, p)
	if err := m.RestoreOriginal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.restarts != 0 {
		t.Fatal("untouched Xray unnecessarily restarted")
	}
	if err := os.MkdirAll(c.Xray.ManagedDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Xray.ManagedDir, "04_90_kee_route_manager_outbounds.json"), []byte(`{"outbounds":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.RestoreOriginal(context.Background()); err == nil {
		t.Fatal("partial managed installation was silently ignored")
	}
}
