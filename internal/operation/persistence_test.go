package operation

import (
	"errors"
	"os"
	"testing"
)

func TestTerminalWriteFailureReleasesOperation(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			c, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			h, err := c.Start("benchmark", "test")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(c.path); err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(c.path, 0700); err != nil {
				t.Fatal(err)
			}
			if failed {
				err = h.Fail(errors.New("benchmark failed"))
			} else {
				err = h.Success("complete")
			}
			if err == nil {
				t.Fatal("completion write fault was not reported")
			}
			if op := c.Current(); op.Status != "unknown" || op.Error == "" {
				t.Fatalf("lost completion persistence error: %+v", op)
			}
			if err = os.Remove(c.path); err != nil {
				t.Fatal(err)
			}
			next, err := c.Start("restore", "after-storage-recovery")
			if err != nil {
				t.Fatalf("storage recovered but operations remain blocked: %v", err)
			}
			if err = next.Success("done"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProgressWriteFailureKeepsRunningOperationExclusive(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Start("benchmark", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(c.path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(c.path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = h.Update("testing", 0, 1, "working"); err == nil {
		t.Fatal("write fault not reported")
	}
	if _, err = c.Start("restore", "test"); !errors.Is(err, ErrBusy) {
		t.Fatalf("running operation lost exclusivity: %v", err)
	}
}
