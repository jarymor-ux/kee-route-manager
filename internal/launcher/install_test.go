package launcher

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
)

func TestInstallPreflightsAllEntrypointsAndCanResumeMatchingBootstrap(t *testing.T) {
	f := newReleaseFixture(t)
	conflict := filepath.Join(f.bin, "kee-route-managerctl.launcher-new")
	if err := os.WriteFile(conflict, []byte("operator file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Install(f.configFile, "", f.seed, f.bin); err == nil {
		t.Fatal("preexisting temporary entry accepted")
	}
	for _, path := range []string{filepath.Join(f.c.Update.InstallDir, "launcher.json"), filepath.Join(f.c.Update.InstallDir, "current"), filepath.Join(f.bin, "kee-route-managerd")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("failed install partially committed %s", path)
		}
	}
	if data, _ := os.ReadFile(conflict); string(data) != "operator file" {
		t.Fatal("preflight overwrote unrelated file")
	}
	if err := os.Remove(conflict); err != nil {
		t.Fatal(err)
	}
	if err := Install(f.configFile, "", f.seed, f.bin); err != nil {
		t.Fatal(err)
	}
	// Simulate power loss after replacing just one of the standard entrypoints.
	if err := os.Remove(filepath.Join(f.bin, "kee-route-managerctl")); err != nil {
		t.Fatal(err)
	}
	if err := Install(f.configFile, "", f.seed, f.bin); err != nil {
		t.Fatalf("matching interrupted bootstrap cannot resume: %v", err)
	}
	for _, name := range []string{"kee-route-managerd", "kee-route-managerctl"} {
		target, err := os.Readlink(filepath.Join(f.bin, name))
		if err != nil || target != filepath.Join(f.c.Update.InstallDir, "current", name) {
			t.Fatalf("incorrect installed entrypoint %s: %q %v", name, target, err)
		}
	}
	r, err := loadRecord(f.c.Update.InstallDir)
	if err != nil || r.Active != "1.0.0" || r.LastResult != "installed" {
		t.Fatalf("seed not committed: %+v %v", r, err)
	}
	// Resume is restricted to bootstrap, never a way to reset an updated system.
	r.LastResult = "updated"
	if err = saveRecord(f.c.Update.InstallDir, r); err != nil {
		t.Fatal(err)
	}
	if err = Install(f.configFile, "", f.seed, f.bin); err == nil {
		t.Fatal("bootstrap replaced an already updated installation")
	}
}

func TestInstallRefusesLiveStateOrTunnelOwnerAndUntrustedExistingBinary(t *testing.T) {
	for _, mode := range []string{"state-owner", "tunnel-owner", "different-binary", "symlink-binary"} {
		t.Run(mode, func(t *testing.T) {
			f := newReleaseFixture(t)
			switch mode {
			case "state-owner", "tunnel-owner":
				path := filepath.Join(f.c.Paths.StateDir, "daemon.lock")
				if mode == "tunnel-owner" {
					path = filepath.Join(f.c.Xray.ConfigDir, ".krm-daemon.lock")
				}
				owner, err := daemonlock.Acquire(path)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
			case "different-binary":
				if err := os.WriteFile(filepath.Join(f.bin, "kee-route-managerd"), []byte("unverified binary"), 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink-binary":
				if err := os.Symlink(filepath.Join(f.seed, "daemon"), filepath.Join(f.bin, "kee-route-managerd")); err != nil {
					t.Fatal(err)
				}
			}
			if err := Install(f.configFile, "", f.seed, f.bin); err == nil {
				t.Fatalf("installation accepted %s", mode)
			}
			if _, err := os.Stat(filepath.Join(f.c.Update.InstallDir, "launcher.json")); !os.IsNotExist(err) {
				t.Fatal("failed installation committed active record")
			}
		})
	}
}
