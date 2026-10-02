package ui

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

//go:embed static/*
var staticFS embed.FS

func StaticHandler() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", files))
	mux.Handle("/manifest.webmanifest", files)
	mux.Handle("/sw.js", files)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "UI unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})
	return Security(mux)
}

func ProxyHandler(c config.Config) (http.Handler, error) {
	target, err := url.Parse(c.UIProxy.Upstream)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("invalid UI upstream")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		baseDirector(r)
		r.Host = target.Host
		upstreamOrigin := target.Scheme + "://" + target.Host
		if r.Header.Get("Origin") != "" {
			r.Header.Set("Origin", upstreamOrigin)
		}
		if ref := r.Header.Get("Referer"); ref != "" {
			if parsed, parseErr := url.Parse(ref); parseErr == nil {
				parsed.Scheme, parsed.Host = target.Scheme, target.Host
				r.Header.Set("Referer", parsed.String())
			}
		}
		r.Header.Set("X-Forwarded-Host", r.Header.Get("Host"))
	}
	timeout := c.UIProxy.RequestTimeout.Duration
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.UIProxy.InsecureTLS {
		tlsConfig.InsecureSkipVerify = true // operator-selected for a local self-signed controller
	}
	if pin := strings.TrimSpace(os.Getenv("KRM_UI_UPSTREAM_SHA256")); pin != "" {
		want := strings.ToLower(strings.ReplaceAll(pin, ":", ""))
		if _, err := hex.DecodeString(want); err != nil || len(want) != sha256.Size*2 {
			return nil, fmt.Errorf("KRM_UI_UPSTREAM_SHA256 must be a SHA-256 certificate fingerprint")
		}
		tlsConfig.InsecureSkipVerify = true
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return fmt.Errorf("upstream certificate missing")
			}
			got := sha256.Sum256(state.PeerCertificates[0].Raw)
			if hex.EncodeToString(got[:]) != want {
				return fmt.Errorf("upstream certificate fingerprint mismatch")
			}
			return nil
		}
	}
	proxy.Transport = &http.Transport{
		TLSClientConfig:       tlsConfig,
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		DialContext:           (&net.Dialer{Timeout: minDuration(timeout, 10*time.Second), KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   minDuration(timeout, 10*time.Second),
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       60 * time.Second,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream unavailable"}` + "\n"))
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", proxy)
	mux.Handle("/healthz", proxy)
	mux.Handle("/readyz", proxy)
	mux.Handle("/", StaticHandler())
	return Security(mux), nil
}

func Security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

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
	tmpl := x509.Certificate{
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
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, entry)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err = writeSecret(c.CertFile, cert, 0o644); err != nil {
		return err
	}
	return writeSecret(c.KeyFile, privateKey, 0o600)
}

func ListenAndServe(ctx context.Context, c config.Config, handler http.Handler) error {
	if err := EnsureTLS(c.Web.TLS, c.Web.Listen); err != nil {
		return err
	}
	server := &http.Server{
		Addr:              c.Web.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	var err error
	if c.Web.TLS.Enabled {
		err = server.ListenAndServeTLS(c.Web.TLS.CertFile, c.Web.TLS.KeyFile)
	} else {
		err = server.ListenAndServe()
	}
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func writeSecret(path string, data []byte, mode os.FileMode) error {
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

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
