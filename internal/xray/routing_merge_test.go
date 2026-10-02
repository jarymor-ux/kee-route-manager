package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

func writeRoutingFixture(t *testing.T, path string, root map[string]any) {
	t.Helper()
	if err := os.WriteFile(path, pretty(root), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingRestorePreservesUnrelatedBalancerEdits(t *testing.T) {
	for _, change := range []string{"retain", "remove", "add"} {
		t.Run(change, func(t *testing.T) {
			c := reproConfig(t)
			c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "05_routing.json")
			original := map[string]any{"routing": map[string]any{
				"domainStrategy": "AsIs",
				"balancers":      []any{map[string]any{"tag": "user", "selector": []any{"user-outbound"}}},
				"rules":          []any{map[string]any{"type": "field", "inboundTag": []any{"redirect"}, "outboundTag": "vless-reality"}},
			}}
			writeRoutingFixture(t, c.Xray.BaseRoutingFile, original)
			m := NewManager(c, &recordingRunner{}, &fakePlatform{})
			snapshot, err := m.snapshot()
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(snapshot)
			if err = m.preserveOriginal(snapshot); err != nil {
				t.Fatal(err)
			}
			if err = m.patchBaseRoute(c.Xray.BaseRoutingFile); err != nil {
				t.Fatal(err)
			}
			first, _ := os.ReadFile(c.Xray.BaseRoutingFile)
			if err = m.patchBaseRoute(c.Xray.BaseRoutingFile); err != nil {
				t.Fatal(err)
			}
			second, _ := os.ReadFile(c.Xray.BaseRoutingFile)
			if !bytes.Equal(first, second) {
				t.Fatal("repeated adoption duplicates routing additions")
			}
			current, err := readRoutingRoot(c.Xray.BaseRoutingFile)
			if err != nil {
				t.Fatal(err)
			}
			routing := current["routing"].(map[string]any)
			expected := original["routing"].(map[string]any)
			balancers := routing["balancers"].([]any)
			switch change {
			case "remove":
				routing["balancers"] = balancers[1:]
				expected["balancers"] = []any{}
			case "add":
				added := map[string]any{"tag": "user-new", "selector": []any{"new-outbound"}}
				routing["balancers"] = append(balancers, added)
				expected["balancers"] = append(expected["balancers"].([]any), added)
			}
			routing["domainStrategy"], expected["domainStrategy"] = "IPIfNonMatch", "IPIfNonMatch"
			current["operatorValue"], original["operatorValue"] = json.Number("9007199254740993"), json.Number("9007199254740993")
			writeRoutingFixture(t, c.Xray.BaseRoutingFile, current)
			if err = m.reverseBaseRoute(filepath.Join(snapshot, "4"), c.Xray.BaseRoutingFile); err != nil {
				t.Fatal(err)
			}
			restored, _ := os.ReadFile(c.Xray.BaseRoutingFile)
			if !bytes.Equal(restored, pretty(original)) {
				t.Fatalf("restore lost user edits:\ngot %s\nwant %s", restored, pretty(original))
			}
			if err = m.reverseBaseRoute(filepath.Join(snapshot, "4"), c.Xray.BaseRoutingFile); err != nil {
				t.Fatalf("restore not idempotent: %v", err)
			}
		})
	}
}

func TestRoutingAdoptionRefusesOriginalOwnershipCollisions(t *testing.T) {
	for _, collision := range []string{"balancer", "api", "health", "probe"} {
		t.Run(collision, func(t *testing.T) {
			c := reproConfig(t)
			c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "05_routing.json")
			routing := map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"redirect"}, "outboundTag": "vless-reality"}}}
			generated := managedRouting(c)
			if collision == "balancer" {
				routing["balancers"] = generated["balancers"]
			} else {
				index := map[string]int{"api": 0, "health": 1, "probe": 2}[collision]
				routing["rules"] = append(routing["rules"].([]any), generated["rules"].([]any)[index])
			}
			root := map[string]any{"routing": routing}
			writeRoutingFixture(t, c.Xray.BaseRoutingFile, root)
			m := NewManager(c, &recordingRunner{}, &fakePlatform{})
			if err := m.patchBaseRoute(c.Xray.BaseRoutingFile); err == nil || !strings.Contains(err.Error(), "conflicts") {
				t.Fatalf("adopted preexisting user content: %v", err)
			}
			data, _ := os.ReadFile(c.Xray.BaseRoutingFile)
			if !bytes.Equal(data, pretty(root)) {
				t.Fatal("rejected adoption modified base file")
			}
		})
	}
}

