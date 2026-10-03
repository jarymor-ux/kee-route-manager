package control

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixSocketPermissionsAndNonSocketProtection(t *testing.T) {
	d, e := os.MkdirTemp("", "krm-sock-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	path := filepath.Join(d, "control.sock")
	l, e := ListenUnix(path)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("socket mode=%o", info.Mode().Perm())
	}
	protected := filepath.Join(d, "file")
	if e = os.WriteFile(protected, []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ListenUnix(protected); e == nil {
		t.Fatal("replaced normal file")
	}
}
func TestRejectExposedSocketDirectory(t *testing.T) {
	d := filepath.Join(t.TempDir(), "public")
	if e := os.Mkdir(d, 0755); e != nil {
		t.Fatal(e)
	}
	// The signed release workflow uses umask 077. Make this intentionally
	// exposed fixture explicit instead of relying on creation permissions.
	if e := os.Chmod(d, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := ListenUnix(filepath.Join(d, "control.sock")); e == nil {
		t.Fatal("accepted public socket directory")
	}
}
func TestSecondDaemonStopsBeforeInitialization(t *testing.T) {
	c := config.Default()
	c.Paths.StateDir = t.TempDir()
	c.Paths.CacheDir = filepath.Join(c.Paths.StateDir, "untouched-cache")
	c.API.UnixSocket = filepath.Join(c.Paths.StateDir, "untouched-run", "socket")
	c.Platform.Kind = "invalid-platform"
	owner, e := daemonlock.Acquire(filepath.Join(c.Paths.StateDir, "daemon.lock"))
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Close()
	e = Serve(context.Background(), c, "test")
	if e == nil || !strings.Contains(e.Error(), "another controller") {
		t.Fatalf("lock not acquired first: %v", e)
	}
	if _, e = os.Stat(c.Paths.CacheDir); !os.IsNotExist(e) {
		t.Fatal("second daemon touched state/cache")
	}
	if _, e = os.Stat(filepath.Dir(c.API.UnixSocket)); !os.IsNotExist(e) {
		t.Fatal("second daemon created socket directory")
	}
}

func TestLiveSocketCannotBeReplaced(t *testing.T) {
	d, e := os.MkdirTemp("", "krm-live-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(d)
	path := filepath.Join(d, "socket")
	l, e := ListenUnix(path)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if _, e = ListenUnix(path); e == nil {
		t.Fatal("second owner replaced live socket")
	}
	if _, e = os.Lstat(path); e != nil {
		t.Fatal("second owner removed original socket")
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := ListenUnix(path)
	if e != nil {
		t.Fatalf("rebind after release: %v", e)
	}
	defer next.Close()
}
