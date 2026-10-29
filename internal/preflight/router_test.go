package preflight

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

const selectedRouting = `{"routing":{"rules":[{"inboundTag":["redirect","tproxy"],"outboundTag":"vless-reality"}]}}`

func routingFixture(t *testing.T) config.Config {
	t.Helper()
	c := config.Default()
	c.Xray.ConfigDir = t.TempDir()
	c.Xray.ManagedDir = c.Xray.ConfigDir
	c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "05_routing.json")
	writeRouterFixture(t, filepath.Join(c.Xray.ConfigDir, "01_inbounds.json"), `{"inbounds":[{"tag":"redirect"},{"tag":"tproxy"}]}`)
	writeRouterFixture(t, c.Xray.BaseRoutingFile, selectedRouting)
	return c
}

func writeRouterFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingPreflightRequiresAuthoritativeBase(t *testing.T) {
	for _, scenario := range []string{"valid", "missing base", "missing routing", "second routing", "empty second routing", "managed fragment"} {
		t.Run(scenario, func(t *testing.T) {
			c := routingFixture(t)
			switch scenario {
			case "missing base":
				c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "missing.json")
			case "missing routing":
				writeRouterFixture(t, c.Xray.BaseRoutingFile, `{}`)
				writeRouterFixture(t, filepath.Join(c.Xray.ConfigDir, "other.json"), selectedRouting)
			case "second routing":
				writeRouterFixture(t, filepath.Join(c.Xray.ConfigDir, "99_other.json"), selectedRouting)
			case "empty second routing":
				writeRouterFixture(t, filepath.Join(c.Xray.ConfigDir, "99_other.json"), `{"routing":{}}`)
			case "managed fragment":
				writeRouterFixture(t, filepath.Join(c.Xray.ConfigDir, "05_90_kee_route_manager_routing.json"), `{"routing":{"rules":[]}}`)
			}
			err := checkRoutes(c)
			wantOK := scenario == "valid" || scenario == "managed fragment"
			if (err == nil) != wantOK {
				t.Fatalf("scenario=%s err=%v", scenario, err)
			}
		})
	}
}

func TestRoutingPreflightDoesNotBlockOnSpecialFiles(t *testing.T) {
	for _, link := range []bool{false, true} {
		t.Run(fmt.Sprintf("symlink=%v", link), func(t *testing.T) {
			c := routingFixture(t)
			fifo := filepath.Join(t.TempDir(), "fifo")
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(c.Xray.ConfigDir, "99_special.json")
			if link {
				if err := os.Symlink(fifo, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Rename(fifo, path); err != nil {
				t.Fatal(err)
			} else {
				fifo = path
			}
			result := make(chan error, 1)
			go func() { result <- checkRoutes(c) }()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("special file accepted")
				}
			case <-time.After(250 * time.Millisecond):
				// Release a blocking legacy reader so a failing regression leaks no goroutine.
				fd, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err == nil {
					fd.Close()
				}
				select {
				case <-result:
				case <-time.After(time.Second):
				}
				t.Fatal("preflight blocked opening special JSON file")
			}
		})
	}
}

func TestRCIProbeIsReadOnlyBoundedAndRejectsRedirects(t *testing.T) {
	var redirects atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1); fmt.Fprint(w, `{"ok":true}`) }))
	defer target.Close()
	for _, scenario := range []string{"valid", "status", "empty", "trailing", "oversized", "redirect", "failure"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("mutating RCI method: %s", r.Method)
				}
				switch scenario {
				case "valid":
					fmt.Fprint(w, `{"system":{"uptime":1}}`)
				case "status":
					fmt.Fprint(w, `{"status":true}`)
				case "empty":
					fmt.Fprint(w, `{}`)
				case "trailing":
					fmt.Fprint(w, `{"ok":true} {"extra":true}`)
				case "oversized":
					fmt.Fprint(w, `{"ok":true}`+strings.Repeat(" ", 300<<10))
				case "redirect":
					http.Redirect(w, r, target.URL, http.StatusFound)
				case "failure":
					http.Error(w, "private device error", http.StatusForbidden)
				}
			}))
			defer server.Close()
			if got := readRCI(context.Background(), server.URL); got != (scenario == "valid") {
				t.Fatalf("scenario=%s available=%v", scenario, got)
			}
		})
	}
	if redirects.Load() != 0 {
		t.Fatal("RCI probe followed redirect")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if readRCI(ctx, target.URL) {
		t.Fatal("cancelled RCI probe succeeded")
	}
}

func TestOfflineXrayValidationTimesOutAndKillsChildren(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Xray.Binary = filepath.Join(dir, "xray-fixture")
	c.Xray.AssetDir = filepath.Join(dir, "assets")
	c.Xray.ConfigDir = dir
	c.Platform.CommandTimeout = config.Dur(250 * time.Millisecond)
	childPID := filepath.Join(dir, "child.pid")
	t.Setenv("KRM_PREFLIGHT_CHILD_PID", childPID)
	writeRouterFixture(t, c.Xray.Binary, "#!/bin/sh\n[ \"$1 $2 $3\" = 'run -test -confdir' ] || exit 2\nsleep 30 &\nprintf '%s\\n' \"$!\" > \"$KRM_PREFLIGHT_CHILD_PID\"\nwait\n")
	if err := os.Chmod(c.Xray.Binary, 0700); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := validateXray(context.Background(), c); err == nil {
		t.Fatal("timed out Xray validation succeeded")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("validation did not honor timeout")
	}
	data, err := os.ReadFile(childPID)
	if err != nil {
		t.Fatal("validation child never started", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatal("invalid synthetic child PID")
	}
	deadline := time.Now().Add(time.Second)
	for {
		proc, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		// A child can disappear between /proc lookup and reading its
		// stat file. Linux may report ESRCH instead of ENOENT in this case.
		if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		// Signal delivery to the killed child may lag its parent. Zombies are
		// already dead; reaping is the container/host init's responsibility.
		fields := strings.Fields(string(proc))
		if len(fields) > 2 && (fields[2] == "Z" || fields[2] == "X") {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("validation left its synthetic sleep child alive")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
