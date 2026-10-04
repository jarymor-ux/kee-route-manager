package buildinfo

import (
	"fmt"
	"runtime"
)

func Print(name, version, commit, buildTime string) {
	fmt.Printf("%s %s (%s, %s, %s/%s)\n", name, version, commit, buildTime, runtime.GOOS, runtime.GOARCH)
}
