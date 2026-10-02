package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

func init() {
	mode := os.Getenv("KRM_BATCH_HELPER_PROCESS")
	if mode == "" {
		return
	}
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "bad node 11111111-1111-1111-1111-111111111111")
		os.Exit(1)
	}
	if mode == "wait" {
		if err := os.WriteFile(os.Getenv("KRM_BATCH_HELPER_STARTED"), []byte("started"), 0600); err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	if mode == "orphan" {
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "KRM_BATCH_HELPER_PROCESS=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(os.Getenv("KRM_BATCH_HELPER_STARTED")); err == nil {
				os.Exit(0)
			}
		}
		os.Exit(2)
	}
	if mode == "child" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(2)
		}
		if err = os.WriteFile(os.Getenv("KRM_BATCH_HELPER_STARTED"), []byte(listener.Addr().String()), 0600); err != nil {
			os.Exit(2)
		}
		for {
			conn, err := listener.Accept()
			if err != nil {
				os.Exit(2)
			}
			_ = conn.Close()
		}
	}
	if err := runBatchHelper(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// A disposable subprocess opens exactly the listeners from the generated batch.
// It has no routing/firewall functionality and is stopped by Batch.Stop.
func runBatchHelper() error {
	data, err := os.ReadFile(os.Args[len(os.Args)-1])
	if err != nil {
		return err
	}
	var root struct {
		Inbounds []struct {
			Listen string `json:"listen"`
			Port   int    `json:"port"`
		} `json:"inbounds"`
	}
	if err = json.Unmarshal(data, &root); err != nil {
		return err
	}
	for _, inbound := range root.Inbounds {
		listener, err := net.Listen("tcp", net.JoinHostPort(inbound.Listen, strconv.Itoa(inbound.Port)))
		if err != nil {
			return err
		}
		defer listener.Close()
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				_ = conn.Close()
			}
		}()
	}
	select {}
}

func batchHelperConfig(t *testing.T, mode string) config.Config {
	t.Helper()
	c := reproConfig(t)
	c.Xray.Binary = os.Args[0]
	c.Benchmark.TemporaryStartupTimeout = config.Dur(15 * time.Second)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.Benchmark.TemporaryProxyPortStart = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	t.Setenv("KRM_BATCH_HELPER_PROCESS", mode)
	return c
}

