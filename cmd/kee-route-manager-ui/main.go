package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/web/ui"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

var version = "1.1.0-rc.7"
var commit = "dev"
var buildTime = "unknown"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"serve"}
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-version" {
		fmt.Printf("kee-route-manager-ui %s (%s, %s, %s/%s)\n", version, commit, buildTime, runtime.GOOS, runtime.GOARCH)
		return
	}
	if args[0] != "serve" && args[0] != "validate" && args[0] != "ready" {
		log.Fatal("usage: kee-route-manager-ui <serve|validate|ready|version> [--config PATH]")
	}
	f := flag.NewFlagSet(args[0], flag.ExitOnError)
	p := f.String("config", "/etc/kee-route-manager-ui/config.yaml", "configuration path")
	_ = f.Parse(args[1:])
	if f.NArg() != 0 {
		log.Fatal("unexpected arguments")
	}
	c, e := config.Load(*p)
	if e != nil {
		log.Fatal(e)
	}
	if c.Instance.Role != "ui" {
		log.Fatal("UI requires instance.role=ui")
	}
	if args[0] == "validate" {
		fmt.Println("UI configuration is valid")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if args[0] == "ready" {
		if e = ui.Ready(ctx, c); e != nil {
			log.Fatal(e)
		}
		fmt.Println("UI and controller are ready")
		return
	}
	if e = ui.Serve(ctx, c); e != nil {
		log.Fatal(e)
	}
}
