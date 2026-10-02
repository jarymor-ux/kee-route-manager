package main

import (
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
