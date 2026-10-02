package xray

import (
	"encoding/json"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"testing"
)

func TestManagedRoutingNeverRandomlySelectsEmptySlots(t *testing.T) {
	c := config.Default()
	slots := model.NewState("v", c.Xray.SlotTagPrefix, c.Pool.Size).Pool
	managed, err := BuildManaged(c, slots, map[string]model.Node{})
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Routing struct {
			Balancers []struct {
				Selector []string `json:"selector"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if err = json.Unmarshal(managed.Routing, &root); err != nil {
		t.Fatal(err)
	}
	if len(root.Routing.Balancers) != 1 || len(root.Routing.Balancers[0].Selector) != 1 || root.Routing.Balancers[0].Selector[0] == c.Xray.SlotTagPrefix {
		t.Fatal("balancer can select unfilled slots after restart")
	}
}
