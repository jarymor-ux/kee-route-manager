// Package control owns the controller runtime. UI and CLI do not import it.
package control

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

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
	logs, e := logging.Setup(c.Paths.LogFile)
	if e != nil {
		return e
	}
	defer logs.Close()
	log.Printf("controller owner pid=%d instance=%s process_start=%s", owner.Owner.PID, owner.Owner.InstanceID, owner.Owner.ProcessStart)
	listener, e := ListenUnix(c.API.UnixSocket)
	if e != nil {
		return e
	}
	defer listener.Close()
	defer os.Remove(c.API.UnixSocket)
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
	mgr.Start(ctx)
	defer mgr.Stop()
	local := &http.Server{Handler: srv.LocalHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10}
	errorsCh := make(chan error, 2)
	go func() {
		e := local.Serve(listener)
		if errors.Is(e, http.ErrServerClosed) {
			e = nil
		}
		errorsCh <- e
	}()
	if c.API.Enabled {
		go func() { errorsCh <- srv.ListenAndServe(ctx) }()
	}
	log.Printf("controller %s control_socket=%s api_enabled=%t", version, c.API.UnixSocket, c.API.Enabled)
	var result error
	select {
	case <-ctx.Done():
	case result = <-errorsCh:
	}
	cancel()
	cc, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	_ = local.Shutdown(cc)
	return result
}

// ListenUnix creates a local authorization boundary. It may only be called
// after acquiring the daemon's state lock.
func ListenUnix(path string) (net.Listener, error) {
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
