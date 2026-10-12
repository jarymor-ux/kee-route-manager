package amnezia

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAWGRefusesActiveRoutingMarkCollisionBeforeSystemChanges(t *testing.T) {
	root := t.TempDir()
	store, err := NewProfileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Import("target", []byte(testAWGConfig))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeCommands{}
	rt, err := NewRuntime(store, filepath.Join(root, "runtime"), runner)
	if err != nil {
		t.Fatal(err)
	}
	replacement := "0"
	if item.ID[len(item.ID)-1] == '0' {
		replacement = "1"
	}
	conflictingID := item.ID[:len(item.ID)-1] + replacement
	if conflictingID == item.ID {
		t.Fatal("collision test fixture is not distinct")
	}
	rt.active[conflictingID] = true
	if _, _, err := rt.Ensure(context.Background(), item.ID); err == nil {
		t.Fatal("two profiles with the same fwmark/interface prefix must not start")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("conflicting profile mutated the system: %v", runner.calls)
	}
}
