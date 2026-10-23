package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/jarymor-ux/kee-route-manager/internal/preflight"
)

func nativePolicyCheck(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("native-policy-check", flag.ContinueOnError)
	f.SetOutput(out)
	path := f.String("xkeen-config", "/opt/etc/xkeen/xkeen.json", "private native XKeen configuration")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected native policy argument")
	}
	if err := preflight.CheckNativePolicy(ctx, *path); err != nil {
		return err
	}
	return nil
}
