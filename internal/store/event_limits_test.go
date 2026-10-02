package store

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func TestLegacyOversizedEventDoesNotBlockStateRecovery(t *testing.T) {
	d := t.TempDir()
	initial := model.NewState("v", "slot-", 1)
	s, err := New(d, d, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(st *model.State) error { st.LastSwitchReason = "durable control"; return nil }); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(s.eventsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []event.Event{{Sequence: 5, Message: "before"}, {Sequence: 6, Message: strings.Repeat("x", (1<<20)+100)}, {Sequence: 7, Message: "after"}} {
		b, _ := json.Marshal(e)
		if _, err = f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(d, d, initial)
	if err != nil {
		t.Fatalf("legacy oversized line blocks recovery: %v", err)
	}
	if reopened.State().LastSwitchReason != "durable control" {
		t.Fatal("durable state lost")
	}
	xs := reopened.Events(0, 10)
	if len(xs) != 3 || xs[0].Message != "before" || xs[2].Message != "after" || !strings.Contains(xs[1].Message, "truncated") {
		t.Fatalf("surrounding records or omission marker lost: %+v", xs)
	}
	next, err := reopened.Append(event.Event{Message: "next"})
	if err != nil || next.Sequence != 8 {
		t.Fatalf("sequence continuity: %+v %v", next, err)
	}
	legacy, err := os.OpenFile(s.eventsPath(), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(event.Event{Sequence: 20, Message: strings.Repeat("x", (1<<20)+100)})
	_, err = legacy.Write(append(b, '\n'))
	closeErr := legacy.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("legacy final fixture: %v %v", err, closeErr)
	}
	reopened, err = New(d, d, initial)
	if err != nil {
		t.Fatal(err)
	}
	next, err = reopened.Append(event.Event{Message: "after oversized final record"})
	if err != nil || next.Sequence != 21 {
		t.Fatalf("oversized final sequence continuity: %+v %v", next, err)
	}
}

func TestEventSerializedBoundIncludesFieldsAndEscaping(t *testing.T) {
	d := t.TempDir()
	s, err := New(d, d, model.NewState("v", "slot-", 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []event.Event{{Message: strings.Repeat("\x00", 1<<20)}, {Message: "fields", Fields: map[string]any{"diagnostic": strings.Repeat("x", 2<<20)}}, {Type: strings.Repeat("x", 2<<20), Message: "metadata"}, {Message: "small control", Fields: map[string]any{"mode": "control"}}} {
		if _, err = s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(s.eventsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1<<20)
	count := 0
	for sc.Scan() {
		var e event.Event
		if err = json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		count++
		if count < 4 && !strings.Contains(e.Message, "truncated") {
			t.Fatal("missing explicit truncation marker")
		}
		if count == 4 && (e.Message != "small control" || e.Fields["mode"] != "control") {
			t.Fatal("bounded control changed")
		}
	}
	if sc.Err() != nil || count != 4 {
		t.Fatalf("reader/writer bound mismatch: %d %v", count, sc.Err())
	}
}
