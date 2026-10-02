package xray

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

func reviewPoolFixture(t *testing.T) (config.Config, tunnel.DesiredPool, *recordingRunner, *fakePlatform) {
	t.Helper()
	c := reproConfig(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	c.Xray.APIAddress = listener.Addr().String()
	node := testNode()
	slots := []model.Slot{{Index: 0, NodeID: node.ID, Tag: c.Xray.SlotTagPrefix + "0"}}
	desired := tunnel.DesiredPool{Previous: slots, Slots: slots, Nodes: map[string]model.Node{node.ID: node}, ActiveSlot: 0, Selection: tunnel.Selection{Tag: slots[0].Tag}}
	runner, platform := &recordingRunner{}, &fakePlatform{}
	if err := NewManager(c, runner, platform).Bootstrap(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	platform.restarts = 0
	runner.commands = nil
	return c, desired, runner, platform
}

func TestApplyPoolRefreshesGeneratedConfiguration(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		for _, direct := range []bool{false, true} {
			t.Run(fmt.Sprintf("dynamic_%v/direct_%v", dynamic, direct), func(t *testing.T) {
				c, desired, runner, platform := reviewPoolFixture(t)
				c.Xray.DynamicAPI = dynamic
				c.Xray.HealthProxyPort = 19888
				if direct {
					desired.Selection.Tag = c.Xray.ManagedDirectTag
				}
				m := NewManager(c, runner, platform)
				proof, err := m.PrepareReplay(context.Background(), desired, false)
				if err != nil {
					t.Fatal(err)
				}
				if err = m.ApplyPool(context.Background(), desired); err != nil {
					t.Fatal(err)
				}
				if platform.restarts != 1 {
					t.Fatalf("generated configuration change restarts = %d; want 1", platform.restarts)
				}
				want, err := BuildManaged(c, desired.Slots, desired.Nodes)
				if err != nil {
					t.Fatal(err)
				}
				want.Outbounds, err = selectOutbound(want.Outbounds, desired.Selection.Tag)
				if err != nil {
					t.Fatal(err)
				}
				for name, data := range map[string][]byte{"00_90_kee_route_manager_api.json": want.API, "03_90_kee_route_manager_inbounds.json": want.Inbounds, "04_90_kee_route_manager_outbounds.json": want.Outbounds, "05_90_kee_route_manager_routing.json": want.Routing} {
					got, err := os.ReadFile(filepath.Join(c.Xray.ManagedDir, name))
					if err != nil || !bytes.Equal(got, data) {
						t.Fatalf("generated configuration %s not refreshed: %v", name, err)
					}
				}
				if err = m.ValidateReplay(context.Background(), proof); err != nil {
					t.Fatalf("replacement violates pre-recorded replay identities: %v", err)
				}
				actual, err := m.ActualState(context.Background())
				if err != nil || actual.Selection.Tag != desired.Selection.Tag {
					t.Fatalf("replacement lost desired selection: %+v %v", actual, err)
				}
			})
		}
	}
}

func TestApplyPoolKeepsUnchangedConfigurationDynamic(t *testing.T) {
	c, desired, runner, platform := reviewPoolFixture(t)
	c.Xray.DynamicAPI = true
	next := testNode()
	next.ID, next.Address = "replacement", "203.0.113.11"
	desired.Slots = append([]model.Slot(nil), desired.Slots...)
	desired.Slots[0].NodeID = next.ID
	desired.Nodes = map[string]model.Node{next.ID: next}
	if err := NewManager(c, runner, platform).ApplyPool(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	if platform.restarts != 0 {
		t.Fatal("ordinary dynamic pool update restarted Xray")
	}
	if len(runner.commands) != 2 || runner.commands[0][2] != "rmo" || runner.commands[1][2] != "ado" {
		t.Fatalf("ordinary pool update did not use dynamic API: %v", runner.commands)
	}
}

type reviewFailRestartPlatform struct{ restarts int }

func (p *reviewFailRestartPlatform) RestartXray(context.Context) error {
	p.restarts++
	if p.restarts == 1 {
		return fmt.Errorf("injected configuration restart failure")
	}
	return nil
}
func (p *reviewFailRestartPlatform) XrayRunning(context.Context) bool { return true }

func TestApplyPoolConfigurationRestartFailureRestoresAllFiles(t *testing.T) {
	c, desired, runner, _ := reviewPoolFixture(t)
	c.Xray.DynamicAPI = true
	m := NewManager(c, runner, &reviewFailRestartPlatform{})
	before := map[string][]byte{}
	for _, path := range m.snapshotPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = data
	}
	m.cfg.Xray.HealthProxyPort = 19888
	proof, err := m.PrepareReplay(context.Background(), desired, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ApplyPool(context.Background(), desired); err == nil {
		t.Fatal("restart failure accepted")
	}
	for path, data := range before {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("failed configuration replacement lost %s: %v", path, err)
		}
	}
	if err = m.ValidateReplay(context.Background(), proof); err != nil {
		t.Fatalf("rollback violates replay identities: %v", err)
	}
}
