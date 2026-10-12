package amnezia

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type intentCheckingRunner struct {
	path   string
	phases []string
}

func (r *intentCheckingRunner) Run(_ context.Context, _ string, _ ...string) error {
	raw, err := os.ReadFile(r.path)
	if err != nil {
		return err
	}
	var state struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if state.Phase != "starting" && state.Phase != "stopping" {
		return errors.New("network changed without pending durable lifecycle intent")
	}
	r.phases = append(r.phases, state.Phase)
	return nil
}

func TestAWGStartupAndStopRecordIntentBeforeEveryNetworkMutation(t *testing.T) {
	rt, id, _ := lifecycleFixture(t)
	intent := filepath.Join(rt.dir, deviceName(id)+".intent.json")
	checker := &intentCheckingRunner{path: intent}
	rt.runner = checker

	if _, _, err := rt.Ensure(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if len(checker.phases) != 3 {
		t.Fatalf("startup mutation count: %v", checker.phases)
	}
	for _, phase := range checker.phases {
		if phase != "starting" {
			t.Fatalf("startup mutated kernel during %s phase", phase)
		}
	}
	raw, err := os.ReadFile(intent)
	if err != nil {
		t.Fatal(err)
	}
	var committed struct {
		ID    string `json:"profile_id"`
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatal(err)
	}
	if committed.ID != id || committed.Phase != "active" {
		t.Fatalf("unexpected AWG committed intent: %+v", committed)
	}

	if err := rt.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	for _, phase := range checker.phases[3:] {
		if phase != "stopping" {
			t.Fatalf("shutdown mutated kernel during %s phase", phase)
		}
	}
	if len(checker.phases) != 6 {
		t.Fatalf("shutdown mutation count: %v", checker.phases)
	}
	if _, err := os.Stat(intent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fully stopped AWG retained intent: %v", err)
	}
}

func TestAWGFailedRollbackPreservesPendingDurableIntent(t *testing.T) {
	rt, id, runner := lifecycleFixture(t)
	runner.fail[2] = true // failed route creation
	runner.fail[3] = true // failed interface rollback
	if _, _, err := rt.Ensure(context.Background(), id); err == nil {
		t.Fatal("expected failed startup")
	}
	if _, err := os.Stat(filepath.Join(rt.dir, deviceName(id)+".intent.json")); err != nil {
		t.Fatalf("ambiguous startup lost its journal: %v", err)
	}
}
