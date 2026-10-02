package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func reproConfig(t *testing.T) config.Config {
	c := config.Default()
	d := t.TempDir()
	c.Paths.RunDir = filepath.Join(d, "run")
	c.Paths.StateDir = filepath.Join(d, "state")
	c.Xray.ConfigDir = filepath.Join(d, "configs")
	c.Xray.ManagedDir = c.Xray.ConfigDir
	c.Xray.BaseRoutingFile = ""
	c.Pool.Size = 2
	for _, p := range []string{c.Paths.RunDir, c.Paths.StateDir, c.Xray.ConfigDir} {
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	c.Xray.Binary = filepath.Join(d, "fake-xray")
	if e := os.WriteFile(c.Xray.Binary, []byte("#!/bin/sh\nexit 0\n"), 0700); e != nil {
		t.Fatal(e)
	}
	return c
}
func TestRegressionDuplicateRestore(t *testing.T) {
	for _, duplicate := range []bool{true, false} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			c := reproConfig(t)
			c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "base.json")
			c.Xray.Route.ReplaceOutboundTags = []string{"vless-reality", "user-direct"}
			second := map[string]any{"type": "field", "inboundTag": []string{"redirect"}, "outboundTag": "user-direct"}
			if !duplicate {
				second["inboundTag"] = []string{"other"}
			}
			root := map[string]any{"routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"redirect"}, "outboundTag": "vless-reality"}, second}}}
			original := filepath.Join(c.Paths.StateDir, "original.json")
			if e := os.WriteFile(original, pretty(root), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(c.Xray.BaseRoutingFile, pretty(root), 0600); e != nil {
				t.Fatal(e)
			}
			m := NewManager(c, &recordingRunner{}, &fakePlatform{})
			snap, e := m.snapshot()
			if e != nil {
				t.Fatal(e)
			}
			if e = m.preserveOriginal(snap); e != nil {
				t.Fatal(e)
			}
			if e = m.patchBaseRoute(c.Xray.BaseRoutingFile); e != nil {
				t.Fatal(e)
			}
			node := testNode()
			slots := []model.Slot{{Index: 0, NodeID: node.ID}}
			managed, e := BuildManaged(c, slots, map[string]model.Node{node.ID: node})
			if e != nil {
				t.Fatal(e)
			}
			managed.Outbounds, e = selectOutbound(managed.Outbounds, c.Xray.SlotTagPrefix+"0")
			if e != nil {
				t.Fatal(e)
			}
			if e = writeManaged(c.Xray.ManagedDir, managed); e != nil {
				t.Fatal(e)
			}
			actual, e := m.ActualState(context.Background())
			if e != nil || actual.Drift != "" {
				t.Fatalf("upstream observation rejects fixture: %v %#v", e, actual)
			}
			t.Logf("pre-restore configured=%t drift=%q", actual.Configured, actual.Drift)
			if e = m.RestoreOriginal(context.Background()); e != nil {
				t.Fatal(e)
			}
			b, _ := os.ReadFile(c.Xray.BaseRoutingFile)
			var after map[string]any
			json.Unmarshal(b, &after)
			rule := after["routing"].(map[string]any)["rules"].([]any)[0].(map[string]any)
			got := rule["outboundTag"]
			t.Logf("duplicate=%t restored first outbound=%v", duplicate, got)
			expected := "vless-reality"
			if got != expected {
				t.Fatalf("unexpected observation %v", got)
			}
			// Both duplicate targets and their order must round-trip exactly.
			if string(pretty(after)) != string(pretty(root)) {
				t.Fatal("restore changed original rule sequence")
			}
			if e = m.reverseBaseRoute(original, c.Xray.BaseRoutingFile); e != nil {
				t.Fatal("restore is not idempotent", e)
			}
		})
	}
}

type reproRunner struct {
	runtime map[string]map[string]any
	fail    bool
}

