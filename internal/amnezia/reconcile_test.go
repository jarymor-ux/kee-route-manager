package amnezia

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func createReconcileOrphan(t *testing.T, rt *Runtime, id string) string {
	t.Helper()
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
	return path
}

func emptyReconcileKernel(id string) *kernelProbeRunner {
	table, _ := tableNumber(id)
	return &kernelProbeRunner{outputs: map[string]string{
		"ip -j link show":                 "[]",
		"ip -j route show table " + table: "[]",
		"ip -j rule show":                 "[]",
	}}
}

func TestAWGReconcileClearsOnlyVerifiedAbsentOrphan(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	path := createReconcileOrphan(t, rt, id)
	probe := emptyReconcileKernel(id)
	rt.runner = probe

	if err := rt.Reconcile(context.Background()); err != nil {
		t.Fatalf("kernel-empty orphan should be recoverable: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed absent orphan still blocks restart: %v", err)
	}
	if len(probe.calls) != 0 {
		t.Fatalf("reconciliation mutated networking: %v", probe.calls)
	}
	if _, _, err := rt.Ensure(context.Background(), id); err != nil {
		t.Fatalf("Ensure after verified empty-kernel reconciliation: %v", err)
	}
}

func TestAWGReconcileRetainsOrphanWhenKernelContainsMatchingInterface(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	path := createReconcileOrphan(t, rt, id)
	probe := emptyReconcileKernel(id)
	probe.outputs["ip -j link show"] = `[{"ifname":"` + deviceName(id) + `"}]`
	rt.runner = probe
	if err := rt.Reconcile(context.Background()); err == nil {
		t.Fatal("live interface must not be discarded during reconciliation")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live interface lost recovery file: %v", err)
	}
	if len(probe.calls) != 0 {
		t.Fatalf("reconciliation changed live interface: %v", probe.calls)
	}
}

func TestAWGReconcileRefusesModifiedOrUnownedRecoveryFile(t *testing.T) {
	for _, mutation := range []string{"modified", "unknown"} {
		t.Run(mutation, func(t *testing.T) {
			rt, id, _ := lifecycleFixture(t)
			path := createReconcileOrphan(t, rt, id)
			probe := emptyReconcileKernel(id)
			rt.runner = probe
			if mutation == "modified" {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, bytes.Replace(raw, []byte("Table = off"), []byte("Table = auto"), 1), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(path, filepath.Join(rt.dir, "krmawgdeadbeef.conf")); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(rt.dir, "krmawgdeadbeef.conf")
			}
			if err := rt.Reconcile(context.Background()); err == nil {
				t.Fatal("unknown or modified recovery evidence must fail closed")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("untrusted evidence removed: %v", err)
			}
			if len(probe.calls) != 0 {
				t.Fatalf("untrusted evidence changed networking: %v", probe.calls)
			}
		})
	}
}

func TestAWGReconcileFailsClosedWhenKernelInspectionUnavailable(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	path := createReconcileOrphan(t, rt, id)
	probe := emptyReconcileKernel(id)
	probe.fail = "ip -j route show table " + mustTable(t, id)
	rt.runner = probe
	if err := rt.Reconcile(context.Background()); err == nil {
		t.Fatal("unavailable kernel inspection must not delete evidence")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("recovery evidence deleted after inspection failure: %v", err)
	}
}

func mustTable(t *testing.T, id string) string {
	t.Helper()
	table, err := tableNumber(id)
	if err != nil {
		t.Fatal(err)
	}
	return table
}
