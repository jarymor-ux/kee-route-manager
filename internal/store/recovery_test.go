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
