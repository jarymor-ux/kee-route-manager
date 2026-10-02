package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotationBoundsAndPrivateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	w, err := New(path, 64, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err = w.Write([]byte(strings.Repeat("x", 32))); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) > 4 {
		t.Fatalf("too many logs: %v", files)
	}
	for _, f := range files {
		s, e := os.Stat(f)
		if e != nil {
			t.Fatal(e)
		}
		if s.Size() > 64 {
			t.Fatalf("oversize %s: %d", f, s.Size())
		}
		if s.Mode().Perm() != 0600 {
			t.Fatalf("mode %s", s.Mode())
		}
	}
}
func TestHugeSingleWriteIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	w, e := New(path, 64, 3)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	b := []byte(strings.Repeat("z", 4096))
	n, e := w.Write(b)
	if e != nil || n != len(b) {
		t.Fatalf("%d %v", n, e)
	}
	s, e := os.Stat(path)
	if e != nil || s.Size() > 64 {
		t.Fatalf("unbounded %v %v", s, e)
	}
}
func TestRefusesSymlink(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	target := filepath.Join(t.TempDir(), "secret")
	if e := os.WriteFile(target, []byte("unchanged"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, p); e != nil {
		t.Fatal(e)
	}
	if w, e := New(p, 64, 3); e == nil {
		w.Close()
		t.Fatal("accepted symlink")
	}
}

func TestRuntimeWriterRedactsBeforeBothOutputs(t *testing.T) {
	var output bytes.Buffer
	writer := redactingWriter{target: &output}
	raw := []byte("subscription https://user:pass@example.invalid/sub?token=hidden Authorization: secret\n")
	n, e := writer.Write(raw)
	if e != nil || n != len(raw) {
		t.Fatalf("write %d %v", n, e)
	}
	for _, secret := range []string{"user:pass", "hidden", "secret"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("leaked %q: %s", secret, output.String())
		}
	}
}
