package main

import (
	"context"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/control"
	"github.com/jarymor-ux/kee-route-manager/internal/control/cli"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var version = "1.0.0-rc.2"
var commit = "dev"
var buildTime = "unknown"

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC | log.Lmicroseconds)
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"serve"}
	}
	var e error
	switch args[0] {
	case "version", "--version", "-version":
		cli.Version("kee-route-managerd", version, commit, buildTime)
		return
	case "validate":
		e = cli.Validate(args[1:])
	case "tls-init":
		c, err := cli.Load(args[1:], "tls-init")
		e = err
		if e == nil {
			e = web.EnsureTLS(c.API.TLS, c.API.Listen)
		}
	case "serve":
		c, err := cli.Load(args[1:], "serve")
		e = err
		if e == nil {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			e = control.Serve(ctx, c, version)
		}
	default:
		e = fmt.Errorf("usage: kee-route-managerd <serve|validate|tls-init|version> [--config PATH]")
	}
	if e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
