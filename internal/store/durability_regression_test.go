package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestVolatileFlushAndRejectedUpdatesPreserveDurableState(t *testing.T) {
	dir := t.TempDir()
	initial := model.NewState("test", "slot-", 1)
	s, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(state *model.State) error { state.LastHealthMessage = "durable"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateVolatile(func(state *model.State) error { state.LastHealthMessage = "volatile"; return nil }); err != nil {
		t.Fatal(err)
	}
	durable, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	if durable.State().LastHealthMessage != "durable" || s.State().LastHealthMessage != "volatile" {
		t.Fatal("volatile mutation unexpectedly changed disk or failed to update memory")
	}
	failure := errors.New("rejected")
	if err := s.UpdateVolatile(func(state *model.State) error { state.LastHealthMessage = "bad"; return failure }); !errors.Is(err, failure) {
		t.Fatalf("mutation error lost: %v", err)
	}
	if err := s.UpdateVolatile(func(state *model.State) error { state.ActiveNodeID = "missing"; return nil }); err == nil {
		t.Fatal("invalid volatile selection accepted")
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State().LastHealthMessage != "volatile" || reopened.State().ActiveNodeID != "" {
		t.Fatal("flush lost good mutation or committed rejected selection")
	}
	before, err := os.ReadFile(s.Path("state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(s.Path("state.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("clean flush changed durable state")
	}
}

func TestEventCompactionPreservesNewestRecordsAndSequence(t *testing.T) {
	dir := t.TempDir()
	initial := model.NewState("test", "slot-", 1)
	s, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	enc := json.NewEncoder(&log)
	const total = 9000
	for i := 1; i <= total; i++ {
		if err := enc.Encode(event.Event{Sequence: uint64(i), Type: "test", Message: strings.Repeat("x", 1000)}); err != nil {
			t.Fatal(err)
		}
	}
	if log.Len() <= maxEventLogSize {
		t.Fatal("fixture does not require compaction")
	}
	if err := os.WriteFile(s.eventsPath(), log.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.eventsPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= maxEventLogSize || info.Mode().Perm() != 0600 {
		t.Fatalf("log was not compacted privately: size=%d mode=%o", info.Size(), info.Mode().Perm())
	}
	first := reopened.Events(0, 1)
	if len(first) != 1 || first[0].Sequence != total-maxEvents+1 {
		t.Fatalf("wrong retained prefix: %+v", first)
	}
	last := reopened.Events(total-1, 10)
	if len(last) != 1 || last[0].Sequence != total {
		t.Fatalf("latest event lost: %+v", last)
	}
	appended, err := reopened.Append(event.Event{Message: "next"})
	if err != nil || appended.Sequence != total+1 {
		t.Fatalf("sequence reset after compaction: %+v %v", appended, err)
	}
}

func TestJournalRejectsInvalidIdentityBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	initial := model.NewState("test", "slot-", 1)
	s, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareTransaction(Transaction{Kind: "pool", Desired: initial}); err == nil {
		t.Error("journal with missing ID was written")
	}
	if _, err := os.Stat(s.Path("transaction.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid preparation poisoned journal: %v", err)
	}
	if err := s.PrepareTransaction(Transaction{ID: "valid", Kind: "pool", Desired: initial}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingTransaction()
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []int{0, 2} {
		invalid := *pending
		invalid.Schema = schema
		if err := s.RestartTransaction(invalid); err == nil {
			t.Errorf("restart accepted schema %d", schema)
		}
		stillPending, err := s.PendingTransaction()
		if err != nil || stillPending == nil || stillPending.ID != "valid" {
			t.Fatalf("invalid restart destroyed journal: %+v %v", stillPending, err)
		}
	}
}
