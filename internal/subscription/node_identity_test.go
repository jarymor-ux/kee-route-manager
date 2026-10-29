package subscription

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestMergeRetainsNamesForEverySubscription(t *testing.T) {
	a, err := ParseVLESS(reality, "first")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseVLESS(strings.Replace(reality, "Node%20A", "Other%20name", 1), "second")
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseVLESS(strings.Replace(reality, "Node%20A", "Alias", 1), "first")
	if err != nil {
		t.Fatal(err)
	}
	merged := Merge([]model.Node{a, b, c}, 10)
	want := []model.NodeIdentity{{SourceID: "first", Name: "Alias"}, {SourceID: "first", Name: "Node A"}, {SourceID: "second", Name: "Other name"}}
	if len(merged) != 1 || merged[0].ID != a.ID || !reflect.DeepEqual(merged[0].SubscriptionNodes, want) {
		t.Fatalf("connection dedup discarded subscription identities: %+v", merged)
	}
	// Merging already cached/deduplicated nodes must not invent a name/source pair.
	again := Merge(append(merged, b), 10)
	if !reflect.DeepEqual(again[0].SubscriptionNodes, want) {
		t.Fatalf("repeat merge corrupted aliases: %+v", again[0].SubscriptionNodes)
	}
	merged[0].SubscriptionNodes[0].Name = "caller changed"
	if again[0].SubscriptionNodes[0].Name != "Alias" {
		t.Fatal("merge output shares mutable aliases with input")
	}
}
