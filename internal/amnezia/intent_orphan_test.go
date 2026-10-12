package amnezia

import (
	"context"
	"os"
	"testing"
)

func TestAWGReconcileReportsIntentWithoutRuntimeConfig(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	if err := rt.writeIntent(id, "starting"); err != nil {
		t.Fatal(err)
	}
	rt.runner = emptyReconcileKernel(id)
	if err := rt.Reconcile(context.Background()); err == nil {
		t.Fatal("unpaired lifecycle intent must be reported during startup")
	}
	if _, err := os.Stat(rt.intentPath(id)); err != nil {
		t.Fatalf("unpaired intent removed without proof of ownership: %v", err)
	}
	if _, _, err := rt.Ensure(context.Background(), id); err == nil {
		t.Fatal("unpaired intent must block Ensure")
	}
}
