package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/bench"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
)

var version = "1.0.0-rc.1"
var commit = "dev"
var buildTime = "unknown"

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC | log.Lmicroseconds)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "ui-proxy":
		err = serveProxy(os.Args[2:])
	case "validate":
		err = validate(os.Args[2:])
	case "passwd":
		err = passwd(os.Args[2:])
	case "benchmark":
		err = runBenchmark(os.Args[2:])
	case "status":
		err = status(os.Args[2:])
	case "doctor":
		err = doctor(os.Args[2:])
	case "restore-xray":
		err = restoreXray(os.Args[2:])
	case "update-check":
		err = updateCheck(os.Args[2:], false)
	case "update-apply":
		err = updateCheck(os.Args[2:], true)
	case "version", "--version", "-version":
		fmt.Printf("Kee Route Manager %s (%s, %s, %s/%s)\n", version, commit, buildTime, runtime.GOOS, runtime.GOARCH)
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Printf("ERROR: %v", err)
		os.Exit(1)
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, `Kee Route Manager

Usage:
  kee-route-manager serve [--config PATH]
  kee-route-manager ui-proxy [--config PATH]
  kee-route-manager validate [--config PATH]
  kee-route-manager passwd [--config PATH] --username NAME --password-stdin
  kee-route-manager benchmark [--config PATH]
  kee-route-manager status [--config PATH]
  kee-route-manager doctor [--config PATH]
  kee-route-manager restore-xray [--config PATH]
  kee-route-manager update-check [--config PATH]
  kee-route-manager update-apply [--config PATH]
  kee-route-manager version`)
}
func configFlag(args []string, name string) (config.Config, string, error) {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	path := f.String("config", defaultConfigPath(), "config path")
	if e := f.Parse(args); e != nil {
		return config.Config{}, "", e
	}
	c, e := config.Load(*path)
	return c, *path, e
}
func defaultConfigPath() string {
	if _, e := os.Stat("/opt/etc/ndm"); e == nil {
		return "/opt/etc/kee-route-manager/config.yaml"
	}
	return "/etc/kee-route-manager/config.yaml"
}
func validate(args []string) error {
	c, p, e := configFlag(args, "validate")
	if e != nil {
		return e
	}
	b, _ := json.MarshalIndent(c.Sanitized(), "", "  ")
	fmt.Printf("Configuration is valid: %s\n%s\n", p, b)
	return nil
}
func passwd(args []string) error {
	f := flag.NewFlagSet("passwd", flag.ContinueOnError)
	path := f.String("config", defaultConfigPath(), "config path")
	user := f.String("username", "", "username")
	stdin := f.Bool("password-stdin", false, "read password from stdin")
	if e := f.Parse(args); e != nil {
		return e
	}
	if !*stdin {
		return fmt.Errorf("--password-stdin is required")
	}
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	r := bufio.NewReader(io.LimitReader(os.Stdin, 2049))
	password, e := r.ReadString('\n')
	if e != nil && len(password) == 0 {
		return e
	}
	password = strings.TrimRight(password, "\r\n")
	return auth.CreateCredentials(c.Web.CredentialsFile, *user, password)
}

type runtimeBundle struct {
	cfg      config.Config
	platform platform.Adapter
	runner   platform.Runner
	store    *store.Store
	ops      *operation.Coordinator
	xray     *xray.Manager
	manager  *core.Manager
	updater  *update.Updater
}

