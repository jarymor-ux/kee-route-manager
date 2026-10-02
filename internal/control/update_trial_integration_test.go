package control

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
)

type trialXrayProcess struct {
	c   config.Config
	cmd *exec.Cmd
}

func (p *trialXrayProcess) stop() {
	if p.cmd != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
		p.cmd = nil
	}
}
func (p *trialXrayProcess) RestartXray(context.Context) error {
	p.stop()
	p.cmd = exec.Command(p.c.Xray.Binary, "run", "-confdir", p.c.Xray.ConfigDir)
	p.cmd.Stdout = io.Discard
	p.cmd.Stderr = io.Discard
	return p.cmd.Start()
}
func (p *trialXrayProcess) XrayRunning(context.Context) bool {
	return p.cmd != nil && p.cmd.ProcessState == nil
}

// Run with a verified real Xray binary (Linux Docker in CI/release verification).
// Both controller and Xray execute as native subprocesses; no firewall is used.
func TestTrialWithRealXrayRejectsRuntimeSelectionDrift(t *testing.T) {
	binary := os.Getenv("KRM_TEST_XRAY_BINARY")
	if binary == "" {
		t.Skip("set KRM_TEST_XRAY_BINARY for actual Xray API trial validation")
	}
	c, _, _ := trialFixture(t)
	c.Xray.Binary = binary
	c.Xray.BaseRoutingFile = ""
	c.Pool.Size = 1
	var listeners []net.Listener
	for len(listeners) < 3 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if ln.Addr().String() == c.API.Listen {
			ln.Close()
			continue
		}
		listeners = append(listeners, ln)
	}
	c.Xray.APIAddress = listeners[0].Addr().String()
	c.Xray.HealthProxyPort = listeners[1].Addr().(*net.TCPAddr).Port
	c.Xray.ProbePortStart = listeners[2].Addr().(*net.TCPAddr).Port
	for _, ln := range listeners {
		ln.Close()
	}
	if err := os.MkdirAll(c.Xray.ConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.Paths.RunDir, 0700); err != nil {
		t.Fatal(err)
	}
	p := &trialXrayProcess{c: c}
	t.Cleanup(p.stop)
	runner := platform.Runner{Timeout: 5 * time.Second}
	xm := xray.NewManager(c, runner, p)
	state := model.NewState("old", c.Xray.SlotTagPrefix, c.Pool.Size)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := xm.Bootstrap(ctx, tunnel.DesiredPool{Slots: state.Pool, Nodes: map[string]model.Node{}, Selection: tunnel.Selection{Tag: c.Xray.ManagedDirectTag}}); err != nil {
		t.Fatal(err)
	}
	actual, err := xm.ActualState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.XrayConfigured = true
	state.XrayGeneration = 1
	state.XrayConfigHash = actual.ConfigHash
	state.DirectMode = true
	state.AutomaticRoutingPaused = true
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(c.Paths.StateDir, "state.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(c.Paths.StateDir, c.Paths.CacheDir, model.NewState("old", c.Xray.SlotTagPrefix, c.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.PrepareTransaction(store.Transaction{ID: "trial-baseline", Kind: "pool", Before: state, Desired: state}); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{store.XrayFilesStaged, store.XrayRuntimeApplied, store.FirewallApplied, store.SelectionApplied, store.StateCommitted, store.Done} {
		if err = st.AdvanceTransaction(stage); err != nil {
			t.Fatal(err)
		}
	}
	before := protectedTrialTree(t, c)
	startTrialProcess(t, c, strings.Repeat("d", 64))
	if health := waitTrialHealth(t, c); health["status"] != "trial_ready" {
		t.Fatalf("bad real trial health: %#v", health)
	}
	if !reflect.DeepEqual(before, protectedTrialTree(t, c)) {
		t.Fatal("real trial modified Xray/state")
	}
	// Mutate ONLY runtime override. Disk hash remains identical: the read-only
	// API must detect this independently of file reconciliation.
	if _, err = runner.Run(ctx, []string{binary, "api", "bo", "--server=" + c.Xray.APIAddress, "-b", c.Xray.BalancerTag, c.Xray.SlotTagPrefix + "0"}); err != nil {
		t.Fatal(err)
	}
	cl := client.New(c.API.UnixSocket)
	defer cl.Close()
	if _, err = cl.Do(ctx, "GET", "/healthz", nil); err == nil {
		t.Fatal("trial accepted foreign runtime selection")
	}
	if !reflect.DeepEqual(before, protectedTrialTree(t, c)) {
		t.Fatal("trial repaired runtime drift by writing files")
	}
	if _, err = runner.Run(ctx, []string{binary, "api", "bo", "--server=" + c.Xray.APIAddress, "-b", c.Xray.BalancerTag, c.Xray.ManagedDirectTag}); err != nil {
		t.Fatal(err)
	}
	mainPID := p.cmd.Process.Pid
	if _, err = cl.Do(ctx, "POST", "/internal/update/activate", map[string]string{"nonce": strings.Repeat("d", 64)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	active := false
	for time.Now().Before(deadline) {
		b, probeErr := cl.Do(ctx, "GET", "/healthz", nil)
		if probeErr == nil && strings.Contains(string(b), `"status":"ok"`) {
			active = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !active {
		t.Fatal("real controller did not become active")
	}
	if p.cmd.Process.Pid != mainPID || !p.XrayRunning(ctx) {
		t.Fatal("activation restarted existing Xray")
	}
}
