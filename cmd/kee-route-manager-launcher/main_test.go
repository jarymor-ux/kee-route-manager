package main

import (
	"strings"
	"testing"
)

func TestLauncherRejectsAmbiguousOrIncompleteCommands(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"delete"}, "usage:"},
		{nil, "--config"},
		{[]string{"serve", "--config", "/missing", "extra"}, "--config"},
		{[]string{"serve", "--config", "/missing", "--ui-config", "/other"}, "bootstrap flags"},
		{[]string{"install", "--config", "/missing"}, "--release-dir"},
		{[]string{"install", "--config", "/missing", "--release-dir", "/missing"}, "no such file"},
	} {
		err := run(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}
