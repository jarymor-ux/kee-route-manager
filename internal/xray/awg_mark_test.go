package xray

import (
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestAWGOutboundMarkTracksProfileRoutingTable(t *testing.T) {
	one := model.Node{Protocol: "amneziawg", AWGProfileID: "0123456789abcdef0123456789abcdef", AWGLocalIPv4: "10.66.66.2"}
	two := model.Node{Protocol: "amneziawg", AWGProfileID: "fedcba9876543210fedcba9876543210", AWGLocalIPv4: "10.66.66.2"}
	outA, err := Outbound(one, "slot-a")
	if err != nil {
		t.Fatal(err)
	}
	outB, err := Outbound(two, "slot-b")
	if err != nil {
		t.Fatal(err)
	}
	sockA := outA["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
	sockB := outB["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
	if sockA["mark"] == nil || sockB["mark"] == nil || sockA["mark"] == sockB["mark"] {
		t.Fatalf("identical client addresses must not share fwmark: %+v %+v", sockA, sockB)
	}
	if sockA["mark"] != uint32(274565) {
		t.Fatalf("unexpected fwmark for known identity: %+v", sockA)
	}
}
