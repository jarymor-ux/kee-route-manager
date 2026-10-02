package logging

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupPersistsRedactedRuntimeLog(t *testing.T) {
	previous := log.Writer()
	t.Cleanup(func() { log.SetOutput(previous) })
	path := filepath.Join(t.TempDir(), "private", "runtime.log")
	closer, err := Setup(path)
	if err != nil {
		t.Fatal(err)
	}
	log.Print(`provider failed password="SYNTHETIC PASSWORD TAIL" https://user:pass@provider.invalid/PRIVATE_TOKEN`)
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SYNTHETIC", "PASSWORD TAIL", "user:pass", "PRIVATE_TOKEN"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatalf("credential %q persisted", secret)
		}
	}
	if !bytes.Contains(b, []byte("provider failed")) {
		t.Fatal("diagnostic category lost")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("log not private: %v %v", info, err)
	}
}

func TestDisabledSetupPreservesExistingOutput(t *testing.T) {
	previous := log.Writer()
	t.Cleanup(func() { log.SetOutput(previous) })
	var output bytes.Buffer
	log.SetOutput(&output)
	closer, err := Setup("")
	if err != nil {
		t.Fatal(err)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	log.Print("still logging")
	if !strings.Contains(output.String(), "still logging") {
		t.Fatal("disabled file output discarded the existing runtime logger")
	}
}

func TestInvalidRetentionAndUnwritableDirectoryFailBeforeUse(t *testing.T) {
	for _, tc := range []struct {
		limit   int64
		backups int
	}{{0, 1}, {-1, 1}, {64, 0}, {64, 11}} {
		if w, err := New(filepath.Join(t.TempDir(), "log"), tc.limit, tc.backups); err == nil {
			_ = w.Close()
			t.Errorf("invalid retention accepted: %+v", tc)
		}
	}
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(filepath.Join(path, "log")); err == nil {
		t.Fatal("invalid parent directory accepted")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "keep" {
		t.Fatalf("setup damaged existing file: %q %v", got, err)
	}
}
