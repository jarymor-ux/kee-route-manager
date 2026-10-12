package amnezia

import (
	"context"
	"os"
	"testing"
)

func TestAWGStopAllReportsUnreconciledCrashOrphans(t *testing.T) {
	rt, id, runner := lifecycleFixture(t)
	path := createReconcileOrphan(t, rt, id)
	if err := rt.StopAll(context.Background()); err == nil {
		t.Fatal("StopAll must report leftover crash-owned runtime evidence")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("StopAll removed ambiguous recovery file: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("StopAll executed commands for an unowned orphan: %v", runner.calls)
	}
}
