package main

import (
	"context"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/control/cli"
	"os"
	"os/signal"
	"syscall"
)

var version = "1.1.0-rc.2"
var commit = "dev"
var buildTime = "unknown"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if e := cli.Run(ctx, os.Args[1:], version, commit, buildTime); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
