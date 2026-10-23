package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/buildinfo"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/configflag"
	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
	"github.com/jarymor-ux/kee-route-manager/internal/setup"
)

func Validate(args []string) error {
	c, e := configflag.Load(args, "validate")
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

func Run(ctx context.Context, args []string, version, commit, buildTime string) error {
	return runWithIO(ctx, args, version, commit, buildTime, os.Stdin, os.Stdout)
}

func runWithIO(ctx context.Context, args []string, version, commit, buildTime string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kee-route-managerctl <init-config|preflight|status|ready|benchmark|switch --slot N|direct|restore-xray|update-status|update-check|update-apply [--target-version VERSION]|validate|passwd|native-policy-check|route-candidates|version> [--config PATH] [--socket PATH]")
	}
	command, args := args[0], args[1:]
	switch command {
	case "version", "--version", "-version":
		buildinfo.Print("kee-route-managerctl", version, commit, buildTime)
		return nil
	case "validate":
		return Validate(args)
	case "preflight":
		return preflightCommand(ctx, args, out)
	case "passwd":
		return password(args)
	case "route-candidates":
		return routeCandidates(args)
	case "init-config":
		return initConfig(args, in, out)
	case "native-policy-check":
		return nativePolicyCheck(ctx, args, out)
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	p := f.String("config", configflag.DefaultPath(), "configuration path")
	socket := f.String("socket", "", "daemon Unix socket (overrides config)")
	slot := f.Int("slot", -1, "slot index for switch")
	var targetVersion string
	if command == "update-apply" {
		f.StringVar(&targetVersion, "target-version", "", "expected signed release version")
	}
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
	case "update-status":
		path = "/api/v1/update/status"
	case "update-apply":
		path, method = "/api/v1/update/apply", "POST"
		body = struct {
			Version string `json:"version,omitempty"`
		}{Version: targetVersion}
	default:
		return fmt.Errorf("unknown command %q", command)
	}
	cl := client.New(*socket)
	defer cl.Close()
	response, e := cl.Do(ctx, method, path, body)
	if e != nil {
		return e
	}
	var pretty bytes.Buffer
	if e = json.Indent(&pretty, response, "", "  "); e != nil {
		return e
	}
	fmt.Println(pretty.String())
	return nil
}
func password(args []string) error {
	f := flag.NewFlagSet("passwd", flag.ContinueOnError)
	p := f.String("config", configflag.DefaultPath(), "configuration path")
	username := f.String("username", "", "administrator username")
	stdin := f.Bool("password-stdin", false, "read password from standard input")
	credentialsOutput := f.String("credentials-output", "", "stage credentials at a new absolute path without changing configured credentials")
	expectedCredentials := f.String("expected-credentials-file", "", "require this configured credentials path before staging")
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
	if *expectedCredentials != "" && c.Web.CredentialsFile != *expectedCredentials {
		return fmt.Errorf("configured credentials path differs from the installer destination")
	}
	if *credentialsOutput != "" {
		if !filepath.IsAbs(*credentialsOutput) {
			return fmt.Errorf("credentials output must be absolute")
		}
		if _, e := os.Lstat(*credentialsOutput); !os.IsNotExist(e) {
			return fmt.Errorf("credentials output already exists or cannot be inspected")
		}
	}
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 1027))
	if e != nil {
		return e
	}
	if len(b) > 1026 {
		return fmt.Errorf("password too long")
	}
	password := strings.TrimRight(string(b), "\r\n")
	if *credentialsOutput != "" {
		tmp, e := os.CreateTemp(filepath.Dir(*credentialsOutput), ".krm-credentials-*")
		if e != nil {
			return e
		}
		name := tmp.Name()
		if e = tmp.Close(); e != nil {
			return e
		}
		defer os.Remove(name)
		if e = auth.CreateCredentials(name, *username, password); e != nil {
			return e
		}
		if e = os.Link(name, *credentialsOutput); e != nil {
			return fmt.Errorf("publish staged credentials without overwrite: %w", e)
		}
		dir, e := os.Open(filepath.Dir(*credentialsOutput))
		if e != nil {
			return e
		}
		defer dir.Close()
		return dir.Sync()
	}
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

func initConfig(args []string, in io.Reader, out io.Writer) error {
	f := flag.NewFlagSet("init-config", flag.ContinueOnError)
	f.SetOutput(out)
	output := f.String("output", configflag.DefaultPath(), "output configuration path")
	overwrite := f.Bool("overwrite", false, "allow replacing an existing output file")
	platform := f.String("platform", "", "target router family (keenetic for Keenetic/Netcraze)")
	uiOutput := f.String("ui-output", "", "create a local UI configuration with verified TLS upstream alongside the controller")
	tlsDir := f.String("tls-dir", "", "new private directory for the local controller TLS identity")
	nativeOutput := f.String("native-output", "", "prepare blocked clean XKeen/Xray configuration in a new private directory; never activate it")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", f.Arg(0))
	}
	if *nativeOutput != "" {
		if *overwrite {
			return fmt.Errorf("native installer cannot overwrite existing files")
		}
		return setup.InitNativeInstallerConfig(in, out, *output, *uiOutput, *tlsDir, *nativeOutput, setup.Platform(*platform))
	}
	if *uiOutput != "" || *tlsDir != "" {
		if *overwrite {
			return fmt.Errorf("local UI pair cannot overwrite existing files")
		}
		return setup.InitLocalUIConfig(in, out, *output, *uiOutput, *tlsDir, setup.Platform(*platform))
	}
	if *platform != "" {
		return setup.InitConfigForPlatform(in, out, *output, *overwrite, setup.Platform(*platform))
	}
	return setup.InitConfig(in, out, *output, *overwrite)
}
