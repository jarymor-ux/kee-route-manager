package amnezia

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWGPolicyRuleMustPrecedeMainRoute(t *testing.T) {
	root := t.TempDir()
	store, err := NewProfileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.Import("priority", []byte(testAWGConfig))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeCommands{}
	rt, err := NewRuntime(store, filepath.Join(root, "runtime"), runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.Ensure(context.Background(), profile.ID); err != nil {
		t.Fatal(err)
	}
	table, err := tableNumber(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Linux allocates a priority ahead of the default main table (32766)
	// when "pref" is omitted. The fwmark/table number is NOT a valid rule
	// priority: it is greater than 32766 and would silently bypass AWG.
	expected := "ip rule add fwmark " + table + " lookup " + table
	joined := strings.Join(runner.calls, "\n")
	if !strings.Contains(joined, expected) {
		t.Fatalf("AWG rule lacks main-table precedence: %s", joined)
	}
	if strings.Contains(joined, "ip rule add pref "+table) {
		t.Fatalf("AWG rule is installed after main: %s", joined)
	}
	if err := rt.Stop(context.Background(), profile.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "ip rule del fwmark "+table+" lookup "+table) {
		t.Fatalf("AWG teardown did not remove its exact marked rule: %v", runner.calls)
	}
}
