package amnezia

import (
	"context"
	"fmt"
	"testing"
)

// A managed in-memory flag does not authorize deleting a route or rule after
// another actor has replaced it. The Stop preflight is strictly read-only.
func TestAWGStopRefusesDriftedKernelWithoutNetworkMutation(t *testing.T) {
	for _, scenario := range []string{"missing-interface", "foreign-route", "missing-rule", "inspection-error"} {
		t.Run(scenario, func(t *testing.T) {
			rt, id, _ := lifecycleFixture(t)
			probe := emptyReconcileKernel(id)
			rt.runner = probe
			ctx := context.Background()
			if _, _, err := rt.Ensure(ctx, id); err != nil {
				t.Fatal(err)
			}
			name := deviceName(id)
			table, err := tableNumber(id)
			if err != nil {
				t.Fatal(err)
			}
			probe.outputs["ip -j link show"] = fmt.Sprintf(`[{"ifname":%q}]`, name)
			probe.outputs["ip -j route show table "+table] = fmt.Sprintf(`[{"dst":"default","dev":%q}]`, name)
			probe.outputs["ip -j rule show"] = fmt.Sprintf(`[{"fwmark":%q,"table":%q}]`, table, table)
			switch scenario {
			case "missing-interface":
				probe.outputs["ip -j link show"] = "[]"
			case "foreign-route":
				probe.outputs["ip -j route show table "+table] = `[{"dst":"default","dev":"foreign0"}]`
			case "missing-rule":
				probe.outputs["ip -j rule show"] = "[]"
			case "inspection-error":
				probe.fail = "ip -j rule show"
			}
			before := len(probe.calls)
			if err := rt.Stop(ctx, id); err == nil {
				t.Fatal("Stop must reject drifted or unobservable kernel resources")
			}
			if len(probe.calls) != before {
				t.Fatalf("Stop modified potentially foreign network state: %v", probe.calls[before:])
			}
			if !rt.active[id] {
				t.Fatal("failed Stop must retain active state")
			}
		})
	}
}
