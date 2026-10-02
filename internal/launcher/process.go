package launcher

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error // written before done closes
}

func startChild(path, configPath, nonce string, output io.Writer) (*child, error) {
	cmd := exec.Command(path, "serve", "--config", configPath)
	cmd.Env = make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "KRM_UPDATE_TRIAL=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	if nonce != "" {
		cmd.Env = append(cmd.Env, "KRM_UPDATE_TRIAL="+nonce)
	}
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = 2 * time.Second
	parentDeathSignal(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &child{cmd: cmd, done: make(chan struct{})}
	go func() { c.err = cmd.Wait(); close(c.done) }()
	return c, nil
}

func (c *child) alive() bool {
	if c == nil {
		return false
	}
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *child) stop(ctx context.Context) error {
	if c == nil || !c.alive() {
		return nil
	}
	// Signal only our child. Its process group may contain an independently
	// running production Xray launched by XKeen; that process must survive.
	if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	timer := time.NewTimer(35 * time.Second)
	defer timer.Stop()
	select {
	case <-c.done:
		return nil
	case <-timer.C:
	case <-ctx.Done():
	}
	if err := c.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-c.done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("child did not release process ownership")
	}
}
