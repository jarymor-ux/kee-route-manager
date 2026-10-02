package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
)

func DefaultConfigPath() string {
	if _, e := os.Stat("/opt/etc/ndm"); e == nil {
		return "/opt/etc/kee-route-manager/config.yaml"
	}
	return "/etc/kee-route-manager/config.yaml"
}
func Load(args []string, name string) (config.Config, error) {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	p := f.String("config", DefaultConfigPath(), "configuration path")
	if e := f.Parse(args); e != nil {
		return config.Config{}, e
	}
	if f.NArg() != 0 {
		return config.Config{}, fmt.Errorf("unexpected argument %q", f.Arg(0))
	}
	return config.Load(*p)
}
func Validate(args []string) error {
	c, e := Load(args, "validate")
	if e != nil {
		return e
	}
	b, e := json.MarshalIndent(c.Sanitized(), "", "  ")
	if e != nil {
		return e
	}
	fmt.Printf("Configuration is valid\n%s\n", b)
	return nil
}
func Version(name, version, commit, buildTime string) {
	fmt.Printf("%s %s (%s, %s, %s/%s)\n", name, version, commit, buildTime, runtime.GOOS, runtime.GOARCH)
}

func Run(ctx context.Context, args []string, version, commit, buildTime string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kee-route-managerctl <status|ready|benchmark|switch --slot N|direct|restore-xray|update-check|validate|passwd|route-candidates|version> [--config PATH] [--socket PATH]")
	}
	command, args := args[0], args[1:]
	switch command {
	case "version", "--version", "-version":
		Version("kee-route-managerctl", version, commit, buildTime)
		return nil
	case "validate":
		return Validate(args)
	case "passwd":
		return password(args)
	case "route-candidates":
		return routeCandidates(args)
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	p := f.String("config", DefaultConfigPath(), "configuration path")
	socket := f.String("socket", "", "daemon Unix socket (overrides config)")
	slot := f.Int("slot", -1, "slot index for switch")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", f.Arg(0))
	}
	if *socket == "" {
		c, e := config.Load(*p)
		if e != nil {
			return e
		}
		*socket = c.API.UnixSocket
	}
	path, method := "", "GET"
	var body any
	switch command {
	case "status", "doctor":
		path = "/api/v1/status"
	case "ready":
		path = "/healthz"
	case "benchmark":
		path = "/api/v1/actions/benchmark"
		method = "POST"
	case "switch":
		if *slot < 0 {
			return fmt.Errorf("switch requires --slot N")
		}
		path = "/api/v1/actions/switch"
		method = "POST"
		body = map[string]int{"index": *slot}
	case "direct":
		path = "/api/v1/actions/direct"
		method = "POST"
	case "restore-xray":
		path = "/api/v1/actions/restore-xray"
		method = "POST"
	case "update-check":
		path = "/api/v1/update/check"
	case "update-apply":
		return fmt.Errorf("automatic update apply is disabled in RC2; install a verified release manually")
	default:
		return fmt.Errorf("unknown command %q", command)
	}
	cl := client.New(*socket)
	defer cl.Close()
	out, e := cl.Do(ctx, method, path, body)
	if e != nil {
		return e
	}
	var pretty bytes.Buffer
	if e = json.Indent(&pretty, out, "", "  "); e != nil {
		return e
	}
	fmt.Println(pretty.String())
	return nil
}
func password(args []string) error {
	f := flag.NewFlagSet("passwd", flag.ContinueOnError)
	p := f.String("config", DefaultConfigPath(), "configuration path")
	username := f.String("username", "", "administrator username")
	stdin := f.Bool("password-stdin", false, "read password from standard input")
	if e := f.Parse(args); e != nil {
		return e
	}
	if !*stdin {
		return fmt.Errorf("--password-stdin is required")
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	c, e := config.Load(*p)
	if e != nil {
		return e
	}
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 1027))
	if e != nil {
		return e
	}
	if len(b) > 1026 {
		return fmt.Errorf("password too long")
	}
	password := strings.TrimRight(string(b), "\r\n")
	return auth.CreateCredentials(c.Web.CredentialsFile, *username, password)
}
func routeCandidates(args []string) error {
	f := flag.NewFlagSet("route-candidates", flag.ContinueOnError)
	p := f.String("file", "", "strict JSON Xray routing file")
	if e := f.Parse(args); e != nil {
		return e
	}
	if *p == "" || f.NArg() != 0 {
		return fmt.Errorf("route-candidates requires --file PATH")
	}
	file, e := os.Open(*p)
	if e != nil {
		return e
	}
	defer file.Close()
	b, e := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	if e != nil {
		return e
	}
	if len(b) > 4<<20 {
		return fmt.Errorf("routing file exceeds 4 MiB")
	}
	var doc struct {
		Routing struct {
			Rules []struct {
				InboundTag  []string `json:"inboundTag"`
				OutboundTag string   `json:"outboundTag"`
				BalancerTag string   `json:"balancerTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if e = d.Decode(&doc); e != nil {
		return fmt.Errorf("routing must be strict JSON: %w", e)
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("routing has trailing data")
	}
	candidates := []map[string]any{}
	for i, r := range doc.Routing.Rules {
		if r.OutboundTag != "" && r.BalancerTag == "" && len(r.InboundTag) > 0 {
			candidates = append(candidates, map[string]any{"rule_index": i, "inbound_tags": r.InboundTag, "outbound_tag": r.OutboundTag})
		}
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no routing rule with explicit inbound tags and outbound tag found")
	}
	out, e := json.MarshalIndent(candidates, "", "  ")
	if e != nil {
		return e
	}
	fmt.Println(string(out))
	return nil
}
