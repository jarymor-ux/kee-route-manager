package amnezia

import (
	"context"
	"fmt"
	"testing"
)

func TestAWGEnsureRejectsPolicyRuleWithSeparateOverlappingMask(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	table, err := tableNumber(id)
	if err != nil {
		t.Fatal(err)
	}
	probe := &kernelProbeRunner{outputs: map[string]string{
		"ip -j link show":                 "[]",
		"ip -j route show table " + table: "[]",
		"ip -j rule show":                 `[{"priority":100,"fwmark":"0x0","fwmask":"0x0","table":"other"}]`,
	}}
	rt.runner = probe
	if _, _, err := rt.Ensure(context.Background(), id); err == nil {
		t.Fatal("rule matching every fwmark must reserve the requested mark")
	}
	if len(probe.calls) != 0 {
		t.Fatalf("masked rule collision changed host routing: %v", probe.calls)
	}
}

func TestAWGEnsureAllowsNonOverlappingMaskedPolicyRule(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	table, err := tableNumber(id)
	if err != nil {
		t.Fatal(err)
	}
	probe := &kernelProbeRunner{outputs: map[string]string{
		"ip -j link show":                 "[]",
		"ip -j route show table " + table: "[]",
		"ip -j rule show":                 fmt.Sprintf(`[{"priority":100,"fwmark":"0x%x","fwmask":"0xffffffff","table":"other"}]`, 0),
	}}
	rt.runner = probe
	if _, _, err := rt.Ensure(context.Background(), id); err != nil {
		t.Fatalf("nonoverlapping policy rule rejected: %v", err)
	}
}
