package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control/cli"
	"github.com/jarymor-ux/kee-route-manager/internal/launcher"
)

var version = "1.1.0-rc.1"
var commit = "dev"
var buildTime = "unknown"

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	if args[0] == "version" || args[0] == "--version" {
		cli.Version("kee-route-manager-launcher", version, commit, buildTime)
		return nil
	}
	if args[0] != "serve" && args[0] != "install" {
		return fmt.Errorf("usage: kee-route-manager-launcher <serve|install|version> --config PATH")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	configPath := f.String("config", "", "controller config path")
	ui := f.String("ui-config", "", "optional co-located UI config")
	release := f.String("release-dir", "", "signed bootstrap release directory")
	bin := f.String("bin-dir", "", "standard executable directory")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || *configPath == "" {
		return fmt.Errorf("an explicit --config path is required")
	}
	abs, err := filepath.Abs(*configPath)
	if err != nil {
		return err
	}
	if args[0] == "install" {
		if *release == "" {
			return fmt.Errorf("install requires --release-dir")
		}
		if *bin == "" {
			c, err := config.Load(abs)
			if err != nil {
				return err
			}
			*bin = "/usr/local/bin"
			if c.Platform.Kind == "keenetic" {
				*bin = "/opt/bin"
			} else if c.Platform.Kind == "openwrt" {
				*bin = "/usr/bin"
			}
		}
		return launcher.Install(abs, *ui, *release, *bin)
	}
	if *ui != "" || *release != "" || *bin != "" {
		return fmt.Errorf("bootstrap flags are only valid with install")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return launcher.Serve(ctx, abs)
}
