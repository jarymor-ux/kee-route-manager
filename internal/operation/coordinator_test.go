package operation

import (
	"errors"
	"strings"
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

func TestOperationErrorsAreRedacted(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Start("test", "api")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Fail(errors.New("https://user:secret@example.com/path?token=hidden uuid=12345678-1234-1234-1234-123456789abc")); err != nil {
		t.Fatal(err)
	}
	message := c.Current().Error
	for _, secret := range []string{"secret", "hidden", "12345678-1234"} {
		if strings.Contains(message, secret) {
			t.Fatalf("operation leaked %s", secret)
		}
	}
}
