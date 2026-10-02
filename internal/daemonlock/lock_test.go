package daemonlock

import (
	"encoding/json"
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

func TestLockRejectsUnsafeLocationsBeforeWriting(t *testing.T) {
	if _, err := Acquire("relative.lock"); err == nil {
		t.Fatal("relative lock path accepted")
	}
	for _, mode := range []os.FileMode{0770, 0702} {
		dir := t.TempDir()
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "owner.lock")
		if _, err := Acquire(path); err == nil {
			t.Fatalf("writable parent %o accepted", mode)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unsafe parent was mutated: %v", err)
		}
	}
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "parent-alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(filepath.Join(alias, "owner.lock")); err == nil {
		t.Fatal("symlink parent accepted")
	}
	if _, err := Acquire(dir); err == nil {
		t.Fatal("directory accepted as lock file")
	}
}

func TestLockReplacesStaleMetadataPrivatelyAndKeepsInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lock")
	if err := os.WriteFile(path, []byte(`{"stale":"a much longer previous owner record that must be completely replaced"}`), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var owner Owner
	if err = json.Unmarshal(data, &owner); err != nil || owner != l.Owner {
		t.Fatalf("persisted owner differs: %+v %v", owner, err)
	}
	after, err := os.Stat(path)
	if err != nil || after.Mode().Perm() != 0600 || !os.SameFile(before, after) {
		t.Fatalf("lock permissions or inode changed incorrectly: %v %v", after, err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("release removed ownership inode: %v", err)
	}
}
