package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
	"github.com/jarymor-ux/kee-route-manager/internal/web/ui"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Controller interface {
	Status(context.Context) core.Status
	Readiness() error
	Nodes() []model.NodeView
	Events(uint64, int) []event.Event
	Metrics() platform.Metrics
	Clients() core.ClientsSnapshot
	SystemLogs(context.Context, int) (string, error)
	Diagnostics(context.Context) (string, error)
	RequestBenchmark(context.Context, string) (*operation.Operation, error)
	RestartXray(context.Context) error
	Reboot(context.Context) error
	Wake(context.Context, string) error
	SetPolicy(context.Context, string, string) error
	SwitchSlot(context.Context, int) error
	SwitchDirect(context.Context) error
	RestoreOriginalXray(context.Context) error
}
type Server struct {
	cfg      config.Config
	mgr      Controller
	updater  *update.Updater
	restart  func(context.Context) error
	creds    auth.Credentials
	sessions *auth.SessionStore
	limiter  *auth.Limiter
	handler  http.Handler
}

func New(c config.Config, mgr Controller, up *update.Updater, restart func(context.Context) error) (*Server, error) {
	var creds auth.Credentials
	if c.API.Enabled {
		var e error
		creds, e = auth.LoadCredentials(c.Web.CredentialsFile)
		if e != nil {
			return nil, fmt.Errorf("load credentials: %w", e)
		}
	}
	s := &Server{cfg: c, mgr: mgr, updater: up, restart: restart, creds: creds, sessions: auth.NewSessionStore(c.Web.SessionTTL.Duration), limiter: auth.NewLimiter(8, 15*time.Minute)}
	s.handler = s.routes()
	return s, nil
}
func (s *Server) Handler() http.Handler { return s.handler }
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{Addr: s.cfg.API.Listen, Handler: s.handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_ = srv.Shutdown(shutdown)
	}()
	if s.cfg.API.TLS.Enabled {
		if e := EnsureTLS(s.cfg.API.TLS, s.cfg.API.Listen); e != nil {
			return e
		}
		e := srv.ListenAndServeTLS(s.cfg.API.TLS.CertFile, s.cfg.API.TLS.KeyFile)
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	return s.security(mux)
}

type auditResponse struct {
	http.ResponseWriter
	status int
}

