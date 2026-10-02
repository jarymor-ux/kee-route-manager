package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
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
	"github.com/jarymor-ux/kee-route-manager/internal/instance"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
)

var version = "1.0.0-rc.2"
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
		fmt.Printf("Kee Route Manager Core %s (%s, %s, %s/%s)\n", version, commit, buildTime, runtime.GOOS, runtime.GOARCH)
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
	fmt.Fprintln(os.Stderr, `Kee Route Manager Core

Usage:
  kee-route-manager serve [--config PATH]
  kee-route-manager validate [--config PATH]
  kee-route-manager passwd [--config PATH] --username NAME --password-stdin
  kee-route-manager benchmark [--config PATH]
  kee-route-manager status [--config PATH]
  kee-route-manager doctor [--config PATH]
  kee-route-manager restore-xray [--config PATH]
  kee-route-manager update-check [--config PATH]
  kee-route-manager update-apply [--config PATH]
  kee-route-manager version

The UI is a separate kee-route-manager-ui process.`)
}

func defaultConfigPath() string {
	if _, err := os.Stat("/opt/etc/ndm"); err == nil {
		return "/opt/etc/kee-route-manager/config.yaml"
	}
	return "/etc/kee-route-manager/config.yaml"
}

func loadConfig(args []string, name string) (config.Config, string, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	path := flags.String("config", defaultConfigPath(), "config path")
	if err := flags.Parse(args); err != nil {
		return config.Config{}, "", err
	}
	if flags.NArg() != 0 {
		return config.Config{}, "", fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	cfg, err := config.Load(*path)
	return cfg, *path, err
}

func validate(args []string) error {
	cfg, path, err := loadConfig(args, "validate")
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg.Sanitized(), "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Configuration is valid: %s\n%s\n", path, data)
	return nil
}

