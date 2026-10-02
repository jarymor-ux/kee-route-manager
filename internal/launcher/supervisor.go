package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
	"github.com/jarymor-ux/kee-route-manager/internal/logging"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
)

type Status struct {
	Enabled         bool                `json:"enabled"`
	Launcher        bool                `json:"launcher"`
	CurrentVersion  string              `json:"current_version"`
	PreviousVersion string              `json:"previous_version,omitempty"`
	Phase           string              `json:"phase"`
	Applying        bool                `json:"applying"`
	CheckedAt       time.Time           `json:"checked_at,omitempty"`
	LastError       string              `json:"last_error,omitempty"`
	LastResult      string              `json:"last_result,omitempty"`
	Check           *update.CheckResult `json:"check,omitempty"`
}

type supervisor struct {
	c          config.Config
	configFile string
	ctx        context.Context
	mu         sync.Mutex // record and public status
	rec        record
	status     Status
	closing    bool
	transition sync.Mutex // processes and every version transition
	checkMu    sync.Mutex
	daemon, ui *child
	wg         sync.WaitGroup
	output     io.Writer
}

// Serve is the only process supervisor. Mutable controller state remains owned
// exclusively by its daemon child; even rollback never restores state snapshots.
func Serve(ctx context.Context, configFile string) error {
	c, err := config.Load(configFile)
	if err != nil {
		return err
	}
	if c.Instance.Role != "controller" {
		return errors.New("launcher requires controller role")
	}
	if err = privateDir(c.Update.InstallDir); err != nil {
		return err
	}
	lock, err := daemonlock.Acquire(filepath.Join(c.Update.InstallDir, "launcher.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	rec, err := loadRecord(c.Update.InstallDir)
	if err != nil {
		return err
	}
	closer, err := logging.Setup(filepath.Join(c.Update.InstallDir, "launcher.log"))
	if err != nil {
		return err
	}
	defer closer.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &supervisor{c: c, configFile: configFile, ctx: ctx, rec: rec, output: log.Writer()}
	s.status = Status{Enabled: c.Update.Enabled, Launcher: true, Phase: "idle"}
	if rec.Phase == "trial" {
		rec.Candidate, rec.CandidateDigest, rec.Nonce, rec.Phase = "", "", "", "committed"
		rec.LastResult, rec.LastError = "rolled_back", "interrupted trial; previous version restored"
		if err = s.persist(rec); err != nil {
			return err
		}
	}
	if err = setCurrent(c.Update.InstallDir, rec.Active); err != nil {
		return err
	}
	if err = s.start(rec.Active, rec.ActiveDigest, ""); err != nil {
		return err
	}
	defer s.stop()
	if err = privateDir(c.Paths.RunDir); err != nil {
		return err
	}
	if info, e := os.Lstat(c.Update.LauncherSocket); e == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing non-socket launcher path")
		}
		if err = os.Remove(c.Update.LauncherSocket); err != nil {
			return err
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	listener, err := net.Listen("unix", c.Update.LauncherSocket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(c.Update.LauncherSocket)
	if err = os.Chmod(c.Update.LauncherSocket, 0600); err != nil {
		return err
	}
	server := &http.Server{Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 150 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.checkLoop() }()
	defer func() {
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		cancel()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
		_ = server.Close()
		s.wg.Wait()
	}()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-serverErr:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-tick.C:
			if !s.transition.TryLock() {
				continue
			}
			if !s.daemon.alive() || s.record().UIConfig != "" && !s.ui.alive() {
				if err = s.stop(); err == nil {
					r := s.record()
					err = s.start(r.Active, r.ActiveDigest, "")
				}
				if err != nil {
					s.setPhase("failed", err)
				}
			}
			s.transition.Unlock()
		}
	}
}

func (s *supervisor) record() record { s.mu.Lock(); defer s.mu.Unlock(); return s.rec }
func (s *supervisor) persist(r record) error {
	if err := saveRecord(s.c.Update.InstallDir, r); err != nil {
		return err
	}
	s.mu.Lock()
	s.rec = r
	s.mu.Unlock()
	return nil
}
func (s *supervisor) snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.CurrentVersion, st.PreviousVersion = s.rec.Active, s.rec.Previous
	if st.LastError == "" {
		st.LastError = s.rec.LastError
	}
	st.LastResult = s.rec.LastResult
	return st
}
func (s *supervisor) setPhase(phase string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Phase = phase
	if err != nil {
		s.status.LastError = redact.Text(err.Error())
	} else {
		s.status.LastError = ""
	}
}
func (s *supervisor) start(version, digest, nonce string) error {
	rel, err := update.VerifyRelease(s.c.Update.InstallDir, version, s.c.Update.PublicKey, s.c.Update.Channel)
	if err != nil {
		return err
	}
	if rel.ManifestSHA256 != digest {
		return errors.New("release identity changed since commit")
	}
	s.daemon, err = startChild(filepath.Join(rel.Directory, "kee-route-managerd"), s.configFile, nonce, s.output)
	if err != nil {
		return err
	}
	if uiConfig := s.record().UIConfig; uiConfig != "" {
		s.ui, err = startChild(filepath.Join(rel.Directory, "kee-route-manager-ui"), uiConfig, "", s.output)
		if err != nil {
			_ = s.daemon.stop(context.Background())
			return err
		}
	}
	return nil
}
func (s *supervisor) stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	u := s.ui.stop(ctx)
	d := s.daemon.stop(ctx)
	return errors.Join(u, d)
}
func (s *supervisor) check(ctx context.Context) (update.CheckResult, error) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	current := s.record().Active
	r, err := update.New(s.c.Update, s.c.Paths.StateDir, current).Check(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rec.Active != current {
		return update.CheckResult{}, errors.New("active version changed during discovery; check again")
	}
	s.status.CheckedAt = time.Now().UTC()
	if err != nil {
		s.status.LastError = redact.Text(err.Error())
		s.status.Check = nil
	} else {
		s.status.Check = &r
		s.status.LastError = ""
	}
	return r, err
}
func (s *supervisor) checkLoop() {
	if !s.c.Update.Enabled {
		return
	}
	interval := s.c.Update.CheckInterval.Duration
	if interval < time.Minute {
		interval = time.Minute
	}
	for {
		_, _ = s.check(s.ctx)
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *supervisor) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		reply(w, 200, s.snapshot())
	})
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		out, err := s.check(r.Context())
		if err != nil {
			reply(w, 502, map[string]string{"error": redact.Text(err.Error())})
			return
		}
		reply(w, 200, out)
	})
	mux.HandleFunc("/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		if !s.c.Update.Enabled {
			reply(w, 409, map[string]string{"error": "updates disabled"})
			return
		}
		var input *struct {
			Version string `json:"version"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		d.DisallowUnknownFields()
		if err := d.Decode(&input); err != nil || input == nil || d.Decode(new(any)) != io.EOF || input.Version != "" && (len(input.Version) > 128 || !versionPattern.MatchString(strings.TrimPrefix(input.Version, "v"))) {
			reply(w, 400, map[string]string{"error": "invalid update request"})
			return
		}
		s.mu.Lock()
		if s.closing || s.ctx.Err() != nil {
			s.mu.Unlock()
			reply(w, 503, map[string]string{"error": "launcher is stopping"})
			return
		}
		if s.status.Applying {
			s.mu.Unlock()
			reply(w, 409, map[string]string{"error": "update already running"})
			return
		}
		s.status.Applying = true
		s.status.Phase = "downloading"
		s.status.LastError = ""
		s.wg.Add(1)
		s.mu.Unlock()
		// Queue only. Waiting for preparation here deadlocks the daemon's mutation
		// admission lock while it proxies this request.
		go func() { defer s.wg.Done(); s.apply(input.Version) }()
		reply(w, 202, map[string]bool{"accepted": true})
	})
	return mux
}

func (s *supervisor) apply(expected string) {
	defer func() { s.mu.Lock(); s.status.Applying = false; s.mu.Unlock() }()
	s.transition.Lock()
	defer s.transition.Unlock()
	old := s.record()
	release, err := update.New(s.c.Update, s.c.Paths.StateDir, old.Active).Stage(s.ctx)
	if err != nil {
		s.setPhase("failed", err)
		return
	}
	if expected != "" && release.Version != strings.TrimPrefix(expected, "v") {
		s.setPhase("failed", errors.New("latest release changed; check and confirm the new version"))
		return
	}
	s.setPhase("preparing", nil)
	cl := client.New(s.c.API.UnixSocket)
	prepared, err := cl.Do(s.ctx, "POST", "/internal/update/prepare", struct{}{})
	cl.Close()
	if err == nil {
		var p struct {
			Prepared bool   `json:"prepared"`
			Version  string `json:"version"`
			PID      int    `json:"pid"`
		}
		err = json.Unmarshal(prepared, &p)
		if err == nil && (!p.Prepared || p.Version != old.Active || s.daemon == nil || p.PID != s.daemon.cmd.Process.Pid) {
			err = errors.New("prepared controller identity mismatch")
		}
	}
	if err != nil {
		s.rollback(old, fmt.Errorf("prepare update: %w", err))
		return
	}
	if err = s.stop(); err != nil {
		s.setPhase("failed", err)
		return
	}
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		s.rollback(old, err)
		return
	}
	trial := old
	trial.Phase = "trial"
	trial.Candidate = release.Version
	trial.CandidateDigest = release.ManifestSHA256
	trial.Nonce = hex.EncodeToString(nonce)
	if err = s.persist(trial); err != nil {
		s.rollback(old, err)
		return
	}
	s.setPhase("trial", nil)
	if err = s.start(trial.Candidate, trial.CandidateDigest, trial.Nonce); err == nil {
		err = s.waitTrial(trial)
	}
	if err != nil {
		s.rollback(old, err)
		return
	}
	committed := trial
	committed.Previous, committed.PreviousDigest = old.Active, old.ActiveDigest
	committed.Active, committed.ActiveDigest = trial.Candidate, trial.CandidateDigest
	committed.Candidate, committed.CandidateDigest, committed.Nonce, committed.Phase = "", "", "", "committed"
	committed.LastResult, committed.LastError = "updated", ""
	// Durable decision BEFORE granting write authority. A restart thereafter must
	// run this version: the candidate may already have written controller state.
	if err = s.persist(committed); err != nil {
		s.rollback(old, err)
		return
	}
	s.setPhase("activating", nil)
	linkErr := setCurrent(s.c.Update.InstallDir, committed.Active)
	cl = client.New(s.c.API.UnixSocket)
	_, err = cl.Do(s.ctx, "POST", "/internal/update/activate", map[string]string{"nonce": trial.Nonce})
	cl.Close()
	if err == nil {
		err = s.waitActive(committed.Active)
	}
	if err != nil {
		// No stale-state rollback after activation. Restart the committed version.
		if stopErr := s.stop(); stopErr == nil {
			_ = s.start(committed.Active, committed.ActiveDigest, "")
		}
		s.setPhase("failed", fmt.Errorf("update committed; activation needs restart: %w", err))
		return
	}
	s.mu.Lock()
	s.status.Check = nil
	s.mu.Unlock()
	if linkErr != nil {
		s.setPhase("failed", linkErr)
	} else {
		s.setPhase("idle", nil)
	}
}

func (s *supervisor) rollback(old record, cause error) {
	if err := s.stop(); err != nil {
		s.setPhase("failed", errors.Join(cause, err))
		return
	}
	old.Phase = "committed"
	old.Candidate, old.CandidateDigest, old.Nonce = "", "", ""
	old.LastResult, old.LastError = "rolled_back", redact.Text(cause.Error())
	if err := s.persist(old); err != nil {
		s.setPhase("failed", errors.Join(cause, err))
		return
	}
	if err := setCurrent(s.c.Update.InstallDir, old.Active); err != nil {
		s.setPhase("failed", errors.Join(cause, err))
		return
	}
	if s.ctx.Err() == nil {
		if err := s.start(old.Active, old.ActiveDigest, ""); err != nil {
			cause = errors.Join(cause, err)
		}
	}
	s.setPhase("failed", cause)
}
func (s *supervisor) waitTrial(r record) error {
	startup := time.Now().Add(30 * time.Second)
	var readyAt time.Time
	grace := s.c.Update.HealthGracePeriod.Duration
	if grace < time.Second {
		grace = time.Second
	}
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if !s.daemon.alive() || r.UIConfig != "" && !s.ui.alive() {
			return errors.New("candidate process exited")
		}
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		err := checkTrial(ctx, s.c, r.UIConfig, r.Candidate, r.Nonce, s.daemon, s.ui)
		cancel()
		if err == nil {
			if readyAt.IsZero() {
				readyAt = time.Now()
			}
			if time.Since(readyAt) >= grace {
				return nil
			}
		} else if !readyAt.IsZero() || time.Now().After(startup) {
			return fmt.Errorf("candidate readiness: %w", err)
		}
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
func (s *supervisor) waitActive(version string) error {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	cl := client.New(s.c.API.UnixSocket)
	defer cl.Close()
	for {
		uiConfig := s.record().UIConfig
		if !s.daemon.alive() || uiConfig != "" && !s.ui.alive() {
			return errors.New("activated controller or UI exited")
		}
		b, err := cl.Do(ctx, "GET", "/healthz", nil)
		valid := func(b []byte) bool {
			var h readiness
			return json.Unmarshal(b, &h) == nil && (h.Status == "ok" || h.Status == "safe_degraded") && h.Version == version
		}
		ready := err == nil && valid(b)
		if ready && s.c.API.Enabled {
			b, err = networkHealth(ctx, s.c.API.Listen, s.c.API.TLS)
			ready = err == nil && valid(b)
		}
		if ready && uiConfig != "" {
			uc, e := config.Load(uiConfig)
			if e != nil {
				return e
			}
			b, err = networkHealth(ctx, uc.Web.Listen, uc.Web.TLS)
			ready = err == nil && valid(b)
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
