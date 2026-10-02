// Package control owns the controller runtime. UI and CLI do not import it.
package control

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
	"github.com/jarymor-ux/kee-route-manager/internal/logging"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
)

func Serve(ctx context.Context, c config.Config, version string) error {
	if c.Instance.Role != "controller" {
		return fmt.Errorf("daemon requires instance.role=controller")
	}
	// The lock is anchored to state, not run_dir: another configuration cannot
	// evade ownership by merely selecting a different socket/run directory.
	owner, e := daemonlock.Acquire(filepath.Join(c.Paths.StateDir, "daemon.lock"))
	if e != nil {
		return e
	}
	defer owner.Close()
	tunnelOwner, e := daemonlock.Acquire(filepath.Join(c.Xray.ConfigDir, ".krm-daemon.lock"))
	if e != nil {
		return fmt.Errorf("tunnel ownership: %w", e)
	}
	defer tunnelOwner.Close()
	if nonce := os.Getenv("KRM_UPDATE_TRIAL"); nonce != "" {
		activated, err := serveTrial(ctx, c, version, nonce)
		if err != nil || !activated {
			return err
		}
	}
	return serveActive(ctx, c, version)
}

// Called with both ownership locks still held, including across trial activation.
func serveActive(ctx context.Context, c config.Config, version string) error {
	logs, e := logging.Setup(c.Paths.LogFile)
	if e != nil {
		return e
	}
	defer logs.Close()
	log.Printf("controller owner pid=%d", os.Getpid())
	p, r, e := platform.New(c)
	if e != nil {
		return e
	}
	st, e := store.New(c.Paths.StateDir, c.Paths.CacheDir, model.NewState(version, c.Xray.SlotTagPrefix, c.Pool.Size))
	if e != nil {
		return e
	}
	ops, e := operation.New(c.Paths.StateDir)
	if e != nil {
		return e
	}
	xm := xray.NewManager(c, r, p)
	mgr := core.New(c, version, st, ops, p, xm, subscription.New(c.Subscriptions, c.Health.ProviderRetryBackoff, c.Paths.CacheDir), bench.New(c, xray.NewBatchRunner(c)))
	srv, e := web.New(c, mgr, update.New(c.Update, c.Paths.StateDir, version), nil)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer mgr.Stop()
	gate := &updateGate{manager: mgr, version: version}
	log.Printf("controller %s control_socket=%s api_enabled=%t", version, c.API.UnixSocket, c.API.Enabled)
	_, e = serveHTTP(ctx, c, gate.local(srv.LocalHandler()), gate.public(srv.Handler()), true, nil, func() { mgr.Start(ctx) })
	return e
}

// ListenUnix creates a local authorization boundary. It may only be called
// after acquiring the daemon's state lock.
type lockedListener struct {
	net.Listener
	owner    *daemonlock.Lock
	once     sync.Once
	closeErr error
}

func (l *lockedListener) Close() error {
	l.once.Do(func() {
		l.closeErr = l.Listener.Close()
		if e := l.owner.Close(); l.closeErr == nil {
			l.closeErr = e
		}
	})
	return l.closeErr
}
func ListenUnix(path string) (net.Listener, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("API unix_socket must be absolute")
	}
	owner, e := daemonlock.Acquire(path + ".lock")
	if e != nil {
		return nil, fmt.Errorf("control socket ownership: %w", e)
	}
	listener, e := listenUnix(path)
	if e != nil {
		_ = owner.Close()
		return nil, e
	}
	return &lockedListener{Listener: listener, owner: owner}, nil
}
func listenUnix(path string) (net.Listener, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("API unix_socket must be absolute")
	}
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	info, e := os.Lstat(dir)
	if e != nil {
		return nil, e
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("control socket directory must be owned by this user with owner-only permissions")
	}
	if info, e = os.Lstat(path); e == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket control path")
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	l, e := net.Listen("unix", path)
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(path, 0600); e != nil {
		_ = l.Close()
		_ = os.Remove(path)
		return nil, e
	}
	return l, nil
}
