package configflag

import (
	"flag"
	"fmt"
	"os"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func DefaultPath() string {
	if _, err := os.Stat("/opt/etc/ndm"); err == nil {
		return "/opt/etc/kee-route-manager/config.yaml"
	}
	return "/etc/kee-route-manager/config.yaml"
}

func Load(args []string, name string) (config.Config, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	path := flags.String("config", DefaultPath(), "configuration path")
	if err := flags.Parse(args); err != nil {
		return config.Config{}, err
	}
	if flags.NArg() != 0 {
		return config.Config{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	return config.Load(*path)
}
