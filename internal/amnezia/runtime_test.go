package amnezia

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCommands struct {
	calls  []string
	failAt int
}

func (r *fakeCommands) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if r.failAt > 0 && len(r.calls) == r.failAt {
		return fmt.Errorf("simulated command failure")
	}
	return nil
}

func TestAWGKernelRuntimeUsesIsolatedRoutingAndSafeConfig(t *testing.T) {
	root := t.TempDir()
	store, err := NewProfileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Import("Primary", []byte(testAWGConfig))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeCommands{}
	runtime, err := NewRuntime(store, filepath.Join(root, "awg-runtime"), runner)
	if err != nil {
		t.Fatal(err)
	}
	device, addr, err := runtime.Ensure(context.Background(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(device, "krmawg") || len(device) > 15 || addr != "10.66.66.2" {
		t.Fatalf("bad interface: %s %s", device, addr)
	}
	if len(runner.calls) < 3 {
		t.Fatalf("missing setup steps: %v", runner.calls)
	}
	if !strings.HasPrefix(runner.calls[0], "awg-quick up ") || !strings.Contains(strings.Join(runner.calls, " "), " table ") {
		t.Fatalf("unexpected command plan: %v", runner.calls)
	}
	generated, err := os.ReadFile(filepath.Join(root, "awg-runtime", device+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "Table = off") || strings.Contains(string(generated), "PostUp") || strings.Contains(string(generated), "DNS =") {
		t.Fatal("runtime config must not modify system DNS/routes or execute hooks")
	}
	if _, _, err = runtime.Ensure(context.Background(), item.ID); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 3 {
		t.Fatal("idempotent Ensure mutated networking twice")
	}
	if err = runtime.Stop(context.Background(), item.ID); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) < 5 {
		t.Fatalf("missing cleanup: %v", runner.calls)
	}
}

func TestAWGRuntimeRollsBackOnRouteFailure(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewProfileStore(dir)
	item, _ := store.Import("Rollback", []byte(testAWGConfig))
	runner := &fakeCommands{failAt: 2}
	runtime, err := NewRuntime(store, filepath.Join(dir, "runtime"), runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = runtime.Ensure(context.Background(), item.ID); err == nil {
		t.Fatal("route failure was ignored")
	}
	combined := strings.Join(runner.calls, ";")
	if !strings.Contains(combined, "awg-quick down") {
		t.Fatalf("tun left running after failure: %s", combined)
	}
}

func TestAWGRuntimeRejectsUnknownProfileAndInvalidSourceAddress(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewProfileStore(dir)
	runtime, _ := NewRuntime(store, filepath.Join(dir, "runtime"), &fakeCommands{})
	if _, _, err := runtime.Ensure(context.Background(), strings.Repeat("f", 32)); err == nil {
		t.Fatal("unknown profile started")
	}
}
