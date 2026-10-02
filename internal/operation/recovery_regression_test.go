package operation

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRestartMarksInterruptedOperationUnknownDurably(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Current() != nil {
		t.Fatal("new coordinator has an operation")
	}
	h, err := c.Start("benchmark", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Update("latency", 2, 4, "token=private-value"); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	op := restarted.Current()
	if op.ID != h.ID() || op.Status != "unknown" || op.FinishedAt.IsZero() || op.Error == "" || strings.Contains(op.Message, "private-value") {
		t.Fatalf("invalid recovered operation: %+v", op)
	}
	op.Status = "changed"
	if restarted.Current().Status != "unknown" {
		t.Fatal("Current aliases internal operation")
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Operation
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "unknown" || persisted.ID != h.ID() {
		t.Fatalf("recovery was not durable: %+v", persisted)
	}
	next, err := restarted.Start("restore", "test")
	if err != nil {
		t.Fatalf("interrupted operation blocked new execution: %v", err)
	}
	if err := next.Success("done"); err != nil {
		t.Fatal(err)
	}
}

func TestProgressWritesAreThrottledButStageAndCompletionPersist(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Start("benchmark", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Update("latency", 1, 10, "one"); err != nil {
		t.Fatal(err)
	}
	read := func() []byte {
		t.Helper()
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := read()
	// Pin the throttle window without relying on filesystem speed.
	c.lastWrite = time.Now().Add(time.Hour)
	if err := h.Update("latency", 2, 10, "two"); err != nil {
		t.Fatal(err)
	}
	if c.Current().Current != 2 || string(read()) != string(first) {
		t.Fatal("same-stage update was not cached without a disk write")
	}
	if err := h.Update("speed", 1, 2, "download"); err != nil {
		t.Fatal(err)
	}
	if string(read()) == string(first) {
		t.Fatal("stage transition was not immediately durable")
	}
	if err := h.Success("complete"); err != nil {
		t.Fatal(err)
	}
	var persisted Operation
	if err := json.Unmarshal(read(), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "succeeded" || persisted.Message != "complete" || persisted.FinishedAt.IsZero() {
		t.Fatalf("completion not durable: %+v", persisted)
	}
}

func TestFinishedHandleCannotAffectNextOperation(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old, err := c.Start("benchmark", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Success("done"); err != nil {
		t.Fatal(err)
	}
	next, err := c.Start("restore", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Update("stale", 1, 1, "stale callback"); err == nil {
		t.Fatal("stale progress accepted")
	}
	if err := old.Fail(nil); err == nil {
		t.Fatal("stale completion accepted")
	}
	if got := c.Current(); got.ID != next.ID() || got.Status != "running" || got.Stage != "starting" {
		t.Fatalf("stale handle changed new operation: %+v", got)
	}
	if err := next.Fail(nil); err != nil {
		t.Fatal(err)
	}
	if c.Current().Status != "failed" || c.Current().Error != "failed" {
		t.Fatal("nil failure was not recorded")
	}
}

func TestFailedStartDoesNotReserveExecution(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(c.path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start("benchmark", "test"); err == nil {
		t.Fatal("failed initial journal write accepted")
	}
	if c.Current() != nil {
		t.Fatal("failed start reserved execution")
	}
	if err := os.Remove(c.path); err != nil {
		t.Fatal(err)
	}
	h, err := c.Start("restore", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Success("done"); err != nil {
		t.Fatal(err)
	}
}
