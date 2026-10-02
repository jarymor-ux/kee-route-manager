package store

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func readOnlyFixture(t *testing.T) (*Store, model.State) {
	t.Helper()
	dir := t.TempDir()
	initial := model.NewState("old", "slot-", 1)
	s, err := New(filepath.Join(dir, "state"), filepath.Join(dir, "cache"), initial)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []model.Node{{ID: "one"}}
	if err = s.ReplaceNodes(nodes); err != nil {
		t.Fatal(err)
	}
	next := initial
	next.XrayConfigured = true
	next.XrayGeneration = 1
	next.XrayConfigHash = strings.Repeat("a", 64)
	next.ActiveSlot = 0
	next.ActiveNodeID = "one"
	next.Pool[0].NodeID = "one"
	if err = s.PrepareTransaction(Transaction{ID: "completed", Kind: "pool", Before: initial, Desired: next, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(v *model.State) error { *v = next; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{XrayFilesStaged, XrayRuntimeApplied, FirewallApplied, SelectionApplied, StateCommitted, Done} {
		if err = s.AdvanceTransaction(stage); err != nil {
			t.Fatal(err)
		}
	}
	return s, initial
}

func storeTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	hashes := map[string][32]byte{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil {
			hashes[path] = sha256.Sum256(b)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return hashes
}

func TestInspectReadOnlyValidatesPrimaryWithoutRecoveryOrWrites(t *testing.T) {
	for _, failure := range []string{"none", "corrupt-primary", "missing-cache", "duplicate-node", "pending", "journal-state-drift", "unknown-field", "trailing-json", "symlink"} {
		t.Run(failure, func(t *testing.T) {
			s, initial := readOnlyFixture(t)
			switch failure {
			case "corrupt-primary":
				b, _ := os.ReadFile(s.Path("state.json"))
				os.WriteFile(s.Path("state.previous.json"), b, 0600)
				os.WriteFile(s.Path("state.json"), []byte("broken"), 0600)
			case "missing-cache":
				os.Remove(s.CachePath("nodes.json"))
			case "duplicate-node":
				os.WriteFile(s.CachePath("nodes.json"), []byte(`[{"id":"one"},{"id":"one"}]`), 0600)
			case "pending":
				tx, err := s.readTransaction()
				if err != nil {
					t.Fatal(err)
				}
				tx.Stage = XrayFilesStaged
				if err = writeJSON(s.Path("transaction.json"), tx); err != nil {
					t.Fatal(err)
				}
			case "journal-state-drift":
				if err := s.Update(func(v *model.State) error { v.XrayGeneration++; return nil }); err != nil {
					t.Fatal(err)
				}
			case "unknown-field":
				b, _ := os.ReadFile(s.Path("state.json"))
				var v map[string]any
				json.Unmarshal(b, &v)
				v["new_schema_behavior"] = true
				if err := writeJSON(s.Path("state.json"), v); err != nil {
					t.Fatal(err)
				}
			case "trailing-json":
				b, _ := os.ReadFile(s.Path("state.json"))
				os.WriteFile(s.Path("state.json"), append(b, []byte(" {}")...), 0600)
			case "symlink":
				if err := os.Rename(s.Path("state.json"), s.Path("saved-state.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("saved-state.json", s.Path("state.json")); err != nil {
					t.Fatal(err)
				}
			}
			before := storeTree(t, filepath.Dir(s.stateDir))
			state, err := InspectReadOnly(s.stateDir, s.cacheDir, initial)
			if (err == nil) != (failure == "none") {
				t.Fatalf("failure=%s err=%v", failure, err)
			}
			if failure == "none" && state.ActiveNodeID != "one" {
				t.Fatal("lost committed selection")
			}
			if !reflect.DeepEqual(before, storeTree(t, filepath.Dir(s.stateDir))) {
				t.Fatal("read-only inspection changed stored files")
			}
		})
	}
}

func TestInspectReadOnlyNeverBootstrapsMissingDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	if _, err := InspectReadOnly(dir, dir, model.NewState("new", "slot-", 1)); err == nil {
		t.Fatal("accepted missing primary")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("inspection created state directory")
	}
}

func TestInspectReadOnlyAcceptsHashFinalizedAfterJournalIntent(t *testing.T) {
	s, initial := readOnlyFixture(t)
	tx, err := s.readTransaction()
	if err != nil {
		t.Fatal(err)
	}
	// Runtime effects finalize the primary hash; the journal keeps its intent.
	tx.Desired.XrayConfigHash = ""
	if err = writeJSON(s.Path("transaction.json"), tx); err != nil {
		t.Fatal(err)
	}
	if _, err = InspectReadOnly(s.stateDir, s.cacheDir, initial); err != nil {
		t.Fatalf("rejected complete transaction: %v", err)
	}
}
