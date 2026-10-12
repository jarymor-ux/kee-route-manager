package amnezia

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fault injection is deliberately applied after recording a command. A failed
// command may have executed partially, so cleanup must remain conservative.
type lifecycleFaultRunner struct {
	calls []string
	fail  map[int]bool
}

func (f *lifecycleFaultRunner) Run(_ context.Context, name string, args ...string) error {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.fail[len(f.calls)] {
		return errors.New("injected system command failure")
	}
	return nil
}

func lifecycleFixture(t *testing.T) (*Runtime, string, *lifecycleFaultRunner) {
	t.Helper()
	root := t.TempDir()
	store, err := NewProfileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.Import("lifecycle", []byte(testAWGConfig))
	if err != nil {
		t.Fatal(err)
	}
	runner := &lifecycleFaultRunner{fail: make(map[int]bool)}
	rt, err := NewRuntime(store, filepath.Join(root, "runtime"), runner)
	if err != nil {
		t.Fatal(err)
	}
	return rt, profile.ID, runner
}

func TestAWGStopRestoresRuleAfterRouteRemovalFailure(t *testing.T) {
	rt, id, runner := lifecycleFixture(t)
	ctx := context.Background()
	if _, _, err := rt.Ensure(ctx, id); err != nil {
		t.Fatal(err)
	}
	table, err := tableNumber(id)
	if err != nil {
		t.Fatal(err)
	}
	runner.fail[5] = true // rule del succeeds, route del fails
	if err := rt.Stop(ctx, id); err == nil {
		t.Fatal("failed route removal must be reported")
	}
	if got, want := runner.calls[len(runner.calls)-1], "ip rule add fwmark "+table+" lookup "+table; got != want {
		t.Fatalf("failed Stop must restore routing rule: got %q, want %q", got, want)
	}
	if !rt.active[id] {
		t.Fatal("failed Stop must not forget an active interface")
	}
	if _, err := os.Stat(filepath.Join(rt.dir, deviceName(id)+".conf")); err != nil {
		t.Fatalf("failed Stop lost recovery configuration: %v", err)
	}
	delete(runner.fail, 5)
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatalf("Stop must be retryable after successful rollback: %v", err)
	}
}

func TestAWGStopRestoresRouteAndRuleAfterDownFailure(t *testing.T) {
	rt, id, runner := lifecycleFixture(t)
	ctx := context.Background()
	if _, _, err := rt.Ensure(ctx, id); err != nil {
		t.Fatal(err)
	}
	table, err := tableNumber(id)
	if err != nil {
		t.Fatal(err)
	}
	runner.fail[6] = true // two deletions succeed, awg-quick down fails
	if err := rt.Stop(ctx, id); err == nil {
		t.Fatal("failed interface shutdown must be reported")
	}
	last := runner.calls[len(runner.calls)-2:]
	wantRoute := "ip route add table "+table+" default dev "+deviceName(id)
	wantRule := "ip rule add fwmark "+table+" lookup "+table
	if last[0] != wantRoute || last[1] != wantRule {
		t.Fatalf("shutdown failure must restore both route and rule: %v", last)
	}
	if !rt.active[id] {
		t.Fatal("failed shutdown must retain active state")
	}
	delete(runner.fail, 6)
	if err := rt.Stop(ctx, id); err != nil {
		t.Fatalf("Stop must be retryable after rollback: %v", err)
	}
}

func TestAWGFailedStartupRollbackRetainsRecoveryConfiguration(t *testing.T) {
	rt, id, runner := lifecycleFixture(t)
	runner.fail[2] = true // route installation fails
	runner.fail[3] = true // interface teardown fails as well
	if _, _, err := rt.Ensure(context.Background(), id); err == nil {
		t.Fatal("failed startup must be reported")
	}
	path := filepath.Join(rt.dir, deviceName(id)+".conf")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed rollback must preserve config for recovery: %v", err)
	}
	before := len(runner.calls)
	if _, _, err := rt.Ensure(context.Background(), id); err == nil {
		t.Fatal("unreconciled interface must not be overwritten or started again")
	}
	if len(runner.calls) != before {
		t.Fatalf("unreconciled interface was manipulated: %v", runner.calls[before:])
	}
}

func TestAWGEnsureRejectsOrphanedConfigBeforeChangingNetworking(t *testing.T) {
	rt, id, runner := lifecycleFixture(t)
	safe, err := isolatedConfig([]byte(testAWGConfig))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rt.dir, deviceName(id)+".conf")
	if err := os.WriteFile(path, safe, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.Ensure(context.Background(), id); err == nil {
		t.Fatal("orphaned runtime config must trigger reconciliation, not overwrite")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("orphaned interface triggered system commands: %v", runner.calls)
	}
}
