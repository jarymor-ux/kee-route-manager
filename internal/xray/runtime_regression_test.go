package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

func TestStaticPoolRestartFailureRestoresPersistedSelection(t *testing.T) {
	c, desired, runner, _ := reviewPoolFixture(t)
	c.Xray.DynamicAPI = false
	platform := &reviewFailRestartPlatform{}
	m := NewManager(c, runner, platform)
	path := filepath.Join(c.Xray.ManagedDir, "04_90_kee_route_manager_outbounds.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next := testNode()
	next.ID, next.Address = "replacement", "203.0.113.11"
	desired.Slots = append([]model.Slot(nil), desired.Slots...)
	desired.Slots[0].NodeID = next.ID
	desired.Nodes = map[string]model.Node{next.ID: next}
	if err = m.ApplyPool(context.Background(), desired); err == nil {
		t.Fatal("restart failure accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("failed pool replacement lost working persistent configuration: %v", err)
	}
	if platform.restarts != 2 {
		t.Errorf("working configuration was not restarted after rollback: %d restarts", platform.restarts)
	}
}

func TestActualStateDetectsAdoptedRoutingCardinalityDrift(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			m, _, _ := snapshotAliasFixture(t)
			data, err := os.ReadFile(m.cfg.Xray.BaseRoutingFile)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err = json.Unmarshal(data, &root); err != nil {
				t.Fatal(err)
			}
			routing := root["routing"].(map[string]any)
			allRules := routing["rules"].([]any)
			rule := allRules[len(allRules)-1]
			rules := []any{}
			rules = append(rules, allRules[:len(allRules)-1]...)
			for i := 0; i < count; i++ {
				rules = append(rules, rule)
			}
			routing["rules"] = rules
			if err = os.WriteFile(m.cfg.Xray.BaseRoutingFile, pretty(root), 0600); err != nil {
				t.Fatal(err)
			}
			actual, err := m.ActualState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if (actual.Drift != "") != (count != 1) {
				t.Fatalf("adopted rule count %d, reported drift %q", count, actual.Drift)
			}
		})
	}
}

func TestTemporaryStartupErrorRedactsNodeCredentials(t *testing.T) {
	c := batchHelperConfig(t, "fail")
	const secret = "11111111-1111-1111-1111-111111111111"
	_, err := NewBatchRunner(c).Start(context.Background(), []model.Node{testNode()})
	if err == nil || !strings.Contains(err.Error(), "temporary xray exited before readiness") {
		t.Fatalf("expected startup failure: %v", err)
	}
	if !strings.Contains(err.Error(), "bad node") {
		t.Fatalf("startup fixture did not capture process output: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("startup failure exposed node credential")
	}
	entries, err := os.ReadDir(c.Paths.RunDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed startup left private configuration behind: %v %v", entries, err)
	}
}

type blockedSelectionRunner struct {
	entered chan struct{}
	release chan struct{}
}

func (r *blockedSelectionRunner) Run(ctx context.Context, _ []string) ([]byte, error) {
	close(r.entered)
	select {
	case <-r.release:
		return nil, errors.New("injected selection failure")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestActualStateWaitsForSelectionRollback(t *testing.T) {
	c, desired, _, platform := reviewPoolFixture(t)
	runner := &blockedSelectionRunner{entered: make(chan struct{}), release: make(chan struct{})}
	m := NewManager(c, runner, platform)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	selected := make(chan error, 1)
	go func() { selected <- m.EnterDirect(ctx) }()
	select {
	case <-runner.entered:
	case <-ctx.Done():
		t.Fatal("selection API was never called")
	}
	observed := make(chan tunnel.ActualCoreState, 1)
	observationError := make(chan error, 1)
	go func() {
		actual, err := m.ActualState(ctx)
		observed <- actual
		observationError <- err
	}()
	select {
	case actual := <-observed:
		close(runner.release)
		<-selected
		t.Fatalf("reported uncommitted selection during API mutation: %+v", actual)
	case <-time.After(30 * time.Millisecond):
	}
	close(runner.release)
	if err := <-selected; err == nil {
		t.Fatal("injected selection failure was hidden")
	}
	actual := <-observed
	if err := <-observationError; err != nil || actual.Selection.Tag != desired.Selection.Tag {
		t.Fatalf("state observation did not wait for rollback: %+v %v", actual, err)
	}
}
