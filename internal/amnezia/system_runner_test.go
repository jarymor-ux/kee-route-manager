package amnezia

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemRunnerPrefersOfficialAWGUserspaceEngine(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "awg-quick")
	result := filepath.Join(dir, "runner.txt")
	body := "#!/bin/sh\nprintf '%s\\n' \"$WG_QUICK_USERSPACE_IMPLEMENTATION\" > \"" + result + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	if err := (SystemRunner{}).Run(context.Background(), "awg-quick", "up", "/tmp/safe.conf"); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(result)
	if err != nil || strings.TrimSpace(string(bytes)) != "amneziawg-go" {
		t.Fatalf("unexpected userspace runtime %q %v", bytes, err)
	}
	if err := (SystemRunner{}).Run(context.Background(), "sh", "-c", "touch /tmp/bad"); err == nil {
		t.Fatal("shell execution accepted")
	}
}
