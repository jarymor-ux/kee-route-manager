package ui

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/tlsutil"
)

func TestPanelSNIPreservesPrimaryIdentityForOldNameAndIP(t *testing.T) {
	dir := t.TempDir()
	primary := config.TLS{Enabled: true, AutoGenerate: true, Hosts: []string{"old.home.arpa"}, CertFile: filepath.Join(dir, "primary.crt"), KeyFile: filepath.Join(dir, "primary.key")}
	alias := config.TLS{Enabled: true, AutoGenerate: true, Hosts: []string{"alice.jopa"}, CertFile: filepath.Join(dir, "alias.crt"), KeyFile: filepath.Join(dir, "alias.key")}
	for _, c := range []config.TLS{primary, alias} {
		if err := tlsutil.EnsureTLS(c, "127.0.0.1:443"); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.ReadFile(primary.CertFile)
	aliasPEM, _ := os.ReadFile(alias.CertFile)
	primary.AdditionalCertificates = []config.TLSCertificate{{Hostname: "alice.jopa", CertFile: alias.CertFile, KeyFile: alias.KeyFile}}
	c, err := panelTLS(primary)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	server.TLS = c
	server.StartTLS()
	defer server.Close()
	for _, name := range []string{"old.home.arpa", "alice.jopa", "127.0.0.1"} {
		roots := x509.NewCertPool()
		if name == "alice.jopa" {
			roots.AppendCertsFromPEM(aliasPEM)
		} else {
			roots.AppendCertsFromPEM(before)
		}
		conn, err := tls.Dial("tcp", server.Listener.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name != "alice.jopa" && !bytes.Equal(conn.ConnectionState().PeerCertificates[0].Raw, server.Certificate().Raw) {
			t.Fatal("old identity replaced")
		}
		conn.Close()
	}
	after, _ := os.ReadFile(primary.CertFile)
	if !bytes.Equal(before, after) {
		t.Fatal("primary certificate changed")
	}
	primary.AdditionalCertificates[0].Hostname = "other.home.arpa"
	if _, err = panelTLS(primary); err == nil {
		t.Fatal("accepted an alias identity with wrong SAN")
	}
}
