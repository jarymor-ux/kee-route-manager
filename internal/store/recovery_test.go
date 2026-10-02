package store

import (
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestCorruptStateFallsBackToPrevious(t *testing.T) {
	d := t.TempDir()
	initial := model.NewState("rc2", "slot-", 2)
	s, err := New(d, d, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(st *model.State) error { st.LastHealthMessage = "previous"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(st *model.State) error { st.LastHealthMessage = "latest"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(d, "state.json"), []byte(`{"schema_version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(d, d, initial)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State().LastHealthMessage != "previous" {
		t.Fatal("previous durable state not recovered")
	}
}

func TestRejectInvalidStateMutation(t *testing.T) {
	d := t.TempDir()
	s, err := New(d, d, model.NewState("rc2", "slot-", 2))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*model.State) error{
		func(st *model.State) error { st.SchemaVersion = 99; return nil },
		func(st *model.State) error { st.Pool[0].Index = 7; return nil },
		func(st *model.State) error { st.Pool[1].Tag = st.Pool[0].Tag; return nil },
		func(st *model.State) error { st.ActiveSlot = 1; st.ActiveNodeID = "missing"; return nil },
		func(st *model.State) error { st.XrayGeneration = -1; return nil },
	} {
		if err := s.Update(mutate); err == nil {
			t.Fatal("invalid mutation accepted")
		}
	}
}

func TestBothInvalidCopiesEnterRecovery(t *testing.T) {
	d := t.TempDir()
	for _, name := range []string{"state.json", "state.previous.json"} {
		if err := os.WriteFile(filepath.Join(d, name), []byte("broken"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(d, d, model.NewState("rc2", "slot-", 2))
	if err != nil {
		t.Fatal(err)
	}
	if s.State().XrayConfigured || s.State().ActiveSlot != -1 || s.State().XrayLastError == "" {
		t.Fatal("expected safe recovery state")
	}
}

func TestPreviousRecoveryIncludesMatchingNodeCache(t *testing.T) {
	d := t.TempDir()
	initial := model.NewState("rc2", "slot-", 1)
	s, err := New(d, d, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReplaceNodes([]model.Node{{ID: "old"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(st *model.State) error {
		st.XrayConfigured = true
		st.Pool[0].NodeID = "old"
		st.ActiveSlot = 0
		st.ActiveNodeID = "old"
		st.XrayGeneration = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.ReplaceNodes([]model.Node{{ID: "new"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(st *model.State) error {
		st.Pool[0].NodeID = "new"
		st.ActiveNodeID = "new"
		st.XrayGeneration = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(d, "state.json"), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err = New(d, d, initial)
		if err != nil {
			t.Fatal(err)
		}
		if s.State().ActiveNodeID != "old" {
			t.Fatal("matching node cache was not recovered")
		}
		if _, ok := s.Node("old"); !ok {
			t.Fatal("previous node missing")
		}
	}
}

func TestCompletedRestoreRecoveryUsesJournal(t *testing.T) {
	for _, damage := range []string{"primary-corrupt", "primary-missing", "both-corrupt", "both-missing", "cache-corrupt"} {
		t.Run(damage, func(t *testing.T) {
			s, initial := completedRestoreFixture(t)
			journal, err := os.ReadFile(s.Path("transaction.json"))
			if err != nil {
				t.Fatal(err)
			}
			if damage == "primary-missing" || damage == "both-missing" {
				err = os.Remove(s.Path("state.json"))
			} else {
				err = os.WriteFile(s.Path("state.json"), []byte("corrupted"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "both-corrupt":
				err = os.WriteFile(s.Path("state.previous.json"), []byte("corrupted"), 0600)
			case "both-missing":
				err = os.Remove(s.Path("state.previous.json"))
			case "cache-corrupt":
				err = os.WriteFile(s.CachePath("nodes.json"), []byte("corrupted"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				s, err = New(s.Path(""), s.CachePath(""), initial)
				if err != nil {
					t.Fatal(err)
				}
				state := s.State()
				if !state.AutomaticRoutingPaused || state.XrayConfigured || state.ActiveNodeID != "" || len(state.Pool) != 0 || len(s.Nodes()) != 0 {
					t.Fatalf("completed restore lost during recovery: %+v", state)
				}
				after, err := os.ReadFile(s.Path("transaction.json"))
				if err != nil || string(after) != string(journal) {
					t.Fatal("recovery changed the journal")
				}
			}
		})
	}
}

func TestRestoreRecoveryRetainsPendingManualResume(t *testing.T) {
	s, initial := completedRestoreFixture(t)
	desired := initial
	desired.XrayConfigured = true
	desired.ActiveSlot = 0
	desired.ActiveNodeID = "resumed"
	desired.Pool[0].NodeID = "resumed"
	desired.XrayGeneration = 1
	tx := Transaction{ID: "manual-resume", Kind: "pool", Before: s.State(), Desired: desired, Nodes: []model.Node{{ID: "resumed"}}}
	if err := s.PrepareTransaction(tx); err != nil {
		t.Fatal(err)
	}
	journal, err := os.ReadFile(s.Path("transaction.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(s.Path("state.json"), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(s.Path(""), s.CachePath(""), initial)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := recovered.PendingTransaction()
	if err != nil || pending == nil || pending.ID != tx.ID || pending.Stage != Prepared || pending.Desired.AutomaticRoutingPaused {
		t.Fatalf("manual resume intent lost: %+v %v", pending, err)
	}
	after, err := os.ReadFile(s.Path("transaction.json"))
	if err != nil || string(after) != string(journal) {
		t.Fatal("recovery changed the pending resume journal")
	}
}

func completedRestoreFixture(t *testing.T) (*Store, model.State) {
	t.Helper()
	d := t.TempDir()
	initial := model.NewState("rc2", "slot-", 1)
	s, err := New(d, d, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReplaceNodes([]model.Node{{ID: "old"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(state *model.State) error {
		state.XrayConfigured = true
		state.ActiveSlot = 0
		state.ActiveNodeID = "old"
		state.Pool[0].NodeID = "old"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	desired := model.NewState("rc2", "slot-", 1)
	desired.Pool = []model.Slot{}
	desired.AutomaticRoutingPaused = true
	if err = s.PrepareTransaction(Transaction{ID: "restore", Kind: "restore", Before: s.State(), Desired: desired}); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{XrayFilesStaged, XrayRuntimeApplied, FirewallApplied, SelectionApplied} {
		if err = s.AdvanceTransaction(stage); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.ReplaceNodes(nil); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(state *model.State) error { *state = desired; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{StateCommitted, Done} {
		if err = s.AdvanceTransaction(stage); err != nil {
			t.Fatal(err)
		}
	}
	return s, initial
}
