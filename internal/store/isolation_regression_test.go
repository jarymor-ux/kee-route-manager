package store

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestEventRedactsNestedContainersBeforePersistence(t *testing.T) {
	dir := t.TempDir()
	initial := model.NewState("test", "slot-", 1)
	s, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{
		"providers": []any{map[string]any{"token": "nested-secret", "name": "provider"}, []any{map[string]any{"password": "deep-secret"}}},
		"headers":   []map[string]string{{"Authorization": "typed-secret", "Proxy-Authorization": "proxy-secret", "mode": "test"}},
		"reality":   []any{map[string]any{"pbk": "public-key-secret", "sid": "short-id-secret"}},
		"counter":   uint64(9007199254740993),
	}
	appended, err := s.Append(event.Event{Type: "test", Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(s.eventsPath())
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"append": appended, "memory": s.Events(0, 10), "disk": json.RawMessage(onDisk), "reload": reopened.Events(0, 10)} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"nested-secret", "deep-secret", "typed-secret", "proxy-secret", "public-key-secret", "short-id-secret"} {
			if strings.Contains(string(data), secret) {
				t.Errorf("%s leaked %s", name, secret)
			}
		}
		if !strings.Contains(string(data), "provider") || !strings.Contains(string(data), "test") {
			t.Errorf("%s lost nonsensitive diagnostics", name)
		}
		if !strings.Contains(string(data), "9007199254740993") {
			t.Errorf("%s changed a large integer diagnostic", name)
		}
	}
	if fields["providers"].([]any)[0].(map[string]any)["token"] != "nested-secret" {
		t.Fatal("Append mutated caller diagnostics")
	}
}

func TestEventSnapshotsCannotMutateStoredDiagnostics(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, dir, model.NewState("test", "slot-", 1))
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"nested": []any{map[string]any{"message": "original"}}}
	appended, err := s.Append(event.Event{Fields: input})
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(fields map[string]any) { fields["nested"].([]any)[0].(map[string]any)["message"] = "changed" }
	for name, fields := range map[string]map[string]any{"input": input, "append": appended.Fields, "snapshot": s.Events(0, 1)[0].Fields} {
		mutate(fields)
		if got := s.Events(0, 1)[0].Fields["nested"].([]any)[0].(map[string]any)["message"]; got != "original" {
			t.Errorf("%s aliases stored event: %v", name, got)
		}
	}
}

func TestNodeSnapshotsCannotMutateCache(t *testing.T) {
	for _, source := range []string{"input", "node", "nodes"} {
		t.Run(source, func(t *testing.T) {
			dir := t.TempDir()
			s, err := New(dir, dir, model.NewState("test", "slot-", 1))
			if err != nil {
				t.Fatal(err)
			}
			input := []model.Node{{ID: "node", Sources: []string{"provider"}, ALPN: []string{"h2"}}}
			if err := s.ReplaceNodes(input); err != nil {
				t.Fatal(err)
			}
			var changed model.Node
			switch source {
			case "input":
				changed = input[0]
			case "node":
				changed, _ = s.Node("node")
			case "nodes":
				changed = s.Nodes()[0]
			}
			changed.Sources[0], changed.ALPN[0] = "changed", "http/1.1"
			got, _ := s.Node("node")
			if got.Sources[0] != "provider" || got.ALPN[0] != "h2" {
				t.Fatalf("%s aliases stored node: %+v", source, got)
			}
		})
	}
}

func TestDurableUpdateFlushesUnchangedVolatileState(t *testing.T) {
	dir := t.TempDir()
	initial := model.NewState("test", "slot-", 1)
	s, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateVolatile(func(state *model.State) error { state.LastHealthMessage = "healthy"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(state *model.State) error { state.LastHealthMessage = "healthy"; return nil }); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir, dir, initial)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.State().LastHealthMessage; got != "healthy" {
		t.Fatalf("durable update lost volatile state after reload: %q", got)
	}
}
