package ui

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

// Primary identity remains the fallback for existing hostnames and IP access.
// Explicit SNI aliases select only their own independently generated identity.
func panelTLS(c config.TLS) (*tls.Config, error) {
	primary, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, err
	}
	additional := make(map[string]*tls.Certificate, len(c.AdditionalCertificates))
	for _, pair := range c.AdditionalCertificates {
		cert, err := tls.LoadX509KeyPair(pair.CertFile, pair.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("additional UI TLS identity unavailable")
		}
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil || leaf.VerifyHostname(pair.Hostname) != nil || time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) {
			return nil, fmt.Errorf("additional UI TLS identity does not cover its hostname")
		}
		additional[pair.Hostname] = &cert
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{primary}, GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if cert := additional[strings.ToLower(hello.ServerName)]; cert != nil {
			return cert, nil
		}
		return &primary, nil
	}}, nil
}
