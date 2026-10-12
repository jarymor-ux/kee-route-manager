package amnezia

import (
	"context"
	"testing"
)

func TestAWGEnsureDoesNotClaimHealthyWhileStopRollbackPending(t *testing.T) {
	rt, id, runner := lifecycleFixture(t)
	ctx := context.Background()
	if _, _, err := rt.Ensure(ctx, id); err != nil {
		t.Fatal(err)
	}
	runner.fail[5] = true // route deletion fails after rule deletion
	if err := rt.Stop(ctx, id); err == nil {
		t.Fatal("expected partial stop failure")
	}
	before := len(runner.calls)
	if _, _, err := rt.Ensure(ctx, id); err == nil {
		t.Fatal("pending stopping intent must not report successful Ensure")
	}
	if len(runner.calls) != before {
		t.Fatalf("pending stopping intent caused unexpected kernel mutations: %v", runner.calls[before:])
	}
	delete(runner.fail, 5)
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatalf("retry of Stop should clean up a restored interface: %v", err)
	}
}
