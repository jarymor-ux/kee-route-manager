package web

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
)

//go:embed static/*
var staticFS embed.FS

type Server struct {
	cfg      config.Config
	mgr      *core.Manager
	updater  *update.Updater
	restart  func(context.Context) error
	creds    auth.Credentials
	sessions *auth.SessionStore
	limiter  *auth.Limiter
	handler  http.Handler
}

func New(c config.Config, mgr *core.Manager, up *update.Updater, restart func(context.Context) error) (*Server, error) {
	creds, e := auth.LoadCredentials(c.Web.CredentialsFile)
	if e != nil {
		return nil, fmt.Errorf("load credentials: %w", e)
	}
	s := &Server{cfg: c, mgr: mgr, updater: up, restart: restart, creds: creds, sessions: auth.NewSessionStore(c.Web.SessionTTL.Duration), limiter: auth.NewLimiter(8, 15*time.Minute)}
	s.handler = s.routes()
	return s, nil
}
func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{Addr: s.cfg.Web.Listen, Handler: s.handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_ = srv.Shutdown(shutdown)
	}()
	if s.cfg.Web.TLS.Enabled {
		if e := EnsureTLS(s.cfg.Web.TLS, s.cfg.Web.Listen); e != nil {
			return e
		}
		e := srv.ListenAndServeTLS(s.cfg.Web.TLS.CertFile, s.cfg.Web.TLS.KeyFile)
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	}
	e := srv.ListenAndServe()
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/api/v1/auth/login", s.login)
	mux.HandleFunc("/api/v1/auth/logout", s.protect(s.logout, true))
	mux.HandleFunc("/api/v1/session", s.protect(s.session, false))
	mux.HandleFunc("/api/v1/status", s.protect(s.status, false))
	mux.HandleFunc("/api/v1/nodes", s.protect(s.nodes, false))
	mux.HandleFunc("/api/v1/events", s.protect(s.events, false))
	mux.HandleFunc("/api/v1/router/metrics", s.protect(s.metrics, false))
	mux.HandleFunc("/api/v1/router/clients", s.protect(s.clients, false))
	mux.HandleFunc("/api/v1/router/logs", s.protect(s.logs, false))
	mux.HandleFunc("/api/v1/router/diagnostics", s.protect(s.diagnostics, false))
	mux.HandleFunc("/api/v1/actions/benchmark", s.protect(s.benchmark, true))
	mux.HandleFunc("/api/v1/actions/xray-restart", s.protect(s.restartXray, true))
	mux.HandleFunc("/api/v1/actions/reboot", s.protect(s.reboot, true))
	mux.HandleFunc("/api/v1/actions/wake", s.protect(s.wake, true))
	mux.HandleFunc("/api/v1/actions/policy", s.protect(s.policy, true))
	mux.HandleFunc("/api/v1/actions/switch", s.protect(s.switchSlot, true))
	mux.HandleFunc("/api/v1/actions/direct", s.protect(s.direct, true))
	mux.HandleFunc("/api/v1/update/check", s.protect(s.updateCheck, false))
	mux.HandleFunc("/api/v1/update/apply", s.protect(s.updateApply, true))
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	mux.Handle("/", files)
	return s.security(mux)
}
func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) protect(fn func(http.ResponseWriter, *http.Request, auth.Session), mutation bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, e := auth.ReadSession(r, s.sessions)
		if e != nil {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if session.RemoteIP != "" && session.RemoteIP != auth.RemoteIP(r) {
			s.sessions.Delete(session.ID)
			auth.ClearCookie(w, s.cfg.Web.TLS.Enabled)
			jsonError(w, http.StatusUnauthorized, "session address changed")
			return
		}
		if mutation {
			if !isMutation(r.Method) {
				jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if subtleEqual(r.Header.Get("X-KRM-CSRF"), session.CSRF) == false {
				jsonError(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
			if !sameOrigin(r, s.cfg.Web.TLS.Enabled) {
				jsonError(w, http.StatusForbidden, "origin rejected")
				return
			}
		}
		fn(w, r, session)
	}
}
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status := s.mgr.Status(r.Context())
	running := status.XrayRunning
	direct := status.State.DirectMode
	code, stateText := http.StatusOK, "ok"
	if !running {
		code, stateText = http.StatusServiceUnavailable, "degraded"
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(code)
		return
	}
	writeJSON(w, code, map[string]any{"status": stateText, "role": "controller", "version": status.Version, "xray_running": running, "direct_mode": direct, "time": time.Now().UTC()})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, 405, "method not allowed")
		return
	}
	ip := auth.RemoteIP(r)
	if !s.limiter.Allow(ip) {
		jsonError(w, 429, "too many attempts")
		return
	}
	var q struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if e := decodeBody(w, r, &q); e != nil {
		return
	}
	if !auth.Verify(s.creds, q.Username, q.Password) {
		time.Sleep(250 * time.Millisecond)
		jsonError(w, 401, "invalid credentials")
		return
	}
	s.limiter.Reset(ip)
	session, e := s.sessions.Create(s.creds.Username, ip)
	if e != nil {
		jsonError(w, 500, "session error")
		return
	}
	auth.SetCookie(w, session, s.cfg.Web.TLS.Enabled)
	writeJSON(w, 200, map[string]any{"username": session.Username, "csrf": session.CSRF, "expires_at": session.ExpiresAt})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request, v auth.Session) {
	s.sessions.Delete(v.ID)
	auth.ClearCookie(w, s.cfg.Web.TLS.Enabled)
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) session(w http.ResponseWriter, r *http.Request, v auth.Session) {
	if r.Method != http.MethodGet {
		jsonError(w, 405, "method not allowed")
		return
	}
	writeJSON(w, 200, map[string]any{"username": v.Username, "csrf": v.CSRF, "expires_at": v.ExpiresAt})
}
func (s *Server) status(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	writeJSON(w, 200, s.mgr.Status(r.Context()))
}
func (s *Server) nodes(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	writeJSON(w, 200, s.mgr.Nodes())
}
func (s *Server) events(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	writeJSON(w, 200, s.mgr.Events(after, limit))
}
func (s *Server) metrics(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	writeJSON(w, 200, s.mgr.Metrics())
}
func (s *Server) clients(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	writeJSON(w, 200, s.mgr.Clients())
}
func (s *Server) logs(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n == 0 {
		n = 200
	}
	v, e := s.mgr.SystemLogs(r.Context(), n)
	if e != nil {
		jsonError(w, 500, e.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"lines": v})
}
func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	v, e := s.mgr.Diagnostics(r.Context())
	if e != nil {
		jsonError(w, 500, e.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"output": v})
}
func (s *Server) benchmark(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if op := s.mgr.Status(r.Context()).Operation; op != nil && op.Status == "running" {
		jsonError(w, 409, "operation already running")
		return
	}
	go func() { _ = s.mgr.ForceBenchmark(context.Background()) }()
	writeJSON(w, 202, map[string]any{"accepted": true})
}
func (s *Server) restartXray(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if e := s.mgr.RestartXray(r.Context()); e != nil {
		jsonError(w, 409, e.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) reboot(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if e := s.mgr.Reboot(r.Context()); e != nil {
		jsonError(w, 409, e.Error())
		return
	}
	writeJSON(w, 202, map[string]any{"accepted": true})
}
func (s *Server) wake(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var q struct {
		MAC string `json:"mac"`
	}
	if e := decodeBody(w, r, &q); e != nil {
		return
	}
	if e := s.mgr.Wake(r.Context(), q.MAC); e != nil {
		jsonError(w, 409, e.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) policy(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var q struct {
		MAC    string `json:"mac"`
		Policy string `json:"policy"`
	}
	if e := decodeBody(w, r, &q); e != nil {
		return
	}
	if e := s.mgr.SetPolicy(r.Context(), q.MAC, q.Policy); e != nil {
		jsonError(w, 409, e.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) switchSlot(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var q struct {
		Index int `json:"index"`
	}
	if e := decodeBody(w, r, &q); e != nil {
		return
	}
	if e := s.mgr.SwitchSlot(r.Context(), q.Index); e != nil {
		jsonError(w, 409, e.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) direct(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if e := s.mgr.SwitchDirect(r.Context()); e != nil {
		jsonError(w, 409, e.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if s.updater == nil {
		jsonError(w, 400, "updates unavailable")
		return
	}
	v, e := s.updater.Check(r.Context())
	if e != nil {
		jsonError(w, 502, e.Error())
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if s.updater == nil {
		jsonError(w, 400, "updates unavailable")
		return
	}
	v, e := s.updater.Check(r.Context())
	if e != nil {
		jsonError(w, 502, e.Error())
		return
	}
	p, e := s.updater.Apply(r.Context(), v)
	if e != nil {
		jsonError(w, 500, e.Error())
		return
	}
	writeJSON(w, 202, p)
	if s.restart != nil {
		go func() { time.Sleep(time.Second); _ = s.restart(context.Background()) }()
	}
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if e := dec.Decode(v); e != nil {
		jsonError(w, 400, "invalid JSON: "+e.Error())
		return e
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
func isMutation(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}
func subtleEqual(a, b string) bool {
	if len(a) != len(b) || a == "" {
		return false
	}
	v := byte(0)
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
func sameOrigin(r *http.Request, tls bool) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, e := url.Parse(o)
	if e != nil {
		return false
	}
	scheme := "http"
	if tls {
		scheme = "https"
	}
	return strings.EqualFold(u.Scheme, scheme) && strings.EqualFold(u.Host, r.Host)
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
	if e != nil {
		return nil, e
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
			if u, err := url.Parse(ref); err == nil {
				u.Scheme, u.Host = target.Scheme, target.Host
				r.Header.Set("Referer", u.String())
			}
		}
		r.Header.Set("X-Forwarded-Host", r.Header.Get("Host"))
	}
	timeout := c.UIProxy.RequestTimeout.Duration
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	proxy.Transport = &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: c.UIProxy.InsecureTLS}, // operator-controlled local upstream
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		DialContext:           (&net.Dialer{Timeout: minDuration(timeout, 10*time.Second), KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   minDuration(timeout, 10*time.Second),
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       60 * time.Second,
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) { jsonError(w, 502, "upstream unavailable") }
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.Handle("/api/", proxy)
	mux.Handle("/healthz", proxy)
	mux.Handle("/", files)
	return (&Server{cfg: c}).security(mux), nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
