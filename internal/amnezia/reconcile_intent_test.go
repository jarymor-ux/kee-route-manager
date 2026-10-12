package amnezia

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAWGReconcileClearsVerifiedAbsentCommittedIntent(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	probe := emptyReconcileKernel(id)
	rt.runner = probe
	if _, _, err := rt.Ensure(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	cold, err := NewRuntime(rt.store, rt.dir, emptyReconcileKernel(id))
	if err != nil {
		t.Fatal(err)
	}
	if err := cold.Reconcile(context.Background()); err != nil {
		t.Fatalf("reboot left no network resources: %v", err)
	}
	for _, suffix := range []string{".conf", ".intent.json"} {
		path := filepath.Join(rt.dir, deviceName(id)+suffix)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("reboot cleanup retained %s: %v", path, err)
		}
	}
	if _, _, err := cold.Ensure(context.Background(), id); err != nil {
		t.Fatalf("new boot could not restart recovered tunnel: %v", err)
	}
}

func TestAWGReconcileDoesNotTrustCorruptLifecycleIntent(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	path := createReconcileOrphan(t, rt, id)
	intentPath := filepath.Join(rt.dir, deviceName(id)+".intent.json")
	if err := os.WriteFile(intentPath, []byte(`{"profile_id":"forged","phase":"active"}`), 0600); err != nil {
		t.Fatal(err)
	}
	rt.runner = emptyReconcileKernel(id)
	if err := rt.Reconcile(context.Background()); err == nil {
		t.Fatal("corrupt or forged journal must prevent cleanup")
	}
	for _, path := range []string{path, intentPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("forged intent lost evidence %s: %v", path, err)
		}
	}
}