func passwd(args []string) error {
	flags := flag.NewFlagSet("passwd", flag.ContinueOnError)
	path := flags.String("config", defaultConfigPath(), "config path")
	username := flags.String("username", "", "username")
	passwordStdin := flags.Bool("password-stdin", false, "read password from stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*passwordStdin {
		return fmt.Errorf("--password-stdin is required")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(io.LimitReader(os.Stdin, 2049))
	password, err := reader.ReadString('\n')
	if err != nil && len(password) == 0 {
		return err
	}
	password = strings.TrimRight(password, "\r\n")
	return auth.CreateCredentials(cfg.Web.CredentialsFile, *username, password)
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

func build(cfg config.Config) (*runtimeBundle, error) {
	if err := os.MkdirAll(cfg.Paths.RunDir, 0o700); err != nil {
		return nil, err
	}
	adapter, runner, err := platform.New(cfg)
	if err != nil {
		return nil, err
	}
	initial := model.NewState(version, cfg.Xray.SlotTagPrefix, cfg.Pool.Size)
	stateStore, err := store.New(cfg.Paths.StateDir, cfg.Paths.CacheDir, initial)
	if err != nil {
		return nil, err
	}
	operations, err := operation.New(cfg.Paths.StateDir)
	if err != nil {
		return nil, err
	}
	xrayManager := xray.NewManager(cfg, runner, adapter)
	fetcher := subscription.New(cfg.Subscriptions, cfg.Health.ProviderRetryBackoff, cfg.Paths.CacheDir)
	benchmark := bench.New(cfg, xray.NewBatchRunner(cfg))
	manager := core.New(cfg, version, stateStore, operations, adapter, xrayManager, fetcher, benchmark)
	updater := update.New(cfg.Update, cfg.Paths.StateDir, version)
	return &runtimeBundle{cfg, adapter, runner, stateStore, operations, xrayManager, manager, updater}, nil
}

func serve(args []string) error {
	cfg, _, err := loadConfig(args, "serve")
	if err != nil {
		return err
	}
	if cfg.Instance.Role != "controller" {
		return fmt.Errorf("instance.role must be controller for serve")
	}
	lock, err := instance.Acquire(cfg.Paths.RunDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	bundle, err := build(cfg)
	if err != nil {
		return err
	}
	rolledBack, err := bundle.updater.PrepareStartup()
	if err != nil {
		return fmt.Errorf("update recovery: %w", err)
	}
	if rolledBack {
		executable, execErr := os.Executable()
		if execErr != nil {
			return execErr
		}
		return syscall.Exec(executable, os.Args, os.Environ())
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	bundle.manager.Start(ctx)
	defer bundle.manager.Stop()
	go updateLoop(ctx, bundle)
	if cfg.Update.Enabled {
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(cfg.Update.HealthGracePeriod.Duration):
				if bundle.manager.Ready() {
					if markErr := bundle.updater.MarkHealthy(); markErr != nil {
						log.Printf("update health mark: %v", markErr)
					}
				}
			}
		}()
	}
	if !cfg.Web.Enabled {
		log.Printf("Kee Route Manager Core %s running without HTTP API (%s)", version, cfg.Platform.Kind)
		<-ctx.Done()
		return nil
	}
	server, err := web.New(cfg, bundle.manager, bundle.updater, bundle.platform.RestartKRM)
	if err != nil {
		return err
	}
	log.Printf("Kee Route Manager Core %s API listening on %s (%s)", version, cfg.Web.Listen, cfg.Platform.Kind)
	return server.ListenAndServe(ctx)
}

func updateLoop(ctx context.Context, bundle *runtimeBundle) {
	if !bundle.cfg.Update.Enabled {
		return
	}
	interval := bundle.cfg.Update.CheckInterval.Duration
	if interval < time.Hour {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := bundle.updater.Check(ctx)
			if err != nil {
				log.Printf("update check: %v", err)
				continue
			}
			if result.Available {
				log.Printf("update available: %s", result.LatestVersion)
				if bundle.cfg.Update.AutoApply {
					if _, err = bundle.updater.Apply(ctx, result); err != nil {
						log.Printf("update apply: %v", err)
						continue
					}
					time.Sleep(time.Second)
					_ = bundle.platform.RestartKRM(context.Background())
					return
				}
			}
		}
	}
}

func runBenchmark(args []string) error {
	cfg, _, err := loadConfig(args, "benchmark")
	if err != nil {
		return err
	}
	lock, err := instance.Acquire(cfg.Paths.RunDir)
	if err != nil {
		return fmt.Errorf("benchmark must be started through the running core API: %w", err)
	}
	defer lock.Close()
	bundle, err := build(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err = bundle.manager.RunBenchmark(ctx, "manual", "cli"); err != nil {
		return err
	}
	output, err := json.MarshalIndent(bundle.store.State().LastBenchmark, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(output))
	return nil
}

func status(args []string) error {
	cfg, _, err := loadConfig(args, "status")
	if err != nil {
		return err
	}
	var state model.State
	statePath := filepath.Join(cfg.Paths.StateDir, "state.json")
	if data, readErr := os.ReadFile(statePath); readErr == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode %s: %w", statePath, err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	} else {
		state = model.NewState(version, cfg.Xray.SlotTagPrefix, cfg.Pool.Size)
	}
	var current any
	operationPath := filepath.Join(cfg.Paths.StateDir, "operation.json")
	if data, readErr := os.ReadFile(operationPath); readErr == nil {
		_ = json.Unmarshal(data, &current)
	}
	output, err := json.MarshalIndent(map[string]any{
		"version":   version,
		"platform":  cfg.Platform.Kind,
		"state":     state,
		"operation": current,
	}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(output))
	return nil
}

func doctor(args []string) error {
	cfg, path, err := loadConfig(args, "doctor")
	if err != nil {
		return err
	}
	fmt.Printf("config: %s\nplatform: %s\narch: %s/%s\n", path, cfg.Platform.Kind, runtime.GOOS, runtime.GOARCH)
	checks := []struct {
		name string
		path string
	}{{"xray", cfg.Xray.Binary}, {"config-dir", cfg.Xray.ConfigDir}, {"managed-dir", cfg.Xray.ManagedDir}}
	failed := false
	for _, check := range checks {
		if _, statErr := os.Stat(check.path); statErr != nil {
			fmt.Printf("FAIL %-14s %s: %v\n", check.name, check.path, statErr)
			failed = true
		} else {
			fmt.Printf("OK   %-14s %s\n", check.name, check.path)
		}
	}
	if _, lookupErr := exec.LookPath(cfg.Xray.Binary); lookupErr != nil && !filepath.IsAbs(cfg.Xray.Binary) {
		failed = true
		fmt.Printf("FAIL xray lookup: %v\n", lookupErr)
	}
	if failed {
		return fmt.Errorf("doctor found failures")
	}
	return nil
}

func restoreXray(args []string) error {
	cfg, _, err := loadConfig(args, "restore-xray")
	if err != nil {
		return err
	}
	lock, err := instance.Acquire(cfg.Paths.RunDir)
	if err != nil {
		return fmt.Errorf("stop the controller before restore: %w", err)
	}
	defer lock.Close()
	bundle, err := build(cfg)
	if err != nil {
		return err
	}
	return bundle.manager.RestoreOriginalXray(context.Background())
}

func updateCheck(args []string, apply bool) error {
	cfg, _, err := loadConfig(args, "update")
	if err != nil {
		return err
	}
	var lock *instance.Lock
	if apply {
		lock, err = instance.Acquire(cfg.Paths.RunDir)
		if err != nil {
			return fmt.Errorf("apply updates through the running core API: %w", err)
		}
		defer lock.Close()
	}
	updater := update.New(cfg.Update, cfg.Paths.StateDir, version)
	result, err := updater.Check(context.Background())
	if err != nil {
		return err
	}
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(output))
	if !apply || !result.Available {
		return nil
	}
	pending, err := updater.Apply(context.Background(), result)
	if err != nil {
		return err
	}
	output, err = json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(output))
	adapter, _, err := platform.New(cfg)
	if err != nil {
		return fmt.Errorf("update installed but service restart setup failed: %w", err)
	}
	time.Sleep(time.Second)
	if err = adapter.RestartKRM(context.Background()); err != nil {
		return fmt.Errorf("update installed but service restart failed: %w", err)
	}
	return nil
}
