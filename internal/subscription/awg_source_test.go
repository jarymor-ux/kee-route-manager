package subscription

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/amnezia"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"strings"
	"testing"
)

func TestImportedAWGProfileJoinsSubscriptionCandidatesWithoutSecretLeak(t *testing.T) {
	root := t.TempDir()
	profiles, err := amnezia.NewProfileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	raw := `[Interface]
PrivateKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=
Address = 10.66.66.2/32
Jc = 5
Jmin = 20
Jmax = 50
[Peer]
PublicKey = AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=
Endpoint = vpn.example.org:51820
AllowedIPs = 0.0.0.0/0
`
	item, err := profiles.Import("Home VPN", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := config.Default().Subscriptions
	c.Sources = []config.Source{}
	fetcher := New(c, nil, t.TempDir())
	fetcher.UseAWGProfiles(profiles)
	result := fetcher.FetchAll(context.Background(), nil, true)
	if len(result.Nodes) != 1 {
		t.Fatalf("AWG node not in fetch candidates: %+v", result)
	}
	node := result.Nodes[0]
	if node.Protocol != "amneziawg" || node.AWGProfileID != item.ID || node.AWGLocalIPv4 != "10.66.66.2" {
		t.Fatalf("wrong imported AWG node %+v", node)
	}
	if strings.Contains(node.Label, "AQEBA") || strings.Contains(node.Address, "AQEBA") {
		t.Fatal("private key leaked in node presentation")
	}
}
