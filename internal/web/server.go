package web

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
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

func New(c config.Config, mgr *core.Manager, updater *update.Updater, restart func(context.Context) error) (*Server, error) {
	credentials, err := auth.LoadCredentials(c.Web.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("load credentials: %w", err)
	}
	server := &Server{
		cfg:      c,
		mgr:      mgr,
		updater:  updater,
		restart:  restart,
		creds:    credentials,
		sessions: auth.NewSessionStore(c.Web.SessionTTL.Duration),
		limiter:  auth.NewLimiter(8, 15*time.Minute),
	}
	server.handler = server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) ListenAndServe(ctx context.Context) error {
	server := &http.Server{
		Addr:              s.cfg.Web.Listen,
		Handler:           s.handler,
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
	if s.cfg.Web.TLS.Enabled {
		if err := EnsureTLS(s.cfg.Web.TLS, s.cfg.Web.Listen); err != nil {
			return err
		}
		err := server.ListenAndServeTLS(s.cfg.Web.TLS.CertFile, s.cfg.Web.TLS.KeyFile)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/readyz", s.readyz)
	mux.HandleFunc("/api/v1/auth/login", s.login)
	mux.HandleFunc("/api/v1/auth/logout", s.protect(http.MethodPost, s.logout, true))
	mux.HandleFunc("/api/v1/session", s.protect(http.MethodGet, s.session, false))
	mux.HandleFunc("/api/v1/status", s.protect(http.MethodGet, s.status, false))
	mux.HandleFunc("/api/v1/nodes", s.protect(http.MethodGet, s.nodes, false))
	mux.HandleFunc("/api/v1/events", s.protect(http.MethodGet, s.events, false))
	mux.HandleFunc("/api/v1/router/metrics", s.protect(http.MethodGet, s.metrics, false))
	mux.HandleFunc("/api/v1/router/clients", s.protect(http.MethodGet, s.clients, false))
	mux.HandleFunc("/api/v1/router/logs", s.protect(http.MethodGet, s.logs, false))
	mux.HandleFunc("/api/v1/router/diagnostics", s.protect(http.MethodGet, s.diagnostics, false))
	mux.HandleFunc("/api/v1/actions/benchmark", s.protect(http.MethodPost, s.benchmark, true))
	mux.HandleFunc("/api/v1/actions/xray-restart", s.protect(http.MethodPost, s.restartXray, true))
	mux.HandleFunc("/api/v1/actions/reboot", s.protect(http.MethodPost, s.reboot, true))
	mux.HandleFunc("/api/v1/actions/wake", s.protect(http.MethodPost, s.wake, true))
	mux.HandleFunc("/api/v1/actions/policy", s.protect(http.MethodPost, s.policy, true))
	mux.HandleFunc("/api/v1/actions/switch", s.protect(http.MethodPost, s.switchSlot, true))
	mux.HandleFunc("/api/v1/actions/direct", s.protect(http.MethodPost, s.direct, true))
	mux.HandleFunc("/api/v1/update/check", s.protect(http.MethodGet, s.updateCheck, false))
	mux.HandleFunc("/api/v1/update/apply", s.protect(http.MethodPost, s.updateApply, true))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		jsonError(w, http.StatusNotFound, "Kee Route Manager core exposes only /api/v1, /healthz and /readyz")
	})
	return s.security(mux)
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) protect(method string, fn func(http.ResponseWriter, *http.Request, auth.Session), mutation bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		session, err := auth.ReadSession(r, s.sessions)
		if err != nil {
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
			if !subtleEqual(r.Header.Get("X-KRM-CSRF"), session.CSRF) {
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
	code := http.StatusOK
	stateText := "ok"
	if !status.XrayRunning && !status.State.DirectMode {
		code = http.StatusServiceUnavailable
		stateText = "degraded"
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(code)
		return
	}
	writeJSON(w, code, map[string]any{
		"status":       stateText,
		"role":         "core",
		"version":      status.Version,
		"xray_running": status.XrayRunning,
		"direct_mode":  status.State.DirectMode,
		"time":         time.Now().UTC(),
	})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status := s.mgr.Status(r.Context())
	ready := status.XrayRunning || status.State.DirectMode
	code := http.StatusOK
	if !ready {
		code = http.StatusServiceUnavailable
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(code)
		return
	}
	writeJSON(w, code, map[string]any{"ready": ready, "version": status.Version})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ip := auth.RemoteIP(r)
	if !s.limiter.Allow(ip) {
		jsonError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	if len(request.Username) > 64 || len(request.Password) > 1024 {
		jsonError(w, http.StatusBadRequest, "credentials too long")
		return
	}
	if !auth.Verify(s.creds, request.Username, request.Password) {
		time.Sleep(250 * time.Millisecond)
		jsonError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.limiter.Reset(ip)
	session, err := s.sessions.Create(s.creds.Username, ip)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "session error")
		return
	}
	auth.SetCookie(w, session, s.cfg.Web.TLS.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"username": session.Username, "csrf": session.CSRF, "expires_at": session.ExpiresAt})
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request, session auth.Session) {
	s.sessions.Delete(session.ID)
	auth.ClearCookie(w, s.cfg.Web.TLS.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) session(w http.ResponseWriter, _ *http.Request, session auth.Session) {
	writeJSON(w, http.StatusOK, map[string]any{"username": session.Username, "csrf": session.CSRF, "expires_at": session.ExpiresAt})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	writeJSON(w, http.StatusOK, s.mgr.Status(r.Context()))
}

func (s *Server) nodes(w http.ResponseWriter, _ *http.Request, _ auth.Session) {
	writeJSON(w, http.StatusOK, s.mgr.Nodes())
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	writeJSON(w, http.StatusOK, s.mgr.Events(after, limit))
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request, _ auth.Session) {
	writeJSON(w, http.StatusOK, s.mgr.Metrics())
}

func (s *Server) clients(w http.ResponseWriter, _ *http.Request, _ auth.Session) {
	writeJSON(w, http.StatusOK, s.mgr.Clients())
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines == 0 {
		lines = 200
	}
	value, err := s.mgr.SystemLogs(r.Context(), lines)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": value})
}

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	value, err := s.mgr.Diagnostics(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": value})
}

func (s *Server) benchmark(w http.ResponseWriter, _ *http.Request, _ auth.Session) {
	if err := s.mgr.StartBenchmark("manual", "web"); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (s *Server) restartXray(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if err := s.mgr.RestartXray(r.Context()); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) reboot(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if err := s.mgr.Reboot(r.Context()); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (s *Server) wake(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var request struct {
		MAC string `json:"mac"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	if err := s.mgr.Wake(r.Context(), request.MAC); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) policy(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var request struct {
		MAC    string `json:"mac"`
		Policy string `json:"policy"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	if err := s.mgr.SetPolicy(r.Context(), request.MAC, request.Policy); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) switchSlot(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var request struct {
		Index int `json:"index"`
	}
	if err := decodeBody(w, r, &request); err != nil {
		return
	}
	if err := s.mgr.SwitchSlot(r.Context(), request.Index); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) direct(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if err := s.mgr.SwitchDirect(r.Context()); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if s.updater == nil || !s.updater.Enabled() {
		jsonError(w, http.StatusBadRequest, "updates unavailable")
		return
	}
	value, err := s.updater.Check(r.Context())
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) updateApply(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if s.updater == nil || !s.updater.Enabled() {
		jsonError(w, http.StatusBadRequest, "updates unavailable")
		return
	}
	value, err := s.updater.Check(r.Context())
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	pending, err := s.updater.Apply(r.Context(), value)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, pending)
	if s.restart != nil {
		go func() {
			time.Sleep(time.Second)
			_ = s.restart(context.Background())
		}()
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func jsonError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) || a == "" {
		return false
	}
	var value byte
	for i := range a {
		value |= a[i] ^ b[i]
	}
	return value == 0
}

func sameOrigin(r *http.Request, tlsEnabled bool) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	return strings.EqualFold(parsed.Scheme, scheme) && strings.EqualFold(parsed.Host, r.Host)
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
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Kee Route Manager Core"},
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
	if err = writeSecret(c.CertFile, cert, 0o644); err != nil {
		return err
	}
	return writeSecret(c.KeyFile, privateKey, 0o600)
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
