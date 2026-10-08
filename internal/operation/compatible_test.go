package operation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCompatibleReservationsPersistAndRecoverTogether(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	benchmark, err := c.StartCompatible("benchmark", "test")
	if err != nil {
		t.Fatal(err)
	}
	action, err := c.StartCompatible("client-policy", "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.List()) != 2 || c.Current().ID != benchmark.ID() {
		t.Fatal("independent actions hid benchmark")
	}
	for _, kind := range []string{"client-policy", "benchmark", "router-reboot", "xray-restart"} {
		if _, err = c.StartCompatible(kind, "test"); !errors.Is(err, ErrBusy) {
			t.Fatalf("incompatible %s accepted: %v", kind, err)
		}
	}
	restarted, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Current().Status != "unknown" {
		t.Fatal("restart claimed completion")
	}
	if err = action.Success("done"); err != nil {
		t.Fatal(err)
	}
	if len(c.List()) != 2 || c.Current().ID != benchmark.ID() {
		t.Fatal("action completion released benchmark")
	}
	if err = benchmark.Success("done"); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryExposesInterruptedCompanion(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	bench, err := c.StartCompatible("benchmark", "test")
	if err != nil {
		t.Fatal(err)
	}
	action, err := c.StartCompatible("client-policy", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = bench.Success("done"); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if op := recovered.Current(); op.ID != action.ID() || op.Status != "unknown" {
		t.Fatalf("interrupted action hidden by completed benchmark: %+v", op)
	}
	if len(recovered.List()) != 2 {
		t.Fatal("recovered companion audit disappeared")
	}
}

func TestCompanionCompletionWriteFailureRemainsVisible(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bench, err := c.StartCompatible("benchmark", "test")
	if err != nil {
		t.Fatal(err)
	}
	action, err := c.StartCompatible("client-policy", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(c.path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(c.path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = action.Success("done"); err == nil {
		t.Fatal("write failure hidden")
	}
	unknown := false
	for _, op := range c.List() {
		if op.ID == action.ID() && op.Status == "unknown" {
			unknown = true
		}
	}
	if !unknown || c.Current().ID != bench.ID() {
		t.Fatal("companion failure hidden or benchmark reservation lost")
	}
	if _, err = c.StartCompatible("benchmark", "test"); !errors.Is(err, ErrBusy) {
		t.Fatal("failure released active benchmark")
	}
	if err = os.Remove(c.path); err != nil {
		t.Fatal(err)
	}
	if err = bench.Success("done"); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(filepath.Dir(c.path))
	if err != nil {
		t.Fatal(err)
	}
	unknown = false
	for _, op := range recovered.List() {
		if op.ID == action.ID() && op.Status == "unknown" {
			unknown = true
		}
	}
	if !unknown {
		t.Fatal("later successful completion erased failure audit")
	}
}

func TestBoundedRecoveryRetainsNewestInterruptedOperation(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		h, err := c.Start("test", "test")
		if err != nil {
			t.Fatal(err)
		}
		if err = h.Success("done"); err != nil {
			t.Fatal(err)
		}
	}
	bench, err := c.StartCompatible("benchmark", "test")
	if err != nil {
		t.Fatal(err)
	}
	action, err := c.StartCompatible("client-policy", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = bench.Success("done"); err != nil {
		t.Fatal(err)
	}
	recovered, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range recovered.List() {
		if op.ID == action.ID() && op.Status == "unknown" {
			found = true
		}
	}
	if !found || len(recovered.List()) > 16 {
		t.Fatal("history truncation dropped newly interrupted operation")
	}
}
