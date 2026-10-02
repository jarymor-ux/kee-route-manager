package main

import (
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEntrypoint(t *testing.T) {
	if os.Getenv("KRM_TEST_UI_ENTRYPOINT") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"kee-route-manager-ui"}, os.Args[i+1:]...)
				main()
				return
			}
		}
		t.Fatal("helper arguments missing")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>UI</html>"))
			return
		}
		_, _ = w.Write([]byte(`{"role":"controller","status":"ok"}`))
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "ui.yaml")
	body := "instance:\n  role: ui\npaths:\n  state_dir: untouched-state\n  log_file: untouched.log\nweb:\n  listen: " + strings.TrimPrefix(upstream.URL, "http://") + "\n  tls:\n    enabled: false\nui:\n  upstream: https://127.0.0.1:9443\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args    []string
		success bool
		want    string
	}{
		{[]string{"version"}, true, "kee-route-manager-ui " + version},
		{[]string{"validate", "--config", path}, true, "UI configuration is valid"},
		{[]string{"ready", "--config", path}, true, "UI and controller are ready"},
		{[]string{"serve", "--config", path, "extra"}, false, "unexpected arguments"},
		{[]string{"validate", "--config", "/missing/config.yaml"}, false, "read config"},
		{[]string{"unsupported"}, false, "usage: kee-route-manager-ui"},
	} {
		out, err := runUIProcess(t, tc.args)
		if (err == nil) != tc.success || !strings.Contains(out, tc.want) {
			t.Fatalf("args=%v err=%v output=%s", tc.args, err, out)
		}
	}
	// Switch after all request goroutines from the previous subprocess finish.
	upstream.Close()
	out, err := runUIProcess(t, []string{"ready", "--config", path})
	if err == nil || strings.Contains(out, "UI and controller are ready") {
		t.Fatalf("offline UI reported ready: %v %s", err, out)
	}
	for _, name := range []string{"untouched-state", "untouched.log"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), name)); !os.IsNotExist(err) {
			t.Fatalf("read-only command wrote %s: %v", name, err)
		}
	}
}

func runUIProcess(t *testing.T, command []string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"-test.run=^TestEntrypoint$"}
	if coverage := flag.Lookup("test.gocoverdir"); coverage != nil && coverage.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverage.Value.String())
	}
	args = append(args, "--")
	args = append(args, command...)
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), "KRM_TEST_UI_ENTRYPOINT=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
