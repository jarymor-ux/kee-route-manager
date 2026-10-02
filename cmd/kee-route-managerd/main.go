package main

import (
	"context"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control"
	"github.com/jarymor-ux/kee-route-manager/internal/control/cli"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var version = "1.1.0-rc.4"
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
		c, err := controllerConfig(args[1:], "validate")
		e = err
		if e == nil {
			e = c.Validate()
			if e == nil {
				fmt.Println("Controller configuration is valid")
			}
		}
	case "tls-init":
		c, err := controllerConfig(args[1:], "tls-init")
		e = err
		if e == nil {
			var path string
			path, e = initTLS(c)
			if e == nil {
				fmt.Println(path)
			}
		}
	case "serve":
		c, err := controllerConfig(args[1:], "serve")
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

func controllerConfig(args []string, command string) (config.Config, error) {
	c, e := cli.Load(args, command)
	if e == nil && c.Instance.Role != "controller" {
		e = fmt.Errorf("daemon requires instance.role=controller")
	}
	return c, e
}
func initTLS(c config.Config) (string, error) {
	if !c.API.Enabled || !c.API.TLS.Enabled {
		return "", fmt.Errorf("tls-init requires api.enabled and api.tls.enabled")
	}
	if e := web.EnsureTLS(c.API.TLS, c.API.Listen); e != nil {
		return "", e
	}
	return c.API.TLS.CertFile, nil
}
