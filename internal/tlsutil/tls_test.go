package tlsutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestEnsureTLS(t *testing.T) {
	dir := t.TempDir()
	cfg := config.TLS{
		Enabled:      true,
		AutoGenerate: true,
		CertFile:     filepath.Join(dir, "cert.pem"),
		KeyFile:      filepath.Join(dir, "key.pem"),
		Hosts:        []string{"krm.local"},
	}
	if err := EnsureTLS(cfg, "127.0.0.1:9443"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.CertFile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.KeyFile); err != nil {
		t.Fatal(err)
	}
}