func (w *auditResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *auditResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (s *Server) security(next http.Handler) http.Handler {
	return ui.Security(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 16)
		if _, e := rand.Read(b); e != nil {
			jsonError(w, 503, "request unavailable")
			return
		}
		id := hex.EncodeToString(b)
		w.Header().Set("X-Request-ID", id)
		aw := &auditResponse{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(aw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("access request_id=%s method=%s path=%q status=%d duration_ms=%d", id, r.Method, r.URL.EscapedPath(), aw.status, time.Since(start).Milliseconds())
		}
	}), s.cfg.API.TLS.Enabled)
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
			auth.ClearCookie(w, s.cfg.API.TLS.Enabled)
			jsonError(w, http.StatusUnauthorized, "session address changed")
			return
		}
		if !mutation && r.Method != http.MethodGet {
			jsonError(w, 405, "method not allowed")
			return
		}
		if mutation {
			if r.Method != http.MethodPost {
				jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if !subtleEqual(r.Header.Get("X-KRM-CSRF"), session.CSRF) {
				jsonError(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
			if !sameOrigin(r, s.cfg.API.TLS.Enabled) {
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
	if s.mgr.Readiness() != nil {
		code, stateText = http.StatusServiceUnavailable, "degraded"
	} else if !running {
		stateText = "safe_degraded"
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
	if !sameOrigin(r, s.cfg.API.TLS.Enabled) {
		jsonError(w, 403, "origin rejected")
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
	auth.SetCookie(w, session, s.cfg.API.TLS.Enabled)
	writeJSON(w, 200, map[string]any{"username": session.Username, "csrf": session.CSRF, "expires_at": session.ExpiresAt})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request, v auth.Session) {
	s.sessions.Delete(v.ID)
	auth.ClearCookie(w, s.cfg.API.TLS.Enabled)
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
	v := s.mgr.Status(r.Context())
	if v.Operation != nil && v.Operation.Error != "" {
		v.Operation.Error = "operation failed; consult local logs"
	}
	if v.State.XrayLastError != "" {
		v.State.XrayLastError = "tunnel operation failed; consult local logs"
	}
	v.State.LastHealthMessage = redact.Text(v.State.LastHealthMessage)
	v.State.LastSwitchReason = redact.Text(v.State.LastSwitchReason)
	for k, x := range v.State.Sources {
		x.Name = redact.Text(x.Name)
		x.LastError = redact.Text(x.LastError)
		v.State.Sources[k] = x
	}
	for k, x := range v.State.Measurements {
		x.Error = redact.Text(x.Error)
		v.State.Measurements[k] = x
	}
	v.State.LastBenchmark.Error = redact.Text(v.State.LastBenchmark.Error)
	for i := range v.State.LastBenchmark.Results {
		v.State.LastBenchmark.Results[i].Error = redact.Text(v.State.LastBenchmark.Results[i].Error)
	}
	writeJSON(w, 200, v)
}
func (s *Server) nodes(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	v := s.mgr.Nodes()
	for i := range v {
		v[i].Label = redact.Text(v[i].Label)
		v[i].Measurement.Error = redact.Text(v[i].Measurement.Error)
	}
	writeJSON(w, 200, v)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	v := s.mgr.Events(after, limit)
	for i := range v {
		v[i].Message = redact.Text(v[i].Message)
		fields := make(map[string]any, len(v[i].Fields))
		for k, x := range v[i].Fields {
			if value, ok := x.(string); ok {
				x = redact.Text(value)
			}
			fields[k] = x
		}
		v[i].Fields = fields
	}
	writeJSON(w, 200, v)
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
		jsonError(w, 500, "operation failed")
		return
	}
	writeJSON(w, 200, map[string]any{"output": redact.Text(v)})
}
func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	v, e := s.mgr.Diagnostics(r.Context())
	if e != nil {
		jsonError(w, 500, "operation failed")
		return
	}
	writeJSON(w, 200, map[string]any{"output": redact.Diagnostics(v)})
}
func (s *Server) benchmark(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	op, e := s.mgr.RequestBenchmark(r.Context(), "http")
	if e != nil {
		jsonError(w, 409, "operation unavailable")
		return
	}
	writeJSON(w, 202, map[string]any{"accepted": true, "operation_id": op.ID})
}
func (s *Server) restartXray(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if e := s.mgr.RestartXray(r.Context()); e != nil {
		jsonError(w, 409, "operation rejected")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) reboot(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if e := s.mgr.Reboot(r.Context()); e != nil {
		jsonError(w, 409, "operation rejected")
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
		jsonError(w, 409, "operation rejected")
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
		jsonError(w, 409, "operation rejected")
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
		jsonError(w, 409, "operation rejected")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) direct(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if e := s.mgr.SwitchDirect(r.Context()); e != nil {
		jsonError(w, 409, "operation rejected")
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
		jsonError(w, 502, "upstream unavailable")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	jsonError(w, http.StatusNotImplemented, "automatic update apply disabled; install a verified release manually")
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if e := dec.Decode(v); e != nil {
		jsonError(w, 400, "invalid JSON")
		return e
	}
	if e := dec.Decode(new(any)); e != io.EOF {
		jsonError(w, 400, "invalid JSON")
		return fmt.Errorf("trailing JSON data")
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

func EnsureTLS(c config.TLS, listen string) error        { return ui.EnsureTLS(c, listen) }
func ProxyHandler(c config.Config) (http.Handler, error) { return ui.ProxyHandler(c) }

// LocalHandler is served exclusively on the owner-only Unix socket. Never mount
// it on a TCP listener; filesystem permissions provide local authorization.
func (s *Server) LocalHandler() http.Handler {
	mux := http.NewServeMux()
	register := func(path, method string, fn func(http.ResponseWriter, *http.Request, auth.Session)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != method {
				jsonError(w, 405, "method not allowed")
				return
			}
			fn(w, r, auth.Session{})
		})
	}
	register("/api/v1/status", http.MethodGet, s.status)
	register("/api/v1/actions/benchmark", http.MethodPost, s.benchmark)
	register("/api/v1/actions/switch", http.MethodPost, s.switchSlot)
	register("/api/v1/actions/direct", http.MethodPost, s.direct)
	register("/api/v1/update/check", http.MethodGet, s.updateCheck)
	register("/api/v1/update/apply", http.MethodPost, s.updateApply)
	register("/api/v1/actions/restore-xray", http.MethodPost, func(w http.ResponseWriter, r *http.Request, _ auth.Session) {
		if e := s.mgr.RestoreOriginalXray(r.Context()); e != nil {
			jsonError(w, 409, "restore rejected")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("/healthz", s.healthz)
	return s.security(mux)
}
