package store

import (
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"testing"
)

func TestJournalSurvivesEveryStage(t *testing.T) {
	stages := []string{Prepared, XrayFilesStaged, XrayRuntimeApplied, FirewallApplied, SelectionApplied, StateCommitted}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			d := t.TempDir()
			initial := model.NewState("v", "slot-", 2)
			s, err := New(d, d, initial)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.PrepareTransaction(Transaction{ID: "tx-test", Kind: "pool", Before: initial, Desired: initial}); err != nil {
				t.Fatal(err)
			}
			for _, next := range stages[1:] {
				if stage == Prepared {
					break
				}
				if err = s.AdvanceTransaction(next); err != nil {
					t.Fatal(err)
				}
				if next == stage {
					break
				}
			}
			reopened, err := New(d, d, initial)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := reopened.PendingTransaction()
			if err != nil {
				t.Fatal(err)
			}
			if tx == nil || tx.Stage != stage {
				t.Fatal("interrupted journal lost")
			}
			if err = reopened.PrepareTransaction(Transaction{ID: "second"}); err == nil {
				t.Fatal("pending journal overwritten")
			}
		})
	}
}
func TestJournalRejectsSkippedStages(t *testing.T) {
	d := t.TempDir()
	s, err := New(d, d, model.NewState("v", "slot-", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PrepareTransaction(Transaction{ID: "tx", Kind: "pool", Desired: model.NewState("v", "slot-", 1)}); err != nil {
		t.Fatal(err)
	}
	if err = s.AdvanceTransaction(StateCommitted); err == nil {
		t.Fatal("skipped transition accepted")
	}
}
