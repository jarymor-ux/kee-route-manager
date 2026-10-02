package xray

import (
	"context"
	"encoding/json"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func testNode() model.Node {
	return model.Node{ID: "n", Protocol: "vless", Address: "example.com", Port: 443, UUID: "11111111-1111-1111-1111-111111111111", Encryption: "none", Network: "tcp", Security: "reality", ServerName: "example.com", PublicKey: "abc", Fingerprint: "chrome"}
}
func TestOutboundReality(t *testing.T) {
	v, e := Outbound(testNode(), "slot-0")
	if e != nil {
		t.Fatal(e)
	}
	if v["tag"] != "slot-0" {
		t.Fatal("tag")
	}
}
func TestPatchBaseRoute(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "route.json")
	raw := `{"routing":{"rules":[{"type":"field","inboundTag":["redirect","tproxy"],"outboundTag":"vless-reality"}]}}`
	if e := os.WriteFile(p, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	c := config.Default()
	c.Xray.BaseRoutingFile = p
	m := &Manager{cfg: c}
	if e := m.patchBaseRoute(p); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if string(b) == raw {
		t.Fatal("route not changed")
	}
	_ = context.Background()
}

func TestManagedBalancerDoesNotSelectDirectByDefault(t *testing.T) {
	c := config.Default()
	c.Pool.Size = 2
	node := testNode()
	slots := []model.Slot{{Index: 0, Tag: c.Xray.SlotTagPrefix + "0", NodeID: node.ID}}
	managed, err := BuildManaged(c, slots, map[string]model.Node{node.ID: node})
	if err != nil {
		t.Fatal(err)
	}
	var routing struct {
		Routing struct {
			Balancers []struct {
				Selector []string `json:"selector"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(managed.Routing, &routing); err != nil {
		t.Fatal(err)
	}
	if len(routing.Routing.Balancers) != 1 || len(routing.Routing.Balancers[0].Selector) != 1 || routing.Routing.Balancers[0].Selector[0] != c.Xray.SlotTagPrefix+"0" {
		t.Fatalf("unexpected selectors: %#v", routing.Routing.Balancers)
	}
	var outbounds struct {
		Outbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(managed.Outbounds, &outbounds); err != nil {
		t.Fatal(err)
	}
	for _, outbound := range outbounds.Outbounds {
		if outbound.Tag == c.Xray.ManagedDirectTag {
			continue
		}
		if outbound.Tag == c.Xray.SlotTagPrefix+"1" && outbound.Protocol != "blackhole" {
			t.Fatalf("empty slot %s must be blackhole, got %s", outbound.Tag, outbound.Protocol)
		}
	}
}
