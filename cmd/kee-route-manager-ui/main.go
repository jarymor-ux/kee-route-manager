package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/ui"
)

var version = "1.0.0-rc.2"
var commit = "dev"
var buildTime = "unknown"

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC | log.Lmicroseconds)
	flags := flag.NewFlagSet("kee-route-manager-ui", flag.ExitOnError)
	configPath := flags.String("config", "/etc/kee-route-manager-ui/config.yaml", "config path")
	showVersion := flags.Bool("version", false, "show version")
	_ = flags.Parse(os.Args[1:])
	if *showVersion {
		fmt.Printf("Kee Route Manager UI %s (%s, %s, %s/%s)\n", version, commit, buildTime, runtime.GOOS, runtime.GOARCH)
		return
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if cfg.Instance.Role != "ui-proxy" {
		log.Fatal("instance.role must be ui-proxy")
	}
	handler, err := ui.ProxyHandler(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	log.Printf("Kee Route Manager UI %s listening on %s", version, cfg.Web.Listen)
	if err = ui.ListenAndServe(ctx, cfg, handler); err != nil {
		log.Fatal(err)
	}
}
