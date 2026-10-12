package amnezia

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAWGOrphanedRuntimeStateCannotBeSilentlyStoppedOrDeleted(t *testing.T) {
	rt, id, runner := lifecycleFixture(t)
	raw, err := rt.store.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	safe, err := isolatedConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rt.dir, deviceName(id)+".conf")
	if err := os.WriteFile(path, safe, 0600); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(context.Background(), id); err == nil {
		t.Fatal("Stop silently ignored an orphaned interface")
	}
	if err := rt.DeleteProfile(context.Background(), id); err == nil {
		t.Fatal("DeleteProfile removed recovery identity for an orphaned interface")
	}
	if _, err := rt.store.Read(id); err != nil {
		t.Fatalf("orphaned interface lost stored profile: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("orphaned runtime was mutated without reconciliation: %v", runner.calls)
	}
}
