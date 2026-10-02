package store

import (
	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"testing"
)

func TestStateNodesEvents(t *testing.T) {
	d := t.TempDir()
	s, e := New(d, d, model.NewState("v", "slot-", 2))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReplaceNodes([]model.Node{{ID: "b"}, {ID: "a"}}); e != nil {
		t.Fatal(e)
	}
	if xs := s.Nodes(); len(xs) != 2 || xs[0].ID != "a" {
		t.Fatal("nodes not sorted")
	}
	ev, e := s.Append(event.Event{Type: "test", Message: "ok"})
	if e != nil || ev.Sequence != 1 {
		t.Fatal(e)
	}
	if len(s.Events(0, 10)) != 1 {
		t.Fatal("event missing")
	}
}
