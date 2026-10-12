package amnezia

import (
	"context"
	"fmt"
	"testing"
)

func TestAWGRepeatedEnsureVerifiesLiveKernelState(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	probe := emptyReconcileKernel(id)
	rt.runner = probe
	ctx := context.Background()
	if _, _, err := rt.Ensure(ctx, id); err != nil {
		t.Fatal(err)
	}
	before := len(probe.calls)
	name := deviceName(id)
	table, err := tableNumber(id)
	if err != nil {
		t.Fatal(err)
	}
	probe.outputs["ip -j link show"] = fmt.Sprintf(`[{"ifname":%q}]`, name)
	probe.outputs["ip -j route show table "+table] = fmt.Sprintf(`[{"dst":"default","dev":%q}]`, name)
	probe.outputs["ip -j rule show"] = fmt.Sprintf(`[{"fwmark":%q,"table":%q}]`, table, table)
	if _, _, err := rt.Ensure(ctx, id); err != nil {
		t.Fatalf("verified live interface rejected: %v", err)
	}
	if len(probe.calls) != before {
		t.Fatalf("idempotent Ensure mutated verified kernel: %v", probe.calls[before:])
	}
	probe.outputs["ip -j route show table "+table] = "[]"
	if _, _, err := rt.Ensure(ctx, id); err == nil {
		t.Fatal("missing kernel route must not be reported as healthy")
	}
	if len(probe.calls) != before {
		t.Fatalf("drifted kernel was modified by Ensure: %v", probe.calls[before:])
	}
}

func TestAWGRepeatedEnsureRejectsKernelInspectionFailure(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	probe := emptyReconcileKernel(id)
	rt.runner = probe
	if _, _, err := rt.Ensure(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	probe.fail = "ip -j link show"
	if _, _, err := rt.Ensure(context.Background(), id); err == nil {
		t.Fatal("lost kernel inspection must not report a healthy interface")
	}
}
