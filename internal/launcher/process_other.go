//go:build !linux

package launcher

import "os/exec"

func parentDeathSignal(_ *exec.Cmd) {}
