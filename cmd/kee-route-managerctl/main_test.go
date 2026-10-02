package main

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEntrypoint(t *testing.T) {
	if os.Getenv("KRM_TEST_ENTRYPOINT") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"kee-route-managerctl"}, os.Args[i+1:]...)
				main()
				return
			}
		}
		t.Fatal("helper arguments missing")
	}
	routing := filepath.Join(t.TempDir(), "routing.json")
	if err := os.WriteFile(routing, []byte(`{"routing":{"rules":[{"inboundTag":["redirect"],"outboundTag":"chosen"}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args    []string
		success bool
		want    string
	}{
		{[]string{"version"}, true, "kee-route-managerctl " + version},
		{[]string{"route-candidates", "--file", routing}, true, `"outbound_tag": "chosen"`},
		{nil, false, "usage: kee-route-managerctl"},
		{[]string{"switch", "--socket", "/missing/control.sock"}, false, "switch requires --slot N"},
		{[]string{"status", "--socket", "/missing/control.sock"}, false, "is kee-route-managerd running?"},
		{[]string{"update-apply", "--socket", "/missing/control.sock"}, false, "is kee-route-managerd running?"},
		{[]string{"update-status", "--socket", "/missing/control.sock"}, false, "is kee-route-managerd running?"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		args := []string{"-test.run=^TestEntrypoint$"}
		if coverage := flag.Lookup("test.gocoverdir"); coverage != nil && coverage.Value.String() != "" {
			args = append(args, "-test.gocoverdir="+coverage.Value.String())
		}
		args = append(args, "--")
		args = append(args, tc.args...)
		cmd := exec.CommandContext(ctx, os.Args[0], args...)
		cmd.Env = append(os.Environ(), "KRM_TEST_ENTRYPOINT=1")
		out, err := cmd.CombinedOutput()
		cancel()
		if (err == nil) != tc.success || !strings.Contains(string(out), tc.want) {
			t.Fatalf("args=%v err=%v output=%s", tc.args, err, out)
		}
	}
}
