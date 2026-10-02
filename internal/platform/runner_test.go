package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerBoundsSubprocessLifetime(t *testing.T) {
	for _, mode := range []string{"timeout", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := Runner{Timeout: 50 * time.Millisecond}
			if mode == "cancellation" {
				runner.Timeout = 5 * time.Second
				timer := time.AfterFunc(50*time.Millisecond, cancel)
				defer timer.Stop()
			}
			start := time.Now()
			_, err := runner.Run(ctx, []string{"/bin/sh", "-c", "sleep 2 & wait"})
			if err == nil {
				t.Fatal("canceled subprocess succeeded")
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("subprocess outlived its deadline: %s", elapsed)
			}
			want := context.DeadlineExceeded
			if mode == "cancellation" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Errorf("context cause not retained: %v", err)
			}
		})
	}
}

func TestRunnerBoundsBothOutputStreamsAndReportsFailures(t *testing.T) {
	runner := Runner{MaxOutput: 4}
	for _, tc := range []struct {
		name, script, output, failure string
	}{
		{"success", "printf abcdef; printf uvwxyz >&2", "abcd", ""},
		{"stderr", "printf abcdef; printf uvwxyz >&2; exit 3", "abcd", "uvwx"},
		{"stdout_fallback", "printf abcdef; exit 3", "abcd", "abcd"},
		{"exit_fallback", "exit 3", "", "exit status 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runner.Run(context.Background(), []string{"/bin/sh", "-c", tc.script})
			if string(out) != tc.output {
				t.Fatalf("output = %q, want %q", out, tc.output)
			}
			if tc.failure == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.failure) {
				t.Fatalf("missing failure %q: %v", tc.failure, err)
			}
		})
	}
	if _, err := runner.Run(context.Background(), nil); err == nil {
		t.Fatal("empty command accepted")
	}
}

func TestMissingFirewallToolCannotConfirmDirectBypass(t *testing.T) {
	g, _ := fakeFirewall(t)
	t.Setenv("PATH", t.TempDir())
	if err := g.EnterDirectBypass(context.Background()); err == nil {
		t.Error("missing nft executable was treated as absent interception")
	}
	if _, err := os.Stat(g.ownershipPath()); !os.IsNotExist(err) {
		t.Errorf("unobserved firewall bypass was persisted: %v", err)
	}
	c := g.firewallConfig()
	if err := g.saveOwnership(firewallOwnership{Mark: c.Mark, Table: c.Table, Bypass: true}); err != nil {
		t.Fatal(err)
	}
	if active, err := g.DirectBypassActive(context.Background()); err == nil || active {
		t.Fatalf("unobservable bypass reported active=%v err=%v", active, err)
	}
}

func TestBrokenFirewallExecutableCannotConfirmDirectBypass(t *testing.T) {
	for _, failure := range []struct {
		name, output, status string
	}{
		{"loader", "nft: error while loading shared libraries: libnftnl.so.11: cannot open shared object file: No such file or directory", "127"},
		{"runtime_configuration", "Error: cannot open runtime configuration: No such file or directory", "1"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			g, dir := fakeFirewall(t)
			script := "#!/bin/sh\nprintf '%s\\n' \"$KRM_NFT_FAILURE\" >&2\nexit " + failure.status + "\n"
			if err := os.WriteFile(filepath.Join(dir, "bin", "nft"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("KRM_NFT_FAILURE", failure.output)
			if err := g.EnterDirectBypass(context.Background()); err == nil {
				t.Error("broken nft executable was treated as absent interception")
			}
			if _, err := os.Stat(g.ownershipPath()); !os.IsNotExist(err) {
				t.Errorf("unobserved firewall bypass was persisted: %v", err)
			}
		})
	}
}

func TestMissingKernelResourceSignaturesRemainRecognized(t *testing.T) {
	for _, resource := range []struct {
		tool, message, status string
	}{
		{"nft", "Error: No such file or directory\nlist table inet krm\n                ^^^", "1"},
		{"nft", "Error: Could not process rule: No such file or directory\nlist table inet krm", "1"},
		{"ip", "RTNETLINK answers: No such file or directory", "2"},
		{"ip", "RTNETLINK answers: No such process", "2"},
		{"ip", "Error: ipv4: FIB table does not exist.\nDump terminated", "2"},
	} {
		t.Run(resource.tool+"/"+strings.SplitN(resource.message, "\n", 2)[0], func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' \"$KRM_MISSING_RESOURCE\" >&2\nexit " + resource.status + "\n"
			if err := os.WriteFile(filepath.Join(dir, resource.tool), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("KRM_MISSING_RESOURCE", resource.message)
			_, err := (Runner{}).Run(context.Background(), []string{resource.tool})
			if err == nil || !isMissingRuleError(err) {
				t.Fatalf("missing kernel resource not recognized: %v", err)
			}
		})
	}
}
