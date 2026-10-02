package core

import (
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestRetainActiveNodeDuringPoolRefresh(t *testing.T) {
	selected := []model.Node{
		{ID: "new-a", Label: "A"},
		{ID: "new-b", Label: "B"},
	}
	all := map[string]model.Node{
		"new-a":      selected[0],
		"new-b":      selected[1],
		"active-old": {ID: "active-old", Label: "Active"},
	}
	retained := retainActiveNode(selected, "active-old", all, 2)
	if len(retained) != 2 {
		t.Fatalf("len = %d", len(retained))
	}
	if retained[0].ID != "new-a" || retained[1].ID != "active-old" {
		t.Fatalf("unexpected selection: %#v", retained)
	}
}

func TestRetainActiveNodeDoesNotDuplicate(t *testing.T) {
	selected := []model.Node{{ID: "active"}, {ID: "other"}}
	retained := retainActiveNode(selected, "active", map[string]model.Node{"active": selected[0]}, 2)
	if len(retained) != 2 || retained[0].ID != "active" || retained[1].ID != "other" {
		t.Fatalf("unexpected selection: %#v", retained)
	}
}
