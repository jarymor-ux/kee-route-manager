package daemonlock

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProcessLock(t *testing.T) {
	if path := os.Getenv("KRM_TEST_LOCK"); path != "" {
		_, e := Acquire(path)
		if e == nil {
			os.Exit(9)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "owner.lock")
	l, e := Acquire(path)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if l.Owner.PID != os.Getpid() || l.Owner.InstanceID == "" || l.Owner.ProcessStart == "" {
		t.Fatalf("missing owner metadata: %+v", l.Owner)
	}
	c := exec.Command(os.Args[0], "-test.run=^TestProcessLock$")
	c.Env = append(os.Environ(), "KRM_TEST_LOCK="+path)
	if e = c.Run(); e != nil {
		t.Fatalf("contending process acquired lock: %v", e)
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	l2, e := Acquire(path)
	if e != nil {
		t.Fatalf("cannot reacquire released lock: %v", e)
	}
	defer l2.Close()
}
func TestRejectSymlink(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "target")
	if e := os.WriteFile(target, []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(d, "lock")
	if e := os.Symlink(target, path); e != nil {
		t.Fatal(e)
	}
	if _, e := Acquire(path); e == nil {
		t.Fatal("accepted symlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "preserve" {
		t.Fatal("target changed")
	}
}
