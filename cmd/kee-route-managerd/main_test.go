package main

import (
	"context"
	"crypto/tls"
	"flag"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTLSInitReturnsCertificatePath(t *testing.T) {
	c := config.Default()
	d := t.TempDir()
	c.API.TLS.CertFile = filepath.Join(d, "api.crt")
	c.API.TLS.KeyFile = filepath.Join(d, "api.key")
	path, e := initTLS(c)
	if e != nil {
		t.Fatal(e)
	}
	if path != c.API.TLS.CertFile {
		t.Fatalf("tls-init path=%q", path)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal(e)
	}
	c.API.Enabled = false
	if _, e = initTLS(c); e == nil {
		t.Fatal("TLS init accepted disabled API")
	}
}

func TestEntrypoint(t *testing.T) {
	if os.Getenv("KRM_TEST_DAEMON_ENTRYPOINT") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"kee-route-managerd"}, os.Args[i+1:]...)
				main()
				return
			}
		}
		t.Fatal("helper arguments missing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "controller.yaml")
	template, err := os.ReadFile("../../configs/linux-systemd.yaml")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.NewReplacer("/etc/kee-route-manager/", dir+"/config/", "/var/lib/kee-route-manager", dir+"/state", "/var/cache/kee-route-manager", dir+"/cache", "/run/kee-route-manager", dir+"/run").Replace(string(template))
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	tlsPath := filepath.Join(dir, "controller-tls.yaml")
	tlsBlock := "    enabled: false\n    auto_generate: false\n    cert_file: \"\"\n    key_file: \"\"\n    hosts: []"
	tlsEnabledBlock := "    enabled: true\n    auto_generate: true\n    cert_file: " + filepath.Join(dir, "config", "api.crt") + "\n    key_file: " + filepath.Join(dir, "config", "api.key") + "\n    hosts: [127.0.0.1, localhost]"
	tlsBody := strings.Replace(body, tlsBlock, tlsEnabledBlock, 1)
	if tlsBody == body {
		t.Fatal("controller API TLS fixture block not found")
	}
	if err := os.WriteFile(tlsPath, []byte(tlsBody), 0600); err != nil {
		t.Fatal(err)
	}
	uiPath := filepath.Join(dir, "ui.yaml")
	if err := os.WriteFile(uiPath, []byte("instance:\n  role: ui\nui:\n  upstream: https://127.0.0.1:9443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args    []string
		success bool
		want    string
	}{
		{[]string{"version"}, true, "kee-route-managerd " + version},
		{[]string{"validate", "--config", path}, true, "Controller configuration is valid"},
		{[]string{"tls-init", "--config", tlsPath}, true, filepath.Join(dir, "config", "api.crt")},
		{[]string{"serve", "--config", uiPath}, false, "daemon requires instance.role=controller"},
		{[]string{"validate", "--config", "/missing/config.yaml"}, false, "read config"},
		{[]string{"unsupported"}, false, "usage: kee-route-managerd"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		args := []string{"-test.run=^TestEntrypoint$"}
		if coverage := flag.Lookup("test.gocoverdir"); coverage != nil && coverage.Value.String() != "" {
			args = append(args, "-test.gocoverdir="+coverage.Value.String())
		}
		args = append(args, "--")
		args = append(args, tc.args...)
		cmd := exec.CommandContext(ctx, os.Args[0], args...)
		cmd.Env = append(os.Environ(), "KRM_TEST_DAEMON_ENTRYPOINT=1")
		out, err := cmd.CombinedOutput()
		cancel()
		if (err == nil) != tc.success || !strings.Contains(string(out), tc.want) {
			t.Fatalf("args=%v err=%v output=%s", tc.args, err, out)
		}
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(dir, "config", "api.crt"), filepath.Join(dir, "config", "api.key")); err != nil {
		t.Fatalf("tls-init produced unusable certificate: %v", err)
	}
	for _, name := range []string{"state", "cache", "run"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("offline command wrote %s: %v", name, err)
		}
	}
}
func TestDaemonRejectsUIConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.yaml")
	if e := os.WriteFile(path, []byte("instance:\n  role: ui\nui:\n  upstream: https://127.0.0.1:9443\n"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e := controllerConfig([]string{"--config", path}, "validate")
	if e == nil || !strings.Contains(e.Error(), "instance.role=controller") {
		t.Fatalf("daemon accepts UI configuration: %v", e)
	}
}