func TestRoutingRejectsAnotherAuthoritativeSectionBeforeReplay(t *testing.T) {
	for _, baseName := range []string{"", "05_routing.json"} {
		for _, extraName := range []string{"01_user.json", "99_user.json"} {
			t.Run(baseName+"/"+extraName, func(t *testing.T) {
				c := reproConfig(t)
				if baseName != "" {
					c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, baseName)
					writeRoutingFixture(t, c.Xray.BaseRoutingFile, map[string]any{"routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"redirect"}, "outboundTag": "vless-reality"}}}})
				}
				writeRoutingFixture(t, filepath.Join(c.Xray.ConfigDir, extraName), map[string]any{"routing": map[string]any{"rules": []any{}}})
				m := NewManager(c, &recordingRunner{}, &fakePlatform{})
				managed, err := BuildManaged(c, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = m.validateCandidate(context.Background(), managed); err == nil || !strings.Contains(err.Error(), "outside configured base") {
					t.Fatalf("candidate silently overwrote foreign routing: %v", err)
				}
				if _, err = m.PrepareReplay(context.Background(), tunnel.DesiredPool{}, false); err == nil || !strings.Contains(err.Error(), "outside configured base") {
					t.Fatalf("journal plan accepted ambiguous routing: %v", err)
				}
				if err = m.checkAdoptedRouting(); err == nil || !strings.Contains(err.Error(), "outside configured base") {
					t.Fatalf("runtime failed to detect foreign override: %v", err)
				}
			})
		}
	}
}

func TestActualStateDetectsManagedBaseRoutingDrift(t *testing.T) {
	for _, mutation := range []string{"api-target", "health-missing", "probe-duplicate", "balancer-selector", "balancer-missing", "rule-order", "all-missing"} {
		t.Run(mutation, func(t *testing.T) {
			m, _, _ := snapshotAliasFixture(t)
			root, err := readRoutingRoot(m.cfg.Xray.BaseRoutingFile)
			if err != nil {
				t.Fatal(err)
			}
			routing := root["routing"].(map[string]any)
			rules := routing["rules"].([]any)
			switch mutation {
			case "api-target":
				rules[0].(map[string]any)["outboundTag"] = "user-direct"
			case "health-missing":
				rules = append(rules[:1], rules[2:]...)
			case "probe-duplicate":
				rules = append(rules, rules[2])
			case "balancer-selector":
				routing["balancers"].([]any)[0].(map[string]any)["selector"] = []any{m.cfg.Xray.ManagedDirectTag}
			case "balancer-missing":
				delete(routing, "balancers")
			case "rule-order":
				rules[0], rules[len(rules)-1] = rules[len(rules)-1], rules[0]
			case "all-missing":
				rules = rules[m.cfg.Pool.Size+2:]
				delete(routing, "balancers")
			}
			routing["rules"] = rules
			writeRoutingFixture(t, m.cfg.Xray.BaseRoutingFile, root)
			actual, err := m.ActualState(context.Background())
			if err != nil || actual.Drift == "" {
				t.Fatalf("managed routing drift escaped detection: %+v %v", actual, err)
			}
		})
	}
}

func TestApplyPoolInstallsChangedBaseRoutingAndMatchesReplay(t *testing.T) {
	c := reproConfig(t)
	c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "05_routing.json")
	c.Xray.DynamicAPI = true
	original := map[string]any{"routing": map[string]any{"rules": []any{
		map[string]any{"type": "field", "inboundTag": []string{"redirect"}, "outboundTag": "vless-reality"},
		map[string]any{"type": "field", "inboundTag": []string{"tproxy"}, "outboundTag": "other-vpn"},
	}}}
	writeRoutingFixture(t, c.Xray.BaseRoutingFile, original)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c.Xray.APIAddress = listener.Addr().String()
	runner, platform := &recordingRunner{}, &fakePlatform{}
	m := NewManager(c, runner, platform)
	node := testNode()
	desired := tunnel.DesiredPool{Slots: []model.Slot{{Index: 0, NodeID: node.ID, Tag: c.Xray.SlotTagPrefix + "0"}}, Nodes: map[string]model.Node{node.ID: node}, Selection: tunnel.Selection{Tag: c.Xray.SlotTagPrefix + "0"}}
	ctx := context.Background()
	if err = m.Bootstrap(ctx, desired); err != nil {
		t.Fatal(err)
	}
	// This change modifies only the authoritative base, with exactly the same
	// generated API, inbound, outbound and placeholder routing fragments.
	c.Xray.Route.ReplaceOutboundTags = append(c.Xray.Route.ReplaceOutboundTags, "other-vpn")
	m = NewManager(c, runner, platform)
	proof, err := m.PrepareReplay(ctx, desired, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof) != 5 || proof[c.Xray.BaseRoutingFile].Before == proof[c.Xray.BaseRoutingFile].After {
		t.Fatal("changed authoritative routing missing from five-file replay plan")
	}
	if err = m.ApplyPool(ctx, desired); err != nil {
		t.Fatal(err)
	}
	if platform.restarts != 2 {
		t.Fatal("base-only routing change did not restart Xray")
	}
	assertAfter := func(proof tunnel.ReplayProof) {
		t.Helper()
		for path, expected := range proof {
			actual, err := fileIdentity(path)
			if err != nil || actual != expected.After {
				t.Fatalf("committed file differs from journal After proof: %s %v", filepath.Base(path), err)
			}
		}
	}
	assertAfter(proof)
	actual, err := m.ActualState(ctx)
	if err != nil || actual.Drift != "" {
		t.Fatalf("base-only adoption reports drift: %+v %v", actual, err)
	}
	proof, err = m.PrepareReplay(ctx, desired, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.RestoreOriginal(ctx); err != nil {
		t.Fatal(err)
	}
	assertAfter(proof)
	restored, _ := os.ReadFile(c.Xray.BaseRoutingFile)
	if !bytes.Equal(restored, pretty(original)) {
		t.Fatal("newly adopted target failed to restore")
	}
}

func TestAdoptionRequiresMatchingUserRule(t *testing.T) {
	c := reproConfig(t)
	c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "05_routing.json")
	c.Xray.Route.InboundTags = []string{"krm-health"}
	root := map[string]any{"routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"redirect"}, "outboundTag": "vless-reality"}}}}
	writeRoutingFixture(t, c.Xray.BaseRoutingFile, root)
	m := NewManager(c, &recordingRunner{}, &fakePlatform{})
	if err := m.patchBaseRoute(c.Xray.BaseRoutingFile); err == nil || !strings.Contains(err.Error(), "no matching") {
		t.Fatalf("generated health rule satisfied user-route adoption: %v", err)
	}
	data, _ := os.ReadFile(c.Xray.BaseRoutingFile)
	if !bytes.Equal(data, pretty(root)) {
		t.Fatal("failed adoption modified original file")
	}
}
