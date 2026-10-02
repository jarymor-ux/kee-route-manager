package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestYAMLPlainListScalarsContainingColons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.yaml")
	data := `instance:
  role: ui
web:
  tls:
    hosts:
      - ::1
      - 2001:db8::1
      - router.test
platform:
  xray_restart_command:
    - /bin/example
    - https://example.test/resource
ui:
  upstream: https://127.0.0.1:9443
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("valid plain YAML scalars rejected: %v", err)
	}
	if !reflect.DeepEqual(c.Web.TLS.Hosts, []string{"::1", "2001:db8::1", "router.test"}) {
		t.Fatalf("hosts=%v", c.Web.TLS.Hosts)
	}
	if !reflect.DeepEqual(c.Platform.XrayRestartCommand, []string{"/bin/example", "https://example.test/resource"}) {
		t.Fatalf("command=%v", c.Platform.XrayRestartCommand)
	}
}
