package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
)

func TestPendingPanelDNSKeepsRecoveryAPIAvailable(t *testing.T) {
	c := isolatedDaemonConfig(t)
	c.Platform.Kind = "keenetic"
	c.Platform.Keenetic.AllowPolicyChange = true
	journal := filepath.Join(c.Paths.StateDir, ".panel-dns-pending.json")
	private := []byte(`{"schema":1,"phase":"unknown","hostname":"alice.jopa","ip":"192.168.1.1"}`)
	if err := os.WriteFile(journal, private, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, c, "dns-recovery") }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon exit: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon shutdown timed out")
		}
	}()
	cl := client.New(c.API.UnixSocket)
	defer cl.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err := cl.Do(ctx, "GET", "/api/v1/status", nil)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending DNS prevented API startup: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(journal)
	if err != nil || string(data) != string(private) {
		t.Fatal("unresolved private intent was modified")
	}
	data, err = os.ReadFile(filepath.Join(c.Paths.StateDir, "events.jsonl"))
	if err != nil || !strings.Contains(string(data), "panel.dns.recovery_pending") {
		t.Fatalf("DNS recovery warning absent: %v", err)
	}
}
