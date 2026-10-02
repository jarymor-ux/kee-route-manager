//go:build linux

package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
)

type linuxProcessIdentity struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

func linuxProcessState(pid int) (linuxProcessIdentity, string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return linuxProcessIdentity{}, "", err
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return linuxProcessIdentity{}, "", fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(b[i+1:]))
	if len(fields) <= 19 {
		return linuxProcessIdentity{}, "", fmt.Errorf("short process stat")
	}
	return linuxProcessIdentity{PID: pid, Start: fields[19]}, fields[0], nil
}

// This helper is an actual parent process, so its SIGKILL bypasses every Go
// cleanup path. Only startChild's production Linux parent-death signal can stop
// the signed daemon it starts. The other helper models an independent Xray.
func TestLinuxParentDeathHelper(t *testing.T) {
	role := os.Getenv("KRM_TEST_PDEATH_ROLE")
	if role == "" {
		return
	}
	pid := os.Getpid()
	var daemon *child
	if role == "parent" {
		c, err := config.Load(os.Getenv("KRM_TEST_PDEATH_CONFIG"))
		if err != nil {
			t.Fatal(err)
		}
		release, err := update.VerifyRelease(c.Update.InstallDir, "1.0.0", c.Update.PublicKey, c.Update.Channel)
		if err != nil || release.ManifestSHA256 != os.Getenv("KRM_TEST_PDEATH_DIGEST") {
			t.Fatalf("signed release identity: %v", err)
		}
		daemon, err = startChild(filepath.Join(release.Directory, "kee-route-managerd"), os.Getenv("KRM_TEST_PDEATH_CONFIG"), "", io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		defer daemon.stop(context.Background())
		pid = daemon.cmd.Process.Pid
	} else if role != "sentinel" {
		t.Fatal("invalid helper role")
	}
	identity, _, err := linuxProcessState(pid)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(os.Getenv("KRM_TEST_PDEATH_READY"), b, 0600); err != nil {
		t.Fatal(err)
	}
	var exited <-chan struct{}
	if daemon != nil {
		exited = daemon.done
	}
	select {
	case <-exited:
		t.Fatalf("daemon exited before parent: %v", daemon.err)
	case <-time.After(45 * time.Second):
		t.Fatal("helper exceeded test deadline")
	}
}

func TestLinuxParentDeathStopsDaemonAndPreservesIndependentProcess(t *testing.T) {
	f := newReleaseFixture(t)
	startHelper := func(role string) (*exec.Cmd, <-chan error, linuxProcessIdentity) {
		t.Helper()
		ready := filepath.Join(f.root, "pdeath-"+role+".json")
		cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxParentDeathHelper$")
		cmd.Env = append(os.Environ(), "KRM_TEST_PDEATH_ROLE="+role, "KRM_TEST_PDEATH_READY="+ready,
			"KRM_TEST_PDEATH_CONFIG="+f.configFile, "KRM_TEST_PDEATH_DIGEST="+f.old.ManifestSHA256)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait(); close(done) }()
		t.Cleanup(func() {
			select {
			case <-done:
				return
			default:
			}
			_ = cmd.Process.Kill()
			<-done
		})
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var identity linuxProcessIdentity
			if b, err := os.ReadFile(ready); err == nil && json.Unmarshal(b, &identity) == nil && identity.PID > 0 && identity.Start != "" {
				return cmd, done, identity
			}
			select {
			case err := <-done:
				t.Fatalf("%s helper exited before readiness: %v", role, err)
			case <-time.After(10 * time.Millisecond):
			}
		}
		t.Fatalf("%s helper never published identity", role)
		return nil, nil, linuxProcessIdentity{}
	}

	sentinel, sentinelDone, sentinelID := startHelper("sentinel")
	parent, parentDone, daemonID := startHelper("parent")
	t.Cleanup(func() {
		// Failure cleanup is PID + start-time scoped; never signal a reused PID or
		// a process group. Reap an orphan if this test is also container PID 1.
		if identity, state, err := linuxProcessState(daemonID.PID); err == nil && identity == daemonID && state != "Z" {
			_ = syscall.Kill(daemonID.PID, syscall.SIGKILL)
		}
		_, _ = syscall.Wait4(daemonID.PID, nil, syscall.WNOHANG, nil)
	})
	waitDaemonVersion(t, f.c, "1.0.0")
	locks := []string{filepath.Join(f.c.Paths.StateDir, "daemon.lock"), filepath.Join(f.c.Xray.ConfigDir, ".krm-daemon.lock")}
	for _, path := range locks {
		if lock, err := daemonlock.Acquire(path); err == nil {
			lock.Close()
			t.Fatalf("running daemon did not own %s", filepath.Base(path))
		}
	}
	group, err := syscall.Getpgid(parent.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{daemonID.PID, sentinel.Process.Pid} {
		if got, err := syscall.Getpgid(pid); err != nil || got != group {
			t.Fatalf("process %d must share the group to detect collateral signals: %d %v", pid, got, err)
		}
	}
	if err = parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-parentDone:
		status, ok := parent.ProcessState.Sys().(syscall.WaitStatus)
		if err == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			t.Fatalf("parent did not die from SIGKILL: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("parent did not exit")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		identity, state, err := linuxProcessState(daemonID.PID)
		if os.IsNotExist(err) || err == nil && (identity != daemonID || state == "Z") {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("daemon survived its killed parent: state=%q err=%v pid=%s", state, err, strconv.Itoa(daemonID.PID))
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, path := range locks {
		lock, err := daemonlock.Acquire(path)
		if err != nil {
			t.Fatalf("orphan retained %s: %v", filepath.Base(path), err)
		}
		if err = lock.Close(); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-sentinelDone:
		t.Fatalf("independent process received collateral signal: %v", err)
	default:
	}
	if identity, state, err := linuxProcessState(sentinel.Process.Pid); err != nil || identity != sentinelID || state == "Z" {
		t.Fatalf("independent process not alive: state=%q err=%v", state, err)
	}
}
