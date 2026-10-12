package amnezia

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Kernel snapshots are read-only. Test cases must never change host networking.
type kernelProbeRunner struct {
	fakeCommands
	outputs map[string]string
	fail    string
	queries []string
}

func (r *kernelProbeRunner) Inspect(_ context.Context, name string, args ...string) ([]byte, error) {
	query := name + " " + strings.Join(args, " ")
	r.queries = append(r.queries, query)
	if r.fail == query {
		return nil, errors.New("injected kernel inspection failure")
	}
	out, ok := r.outputs[query]
	if !ok {
		return nil, fmt.Errorf("unexpected kernel inspection: %s", query)
	}
	return []byte(out), nil
}

func TestAWGEnsureRefusesForeignKernelResourcesBeforeAnyMutation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		conflictKind string
	}{
		{name: "interface", conflictKind: "link"},
		{name: "routing table", conflictKind: "route"},
		{name: "policy mark", conflictKind: "mark"},
		{name: "policy table", conflictKind: "rule-table"},
		{name: "inspection error", conflictKind: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, id, _ := lifecycleFixture(t)
			name := deviceName(id)
			table, err := tableNumber(id)
			if err != nil {
				t.Fatal(err)
			}
			linkQ := "ip -j link show"
			routeQ := "ip -j route show table " + table
			ruleQ := "ip -j rule show"
			probe := &kernelProbeRunner{outputs: map[string]string{
				linkQ:  "[]",
				routeQ: "[]",
				ruleQ:  "[]",
			}}
			switch tc.conflictKind {
			case "link":
				probe.outputs[linkQ] = fmt.Sprintf(`[{"ifname":%q}]`, name)
			case "route":
				probe.outputs[routeQ] = `[{"dst":"default","dev":"foreign0"}]`
			case "mark":
				mark, err := strconv.ParseUint(table, 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				probe.outputs[ruleQ] = fmt.Sprintf(`[{"priority":100,"fwmark":"0x%x","table":"other"}]`, mark)
			case "rule-table":
				probe.outputs[ruleQ] = fmt.Sprintf(`[{"priority":100,"table":%q}]`, table)
			case "error":
				probe.fail = linkQ
			}
			rt.runner = probe
			if _, _, err := rt.Ensure(context.Background(), id); err == nil {
				t.Fatal("Ensure must fail closed on occupied or uninspectable kernel routing resources")
			}
			if len(probe.calls) != 0 {
				t.Fatalf("unsafe system command after conflict: %v", probe.calls)
			}
			if _, err := os.Lstat(filepath.Join(rt.dir, name+".conf")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("foreign conflict left a private runtime config: %v", err)
			}
			if len(probe.queries) == 0 {
				t.Fatal("Ensure did not inspect kernel state")
			}
		})
	}
}

func TestAWGEnsureAllowsEmptyKernelResources(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	table, err := tableNumber(id)
	if err != nil {
		t.Fatal(err)
	}
	probe := &kernelProbeRunner{outputs: map[string]string{
		"ip -j link show":                  "[]",
		"ip -j route show table " + table: "[]",
		"ip -j rule show":                  `[{"priority":32766,"table":"main"},{"priority":32767,"table":"default"}]`,
	}}
	rt.runner = probe
	if _, _, err := rt.Ensure(context.Background(), id); err != nil {
		t.Fatalf("unoccupied routing resources rejected: %v", err)
	}
	if len(probe.calls) != 3 {
		t.Fatalf("expected three startup commands, got %v", probe.calls)
	}
}