func (r *reproRunner) Run(_ context.Context, cmd []string) ([]byte, error) {
	if cmd[2] == "rmo" {
		delete(r.runtime, cmd[4])
		return nil, nil
	}
	var root struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	b, _ := os.ReadFile(cmd[4])
	json.Unmarshal(b, &root)
	out := root.Outbounds[0]
	if r.fail {
		r.fail = false
		return nil, fmt.Errorf("injected ado failure")
	}
	r.runtime[out["tag"].(string)] = out
	return nil, nil
}
func TestRegressionPoolRollback(t *testing.T) {
	for _, retainOld := range []bool{false, true} {
		t.Run(fmt.Sprint(retainOld), func(t *testing.T) {
			c := reproConfig(t)
			c.Xray.DynamicAPI = true
			active := testNode()
			active.ID = "active"
			old := testNode()
			old.ID = "evicted"
			next := testNode()
			next.ID = "new"
			previous := []model.Slot{{Index: 0, Tag: c.Xray.SlotTagPrefix + "0", NodeID: active.ID}, {Index: 1, Tag: c.Xray.SlotTagPrefix + "1", NodeID: old.ID}}
			slots := append([]model.Slot(nil), previous...)
			slots[1].NodeID = next.ID
			nodes := map[string]model.Node{active.ID: active, next.ID: next}
			if retainOld {
				nodes[old.ID] = old
			}
			oldOut, _ := Outbound(old, previous[1].Tag)
			previousManaged, err := BuildManaged(c, previous, map[string]model.Node{active.ID: active, old.ID: old})
			if err != nil {
				t.Fatal(err)
			}
			previousManaged.Outbounds, err = selectOutbound(previousManaged.Outbounds, previous[0].Tag)
			if err != nil {
				t.Fatal(err)
			}
			var persisted map[string]any
			if err = json.Unmarshal(previousManaged.Outbounds, &persisted); err != nil {
				t.Fatal(err)
			}
			for _, raw := range persisted["outbounds"].([]any) {
				out := raw.(map[string]any)
				if out["tag"] == previous[1].Tag {
					out["sendThrough"] = "192.0.2.99"
					oldOut = out
				}
			}
			previousManaged.Outbounds = pretty(persisted)
			if err = writeManaged(c.Xray.ManagedDir, previousManaged); err != nil {
				t.Fatal(err)
			}
			r := &reproRunner{runtime: map[string]map[string]any{previous[1].Tag: oldOut}, fail: true}
			m := NewManager(c, r, &fakePlatform{})
			e := m.ApplyPool(context.Background(), tunnel.DesiredPool{Previous: previous, Slots: slots, Nodes: nodes, ActiveSlot: 0, Selection: tunnel.Selection{Tag: slots[0].Tag}})
			if e == nil {
				t.Fatal("expected failure")
			}
			got := r.runtime[previous[1].Tag]["protocol"]
			t.Logf("retainOld=%t error=%v runtime slot1 protocol=%v", retainOld, e, got)
			expected := "vless"
			if got != expected {
				t.Fatalf("unexpected observation %v", got)
			}
			if !reflect.DeepEqual(r.runtime[previous[1].Tag], oldOut) {
				t.Fatal("rollback lost previous outbound fields")
			}
		})
	}
}

func TestRegressionRestoreRefusesDuplicateMultiplicityDrift(t *testing.T) {
	for _, count := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			c := reproConfig(t)
			c.Xray.Route.ReplaceOutboundTags = []string{"old-a", "old-b"}
			rule := func(tag string) map[string]any {
				return map[string]any{"type": "field", "inboundTag": []string{"redirect"}, "outboundTag": tag}
			}
			original := map[string]any{"routing": map[string]any{"rules": []any{rule("old-a"), rule("old-b")}}}
			originalPath := filepath.Join(c.Paths.StateDir, "original.json")
			currentPath := filepath.Join(c.Xray.ConfigDir, "base.json")
			if err := os.WriteFile(originalPath, pretty(original), 0600); err != nil {
				t.Fatal(err)
			}
			rules := []any{}
			for i := 0; i < count; i++ {
				r := rule("")
				delete(r, "outboundTag")
				r["balancerTag"] = c.Xray.BalancerTag
				rules = append(rules, r)
			}
			before := pretty(map[string]any{"routing": map[string]any{"rules": rules}})
			if err := os.WriteFile(currentPath, before, 0600); err != nil {
				t.Fatal(err)
			}
			m := NewManager(c, &recordingRunner{}, &fakePlatform{})
			err := m.reverseBaseRoute(originalPath, currentPath)
			if count == 2 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("ambiguous duplicate cardinality accepted")
			}
			after, err := os.ReadFile(currentPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("refused restore mutated file")
			}
		})
	}
}
