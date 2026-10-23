package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/configflag"
	"github.com/jarymor-ux/kee-route-manager/internal/preflight"
)

func preflightCommand(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("preflight", flag.ContinueOnError)
	f.SetOutput(out)
	p := f.String("config", configflag.DefaultPath(), "prepared private configuration")
	mode := f.String("mode", "core", "core, local-ui or ui")
	stage := f.String("staging-dir", "", "existing staging directory (defaults to TMPDIR)")
	release := f.String("release-dir", "", "verified extracted release root for exact binary disk accounting")
	resources := f.Bool("resources-only", false, "only check resources before dependency provisioning; does not authorize installation")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected preflight argument")
	}
	c, err := config.Load(*p)
	if err != nil {
		return err
	}
	r := preflight.Run(ctx, c, preflight.Options{Mode: *mode, StageDir: *stage, ReleaseDir: *release, ResourcesOnly: *resources})
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return err
	}
	if !r.OK {
		return fmt.Errorf("preflight failed; installation has not started")
	}
	return nil
}
