package ui

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/logging"
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed static/*
var staticFS embed.FS

func StaticHandler() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", files))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(405)
			return
		}
		switch r.URL.Path {
		case "/", "/sw.js", "/manifest.webmanifest":
		default:
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		if r.URL.Path == "/sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	return mux
}
func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
func Security(next http.Handler, tlsEnabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		if tlsEnabled && r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
func EnsureTLS(c config.TLS, listen string) error {
	if !c.Enabled {
		return nil
	}
	if _, e := os.Stat(c.CertFile); e == nil {
		if _, e = os.Stat(c.KeyFile); e == nil {
			return nil
		}
	}
	if !c.AutoGenerate {
		return fmt.Errorf("TLS files missing")
	}
	if e := os.MkdirAll(filepath.Dir(c.CertFile), 0700); e != nil {
		return e
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return e
	}
	hosts := append([]string(nil), c.Hosts...)
	host, _, _ := net.SplitHostPort(listen)
	if host != "" && host != "0.0.0.0" && host != "::" {
		hosts = append(hosts, host)
	}
	hosts = append(hosts, "localhost", "127.0.0.1", "::1")
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				host := addr.String()
				if h, _, err := net.ParseCIDR(host); err == nil {
					host = h.String()
				}
				if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
					hosts = append(hosts, ip.String())
				}
			}
		}
	}
	tmpl := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Kee Route Manager"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	seenHosts := map[string]bool{}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" || seenHosts[strings.ToLower(h)] {
			continue
		}
		seenHosts[strings.ToLower(h)] = true
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, e := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if e != nil {
		return e
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return e
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	priv := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if e = writeSecret(c.CertFile, cert, 0644); e != nil {
		return e
	}
	return writeSecret(c.KeyFile, priv, 0600)
}
func writeSecret(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".tls-*")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	_ = f.Chmod(mode)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if e2 := f.Close(); e == nil {
		e = e2
	}
	if e != nil {
		return e
	}
	return os.Rename(n, path)
}

func ProxyHandler(c config.Config) (http.Handler, error) {
	target, e := url.Parse(c.UIProxy.Upstream)
	if e != nil || target.Host == "" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("invalid UI upstream URL")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalHost := r.Host
		r.Header.Del("Forwarded")
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Forwarded-Proto")
		r.Header.Del("X-Forwarded-Host")
		baseDirector(r)
		r.Host = target.Host
		upstreamOrigin := target.Scheme + "://" + target.Host
		if r.Header.Get("Origin") != "" {
			r.Header.Set("Origin", upstreamOrigin)
		}
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				u.Scheme, u.Host = target.Scheme, target.Host
				r.Header.Set("Referer", u.String())
			}
		}
		r.Header.Set("X-Forwarded-Host", originalHost)
	}
	timeout := c.UIProxy.RequestTimeout.Duration
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tlsConfig, e := upstreamTLS(c.UIProxy)
	if e != nil {
		return nil, e
	}
	proxy.Transport = &http.Transport{
		TLSClientConfig:       tlsConfig, // operator-controlled local upstream
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		DialContext:           (&net.Dialer{Timeout: minDuration(timeout, 10*time.Second), KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   minDuration(timeout, 10*time.Second),
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       60 * time.Second,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) { jsonError(w, 502, "upstream unavailable") }
	files := StaticHandler()
	loginLimiter := auth.NewLimiter(8, 15*time.Minute)
	proxy.ModifyResponse = func(response *http.Response) error {
		r := response.Request
		if r != nil && r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login" && response.StatusCode == http.StatusOK {
			loginLimiter.Reset(auth.RemoteIP(r))
		}
		return nil
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			scheme := "http"
			if c.Web.TLS.Enabled {
				scheme = "https"
			}
			if err != nil || u.Scheme != scheme || !strings.EqualFold(u.Host, r.Host) {
				jsonError(w, 403, "origin rejected")
				return
			}
		}
		if r.URL.Path == "/api/v1/auth/login" && r.Method == http.MethodPost {
			if !loginLimiter.Allow(auth.RemoteIP(r)) {
				jsonError(w, http.StatusTooManyRequests, "too many attempts")
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	mux.Handle("/healthz", proxy)
	mux.Handle("/", files)
	return Security(mux, c.Web.TLS.Enabled), nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func upstreamTLS(c config.UIProxy) (*tls.Config, error) {
	t := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.InsecureTLS}
	if c.InsecureTLS {
		log.Print("WARNING: UI upstream TLS certificate verification is disabled")
	}
	if c.UpstreamCAFile != "" {
		b, e := os.ReadFile(c.UpstreamCAFile)
		if e != nil {
			return nil, e
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(b) {
			return nil, fmt.Errorf("upstream CA file contains no certificates")
		}
		t.RootCAs = roots
	}
	if c.UpstreamSPKISHA256 != "" {
		pin := strings.TrimPrefix(c.UpstreamSPKISHA256, "sha256/")
		expected, e := base64.StdEncoding.DecodeString(pin)
		if e != nil || len(expected) != 32 {
			expected, e = hex.DecodeString(pin)
		}
		if e != nil || len(expected) != 32 {
			return nil, fmt.Errorf("invalid upstream SPKI SHA-256 pin")
		}
		t.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("upstream peer certificate missing")
			}
			digest := sha256.Sum256(cs.PeerCertificates[0].RawSubjectPublicKeyInfo)
			if subtle.ConstantTimeCompare(expected, digest[:]) != 1 {
				return fmt.Errorf("upstream SPKI pin mismatch")
			}
			return nil
		}
	}
	return t, nil
}
func Serve(ctx context.Context, c config.Config) error {
	if c.Instance.Role != "ui" || !c.UIProxy.Enabled || !c.Web.Enabled {
		return fmt.Errorf("UI requires instance.role=ui, ui.enabled=true and web.enabled=true")
	}
	logs, e := logging.Setup(c.Paths.LogFile)
	if e != nil {
		return e
	}
	defer logs.Close()
	h, e := ProxyHandler(c)
	if e != nil {
		return e
	}
	if e = EnsureTLS(c.Web.TLS, c.Web.Listen); e != nil {
		return e
	}
	srv := &http.Server{Addr: c.Web.Listen, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		<-ctx.Done()
		cc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(cc)
	}()
	if c.Web.TLS.Enabled {
		e = srv.ListenAndServeTLS(c.Web.TLS.CertFile, c.Web.TLS.KeyFile)
	} else {
		e = srv.ListenAndServe()
	}
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
