package platform

import (
	"context"
	"errors"
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
