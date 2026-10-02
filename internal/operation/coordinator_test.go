package operation

import (
	"errors"
	"testing"
)

func TestExclusiveOperation(t *testing.T) {
	c, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	h, e := c.Start("benchmark", "test")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Start("reboot", "test"); !errors.Is(e, ErrBusy) {
		t.Fatalf("expected busy: %v", e)
	}
	_ = h.Update("work", 1, 2, "working")
	if e = h.Success("done"); e != nil {
		t.Fatal(e)
	}
	if c.Current().Status != "succeeded" {
		t.Fatal("completion not persisted")
	}
}
