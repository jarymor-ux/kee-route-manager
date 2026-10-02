package core

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func blockOperationRecord(t *testing.T, m *Manager) {
	t.Helper()
	path := m.store.Path("operation.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
}

func TestActionReportsCompletionPersistenceFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			m, _, _ := fixture(t)
			runErr := errors.New("action failed")
			err := m.RunAction(context.Background(), "test", "api", func(context.Context) error {
				blockOperationRecord(t, m)
				if failed {
					return runErr
				}
				return nil
			})
			if err == nil || (failed && !errors.Is(err, runErr)) {
				t.Fatalf("completion error lost: %v", err)
			}
			if m.ops.Current().Status != "unknown" {
				t.Fatal("completion persistence failure hidden")
			}
			if err := os.Remove(m.store.Path("operation.json")); err != nil {
				t.Fatal(err)
			}
			if err := m.RunAction(context.Background(), "next", "api", func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type completionFaultBenchmark struct {
	t *testing.T
	m *Manager
}

func (b completionFaultBenchmark) Run(context.Context, []model.Node, bench.Progress) ([]model.Measurement, error) {
	blockOperationRecord(b.t, b.m)
	return healthyReviewBenchmark().results, nil
}

func TestBenchmarkReportsCompletionPersistenceFailure(t *testing.T) {
	m, _, _ := fixture(t)
	m.bench = completionFaultBenchmark{t, m}
	if err := m.RunBenchmark(context.Background(), "manual", "api"); err == nil {
		t.Fatal("benchmark silently discarded completion persistence error")
	}
	if m.ops.Current().Status != "unknown" {
		t.Fatal("completion persistence failure hidden")
	}
	if err := os.Remove(m.store.Path("operation.json")); err != nil {
		t.Fatal(err)
	}
	m.bench = healthyReviewBenchmark()
	if err := m.RunBenchmark(context.Background(), "manual", "api"); err != nil {
		t.Fatalf("recovered storage left benchmark blocked: %v", err)
	}
}
