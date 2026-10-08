package config

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestPanelAddressValidationAndPatchesPreservePrimaryTrust(t *testing.T) {
	for _, name := range []string{"alice.jopa", "alice.home.arpa", "panel"} {
		if !ValidPanelHostname(name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"", "Alice.Jopa", "alice.local", "alice.localhost", "localhost", "192.168.1.1", "a..b", "bad/name", "a;reboot", "-bad.example", "bad_.example"} {
		if ValidPanelHostname(name) {
			t.Fatal(name)
		}
	}
	for _, port := range []int{0, -1, 65536, 9443} {
		if ValidPanelPort(port) {
			t.Fatal(port)
		}
	}
	if !ValidPanelAddressHost("192.168.1.1", "192.168.1.1") || ValidPanelAddressHost("192.168.1.2", "192.168.1.1") || PanelURL("::1", 443) != "https://[::1]" {
		t.Fatal("literal address validation")
	}
	path := filepath.Join(t.TempDir(), "ui.yaml")
	data := []byte("schema_version: 1\ninstance:\n  role: ui\nweb:\n  listen: 192.168.1.1:443 # keep endpoint comment\n  tls:\n    cert_file: old.crt # retain primary trust\n    key_file: old.key\n    hosts: [alice.jopa]\nui:\n  upstream: https://127.0.0.1:9443\n  upstream_ca_file: core.crt\n")
	crt := TLSCertificate{Hostname: "new.jopa", CertFile: filepath.Join(filepath.Dir(path), "new.crt"), KeyFile: filepath.Join(filepath.Dir(path), "new.key")}
	out, c, err := PanelConfig(data, path, PanelInput{Hostname: "new.jopa", Port: 9444}, []TLSCertificate{crt})
	if err != nil {
		t.Fatal(err)
	}
	if c.Web.Listen != "192.168.1.1:9444" || c.Web.TLS.CertFile != filepath.Join(filepath.Dir(path), "old.crt") || c.UIProxy.UpstreamCAFile != filepath.Join(filepath.Dir(path), "core.crt") || !bytes.Contains(out, []byte("retain primary trust")) {
		t.Fatal("non-panel infrastructure changed")
	}
	c.API.TLS.AdditionalCertificates = []TLSCertificate{crt}
	if c.Validate() == nil {
		t.Fatal("controller API accepted UI SNI certificates")
	}
}