func TestBatchLifecycleExposesIsolatedProxiesAndJoinsCleanup(t *testing.T) {
	c := batchHelperConfig(t, "listen")
	nodes := []model.Node{testNode(), testNode()}
	nodes[1].ID = "other"
	batch, err := NewBatchRunner(c).Start(context.Background(), nodes)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Stop()
	if len(batch.Proxies) != 2 || batch.Proxies[nodes[0].ID].Host == batch.Proxies[nodes[1].ID].Host {
		t.Fatal("nodes did not receive distinct probe endpoints")
	}
	path := filepath.Join(batch.dir, "bench.json")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("temporary credentials are not private: %v %v", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Outbounds []map[string]any `json:"outbounds"`
		Routing   struct {
			Rules []struct {
				InboundTag  []string `json:"inboundTag"`
				OutboundTag string   `json:"outboundTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err = json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if len(root.Outbounds) != len(nodes) || len(root.Routing.Rules) != len(nodes) {
		t.Fatal("generated batch lost per-node routes")
	}
	for i, rule := range root.Routing.Rules {
		if len(rule.InboundTag) != 1 || rule.InboundTag[0] != fmt.Sprintf("bench-in-%d", i) || rule.OutboundTag != fmt.Sprintf("bench-out-%d", i) || root.Outbounds[i]["protocol"] != "vless" {
			t.Fatalf("batch probes can escape their node: %+v", rule)
		}
	}
	var stops sync.WaitGroup
	for i := 0; i < 3; i++ {
		stops.Add(1)
		go func() { defer stops.Done(); batch.Stop() }()
	}
	stops.Wait()
	if _, err = os.Stat(batch.dir); !os.IsNotExist(err) {
		t.Fatalf("batch retained private configuration: %v", err)
	}
	for _, proxy := range batch.Proxies {
		conn, err := net.DialTimeout("tcp", proxy.Host, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Fatalf("stopped batch still listens on %s", proxy.Host)
		}
	}
}

func TestBatchRejectsInvalidInputsWithoutLeakingPrivateFiles(t *testing.T) {
	for _, nodes := range [][]model.Node{nil, {{Protocol: "unsupported"}}} {
		c := reproConfig(t)
		if _, err := NewBatchRunner(c).Start(context.Background(), nodes); err == nil {
			t.Fatal("invalid batch accepted")
		}
		entries, err := os.ReadDir(c.Paths.RunDir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("rejected batch left temporary files: %v %v", entries, err)
		}
	}
}

func TestBatchStartupCancellationPreservesContextAndCleansFiles(t *testing.T) {
	c := batchHelperConfig(t, "wait")
	started := filepath.Join(t.TempDir(), "started")
	t.Setenv("KRM_BATCH_HELPER_STARTED", started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		batch, err := NewBatchRunner(c).Start(ctx, []model.Node{testNode()})
		if batch != nil {
			batch.Stop()
		}
		result <- err
	}()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("helper exited before startup handshake: %v", err)
		case <-deadline.C:
			t.Fatal("helper did not reach startup handshake")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup cancellation cause lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup cancellation did not join subprocess")
	}
	entries, err := os.ReadDir(c.Paths.RunDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled startup retained private files: %v %v", entries, err)
	}
}

func TestBatchEarlyExitCleansInheritedDescendants(t *testing.T) {
	c := batchHelperConfig(t, "orphan")
	started := filepath.Join(t.TempDir(), "child-address")
	t.Setenv("KRM_BATCH_HELPER_STARTED", started)
	if batch, err := NewBatchRunner(c).Start(context.Background(), []model.Node{testNode()}); err == nil {
		batch.Stop()
		t.Fatal("exited wrapper reported ready")
	}
	address, err := os.ReadFile(started)
	if err != nil {
		t.Fatalf("descendant never started: %v", err)
	}
	conn, err := net.DialTimeout("tcp", string(address), 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatal("exited wrapper left temporary descendant listening")
	}
}

func TestBatchStopDoesNotSignalCompletedPID(t *testing.T) {
	// Use a disposable live group to represent a PID reused after completion.
	started := filepath.Join(t.TempDir(), "started")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "KRM_BATCH_HELPER_PROCESS=wait", "KRM_BATCH_HELPER_STARTED="+started)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	childDone := make(chan struct{})
	go func() { _ = cmd.Wait(); close(childDone) }()
	defer func() { _ = cmd.Process.Kill(); <-childDone }()
	done := make(chan struct{})
	close(done)
	batch := &Batch{cmd: cmd, done: done, dir: t.TempDir()}
	batch.Stop()
	select {
	case <-childDone:
		t.Fatal("Stop signaled a PID after its batch was already complete")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(batch.dir); !os.IsNotExist(err) {
		t.Fatalf("completed batch files were not cleaned: %v", err)
	}
}

func TestFreeBlockIncludesLastTCPPortAndRejectsInvalidSizes(t *testing.T) {
	for _, count := range []int{0, -1, 65536, int(^uint(0) >> 1)} {
		if _, err := freeBlock(20000, count); err == nil {
			t.Fatalf("invalid block size %d accepted", count)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:65535")
	if err != nil {
		t.Skipf("port65535 already occupied: %v", err)
	}
	_ = listener.Close()
	port, err := freeBlock(65535, 1)
	if err != nil || port != 65535 {
		t.Fatalf("valid last TCP port rejected: port=%d err=%v", port, err)
	}
}
