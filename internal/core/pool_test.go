package core

import (
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"testing"
	"time"
)

func TestProviderDiversityIsSoft(t *testing.T) {
	c := config.Default()
	c.Pool.Size = 3
	c.Pool.ProviderDiversity.Enabled = true
	c.Pool.ProviderDiversity.MaxPerProvider = 1
	m := &Manager{cfg: c}
	nodes := []model.Node{{ID: "a1", Sources: []string{"a"}}, {ID: "a2", Sources: []string{"a"}}, {ID: "b1", Sources: []string{"b"}}}
	rs := []model.Measurement{{NodeID: "a1", Healthy: true, Score: 1}, {NodeID: "a2", Healthy: true, Score: 2}, {NodeID: "b1", Healthy: true, Score: 3}}
	out := m.selectPool(nodes, rs)
	if len(out) != 3 {
		t.Fatalf("expected pool fill, got %d", len(out))
	}
	if out[0].ID != "a1" || out[1].ID != "b1" {
		t.Fatalf("diversity order %#v", out)
	}
}
func TestAssignSlotsPreservesActiveIdentity(t *testing.T) {
	old := []model.Slot{{Index: 0, NodeID: "a"}, {Index: 1, NodeID: "b"}}
	nodes := []model.Node{{ID: "b", Label: "B"}, {ID: "c", Label: "C"}}
	ms := map[string]model.Measurement{"b": {NodeID: "b", Healthy: true, Score: 2, CheckedAt: time.Now()}, "c": {NodeID: "c", Healthy: true, Score: 1, CheckedAt: time.Now()}}
	out := assignSlots(old, nodes, ms, "slot-", 2)
	if out[1].NodeID != "b" {
		t.Fatalf("existing slot moved: %#v", out)
	}
}
