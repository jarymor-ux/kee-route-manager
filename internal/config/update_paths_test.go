package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLauncherPathsAreSeparateFromMutableControllerState(t *testing.T) {
	c := validConfig(t)
	if c.Update.InstallDir != c.Paths.StateDir+"-updates" || c.Update.LauncherSocket != filepath.Join(c.Paths.RunDir, "launcher.sock") {
		t.Fatalf("missing safe platform defaults: %+v", c.Update)
	}
	for _, path := range []string{c.Paths.StateDir, filepath.Join(c.Paths.StateDir, "slots"), filepath.Dir(c.Paths.StateDir), c.Paths.CacheDir, c.Paths.RunDir, c.Xray.ConfigDir, "relative", "/tmp/nul\x00"} {
		t.Run(path, func(t *testing.T) {
			bad := c
			bad.Update.InstallDir = path
			if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "update.install_dir") {
				t.Fatalf("unsafe update directory accepted: %v", err)
			}
		})
	}
	for _, path := range []string{c.API.UnixSocket, "relative", filepath.Join(filepath.Dir(c.Paths.RunDir), "outside.sock")} {
		bad := c
		bad.Update.LauncherSocket = path
		if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "update.launcher_socket") {
			t.Fatalf("unsafe update socket accepted: %v", err)
		}
	}
	link := filepath.Join(t.TempDir(), "state-link")
	if err := os.MkdirAll(c.Paths.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(c.Paths.StateDir, link); err != nil {
		t.Fatal(err)
	}
	c.Update.InstallDir = filepath.Join(link, "updates")
	if err := c.Validate(); err == nil {
		t.Fatal("symlink overlap accepted")
	}
}