func build(c config.Config) (*runtimeBundle, error) {
	if e := os.MkdirAll(c.Paths.RunDir, 0700); e != nil {
		return nil, e
	}
	p, r, e := platform.New(c)
	if e != nil {
		return nil, e
	}
	initial := model.NewState(version, c.Xray.SlotTagPrefix, c.Pool.Size)
	st, e := store.New(c.Paths.StateDir, c.Paths.CacheDir, initial)
	if e != nil {
		return nil, e
	}
	ops, e := operation.New(c.Paths.StateDir)
	if e != nil {
		return nil, e
	}
	xm := xray.NewManager(c, r, p)
	fetch := subscription.New(c.Subscriptions, c.Health.ProviderRetryBackoff, c.Paths.CacheDir)
	be := bench.New(c, xray.NewBatchRunner(c))
	mgr := core.New(c, version, st, ops, p, xm, fetch, be)
	up := update.New(c.Update, c.Paths.StateDir, version)
	return &runtimeBundle{c, p, r, st, ops, xm, mgr, up}, nil
}
func serve(args []string) error {
	c, _, e := configFlag(args, "serve")
	if e != nil {
		return e
	}
	if c.Instance.Role != "controller" {
		return fmt.Errorf("instance.role must be controller for serve")
	}
	b, e := build(c)
	if e != nil {
		return e
	}
	rolled, e := b.updater.PrepareStartup()
	if e != nil {
		return fmt.Errorf("update recovery: %w", e)
	}
	if rolled {
		exe, _ := os.Executable()
		return syscall.Exec(exe, os.Args, os.Environ())
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	b.manager.Start(ctx)
	defer b.manager.Stop()
	srv, e := web.New(c, b.manager, b.updater, b.platform.RestartKRM)
	if e != nil {
		return e
	}
	go updateLoop(ctx, b)
	if c.Update.Enabled {
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.Update.HealthGracePeriod.Duration):
				if e := b.updater.MarkHealthy(); e != nil {
					log.Printf("update health mark: %v", e)
				}
			}
		}()
	}
	log.Printf("Kee Route Manager %s listening on %s (%s)", version, c.Web.Listen, c.Platform.Kind)
	return srv.ListenAndServe(ctx)
}
func updateLoop(ctx context.Context, b *runtimeBundle) {
	if !b.cfg.Update.Enabled {
		return
	}
	interval := b.cfg.Update.CheckInterval.Duration
	if interval < time.Hour {
		interval = time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r, e := b.updater.Check(ctx)
			if e != nil {
				log.Printf("update check: %v", e)
				continue
			}
			if r.Available {
				log.Printf("update available: %s", r.LatestVersion)
				if b.cfg.Update.AutoApply {
					if _, e = b.updater.Apply(ctx, r); e != nil {
						log.Printf("update apply: %v", e)
						continue
					}
					time.Sleep(time.Second)
					_ = b.platform.RestartKRM(context.Background())
					return
				}
			}
		}
	}
}
func serveProxy(args []string) error {
	c, _, e := configFlag(args, "ui-proxy")
	if e != nil {
		return e
	}
	if c.Instance.Role != "ui-proxy" {
		return fmt.Errorf("instance.role must be ui-proxy")
	}
	handler, e := web.ProxyHandler(c)
	if e != nil {
		return e
	}
	if e = web.EnsureTLS(c.Web.TLS, c.Web.Listen); e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	srv := &http.Server{Addr: c.Web.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		cc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(cc)
	}()
	if c.Web.TLS.Enabled {
		e = srv.ListenAndServeTLS(c.Web.TLS.CertFile, c.Web.TLS.KeyFile)
	} else {
		e = srv.ListenAndServe()
	}
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
func runBenchmark(args []string) error {
	c, _, e := configFlag(args, "benchmark")
	if e != nil {
		return e
	}
	b, e := build(c)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if e = b.manager.RunBenchmark(ctx, "manual", "cli"); e != nil {
		return e
	}
	out, _ := json.MarshalIndent(b.store.State().LastBenchmark, "", "  ")
	fmt.Println(string(out))
	return nil
}
func status(args []string) error {
	c, _, e := configFlag(args, "status")
	if e != nil {
		return e
	}
	b, e := build(c)
	if e != nil {
		return e
	}
	out, _ := json.MarshalIndent(b.manager.Status(context.Background()), "", "  ")
	fmt.Println(string(out))
	return nil
}
func doctor(args []string) error {
	c, p, e := configFlag(args, "doctor")
	if e != nil {
		return e
	}
	fmt.Printf("config: %s\nplatform: %s\narch: %s/%s\n", p, c.Platform.Kind, runtime.GOOS, runtime.GOARCH)
	checks := []struct{ name, path string }{{"xray", c.Xray.Binary}, {"config-dir", c.Xray.ConfigDir}, {"managed-dir", c.Xray.ManagedDir}}
	failed := false
	for _, x := range checks {
		if _, e := os.Stat(x.path); e != nil {
			fmt.Printf("FAIL %-14s %s: %v\n", x.name, x.path, e)
			failed = true
		} else {
			fmt.Printf("OK   %-14s %s\n", x.name, x.path)
		}
	}
	if _, e := exec.LookPath(c.Xray.Binary); e != nil && !filepath.IsAbs(c.Xray.Binary) {
		failed = true
		fmt.Printf("FAIL xray lookup: %v\n", e)
	}
	if failed {
		return fmt.Errorf("doctor found failures")
	}
	return nil
}
func restoreXray(args []string) error {
	c, _, e := configFlag(args, "restore-xray")
	if e != nil {
		return e
	}
	b, e := build(c)
	if e != nil {
		return e
	}
	return b.manager.RestoreOriginalXray(context.Background())
}

func updateCheck(args []string, apply bool) error {
	c, _, e := configFlag(args, "update")
	if e != nil {
		return e
	}
	u := update.New(c.Update, c.Paths.StateDir, version)
	r, e := u.Check(context.Background())
	if e != nil {
		return e
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	if apply && r.Available {
		pending, e := u.Apply(context.Background(), r)
		if e != nil {
			return e
		}
		b, _ = json.MarshalIndent(pending, "", "  ")
		fmt.Println(string(b))
		adapter, _, e := platform.New(c)
		if e != nil {
			return fmt.Errorf("update installed but service restart setup failed: %w", e)
		}
		time.Sleep(time.Second)
		if e = adapter.RestartKRM(context.Background()); e != nil {
			return fmt.Errorf("update installed but service restart failed: %w", e)
		}
	}
	return nil
}
