// Package tlsutil owns shared TLS identity generation for the daemon and UI.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

// EnsureTLS keeps an existing configured identity or atomically creates a
// self-signed server identity when auto generation is enabled.
func EnsureTLS(c config.TLS, listen string) error {
	if !c.Enabled {
		return nil
	}
	if _, err := os.Stat(c.CertFile); err == nil {
		if _, err = os.Stat(c.KeyFile); err == nil {
			return nil
		}
	}
	if !c.AutoGenerate {
		return fmt.Errorf("TLS files missing")
	}
	if err := os.MkdirAll(filepath.Dir(c.CertFile), 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	hosts := append([]string(nil), c.Hosts...)
	host, _, _ := net.SplitHostPort(listen)
	if host != "" && host != "0.0.0.0" && host != "::" {
		hosts = append(hosts, host)
	}
	hosts = append(hosts, "localhost", "127.0.0.1", "::1")
	if ifaces, ifaceErr := net.Interfaces(); ifaceErr == nil {
		for _, iface := range ifaces {
			addrs, addrErr := iface.Addrs()
			if addrErr != nil {
				continue
			}
			for _, addr := range addrs {
				value := addr.String()
				if ip, _, parseErr := net.ParseCIDR(value); parseErr == nil {
					value = ip.String()
				}
				if ip := net.ParseIP(value); ip != nil && !ip.IsUnspecified() {
					hosts = append(hosts, ip.String())
				}
			}
		}
	}
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Kee Route Manager"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	seen := map[string]bool{}
	for _, entry := range hosts {
		entry = strings.TrimSpace(entry)
		key := strings.ToLower(entry)
		if entry == "" || seen[key] {
			continue
		}
		seen[key] = true
		if ip := net.ParseIP(entry); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, entry)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err = writeAtomic(c.CertFile, cert, 0o644); err != nil {
		return err
	}
	return writeAtomic(c.KeyFile, privateKey, 0o600)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".tls-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
