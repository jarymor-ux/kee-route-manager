package launcher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
)

func TestSignedApplyCommitsRealControllerAndRestartsCommittedVersion(t *testing.T) {
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	oldPID := s.daemon.cmd.Process.Pid
	s.apply("1.1.0")
	if status := s.snapshot(); status.Phase != "idle" || status.CurrentVersion != "1.1.0" || status.PreviousVersion != "1.0.0" || status.LastResult != "updated" {
		t.Fatalf("update failed: %+v", status)
	}
	r, err := loadRecord(f.c.Update.InstallDir)
	if err != nil || r.Phase != "committed" || r.Active != "1.1.0" || r.Candidate != "" || r.Nonce != "" {
		t.Fatalf("commit not durable: %+v %v", r, err)
	}
	if s.daemon.cmd.Process.Pid == oldPID {
		t.Fatal("old process remained active")
	}
	if l, e := daemonlock.Acquire(filepath.Join(f.c.Paths.StateDir, "daemon.lock")); e == nil {
		l.Close()
		t.Fatal("activated daemon does not own state")
	}
	waitDaemonVersion(t, f.c, "1.1.0")
	if target, e := os.Readlink(filepath.Join(f.c.Update.InstallDir, "current")); e != nil || target != filepath.Join("releases", "1.1.0") {
		t.Fatalf("current link: %q %v", target, e)
	}
	if err = s.stop(); err != nil {
		t.Fatal(err)
	}
	if err = s.start(r.Active, r.ActiveDigest, ""); err != nil {
		t.Fatal(err)
	}
	waitDaemonVersion(t, f.c, "1.1.0")
	if f.assetRequests.Load() != 3 {
		t.Fatalf("incomplete signed bundle transfer: %d", f.assetRequests.Load())
	}
}

func TestSignedApplyFailedCandidateRollsBackWithoutRestoringControllerFiles(t *testing.T) {
	f := newReleaseFixture(t)
	t.Setenv("KRM_LAUNCHER_FAIL_VERSION", "1.1.0")
	s := f.supervisor(t)
	oldPID := s.daemon.cmd.Process.Pid
	// This file is intentionally not part of any launcher backup. A rollback
	// that replaces controller state would silently erase it.
	marker := filepath.Join(f.c.Paths.StateDir, "operator-note")
	if err := os.WriteFile(marker, []byte("keep current operator data"), 0600); err != nil {
		t.Fatal(err)
	}
	s.apply("1.1.0")
	r, err := loadRecord(f.c.Update.InstallDir)
	if err != nil || r.Active != "1.0.0" || r.Phase != "committed" || r.LastResult != "rolled_back" || r.Candidate != "" {
		t.Fatalf("rollback record: %+v %v", r, err)
	}
	if status := s.snapshot(); status.Phase != "failed" || !strings.Contains(status.LastError, "candidate process exited") {
		t.Fatalf("failure missing: %+v", status)
	}
	waitDaemonVersion(t, f.c, "1.0.0")
	if s.daemon.cmd.Process.Pid == oldPID {
		t.Fatal("prepared old instance was reused instead of restarted")
	}
	b, err := os.ReadFile(marker)
	if err != nil || string(b) != "keep current operator data" {
		t.Fatalf("controller data replaced on rollback: %q %v", b, err)
	}
	if target, _ := os.Readlink(filepath.Join(f.c.Update.InstallDir, "current")); target != filepath.Join("releases", "1.0.0") {
		t.Fatal("current link did not roll back")
	}
}

func TestCheckLoopAndManualCheckNeverDownloadOrStopOwner(t *testing.T) {
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	pid := s.daemon.cmd.Process.Pid
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	done := make(chan struct{})
	go func() { s.checkLoop(); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for s.snapshot().CheckedAt.IsZero() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	status := s.snapshot()
	if status.Check == nil || !status.Check.Available || status.Check.LatestVersion != "1.1.0" {
		t.Fatalf("automatic check failed: %+v", status)
	}
	for _, method := range []string{"GET", "POST"} {
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, httptest.NewRequest(method, "/check", nil))
		want := http.StatusOK
		if method == "POST" {
			want = http.StatusMethodNotAllowed
		}
		if w.Code != want {
			t.Fatalf("check %s: %d %s", method, w.Code, w.Body.String())
		}
	}
	if f.assetRequests.Load() != 0 || s.daemon.cmd.Process.Pid != pid || !s.daemon.alive() || s.record().Active != "1.0.0" {
		t.Fatal("check activated or downloaded an update")
	}
	if _, err := os.Stat(filepath.Join(f.c.Update.InstallDir, "releases", "1.1.0")); !os.IsNotExist(err) {
		t.Fatal("check staged candidate")
	}
}

func TestManualApplyAcknowledgesBeforePreparingAndRejectsConcurrentRequest(t *testing.T) {
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	// Holding the transition lock models the daemon's mutation admission while
	// it proxies apply. The HTTP response must not await preparation.
	s.transition.Lock()
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, httptest.NewRequest("POST", "/apply", strings.NewReader(`{"version":"1.1.0"}`)))
	if w.Code != http.StatusAccepted {
		s.transition.Unlock()
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	second := httptest.NewRecorder()
	s.handler().ServeHTTP(second, httptest.NewRequest("POST", "/apply", strings.NewReader(`{}`)))
	if second.Code != http.StatusConflict {
		s.transition.Unlock()
		t.Fatalf("concurrent apply: %d", second.Code)
	}
	s.transition.Unlock()
	s.wg.Wait()
	if s.snapshot().CurrentVersion != "1.1.0" || s.snapshot().Applying {
		t.Fatalf("queued update did not complete: %+v", s.snapshot())
	}
}

func TestApplyRefusesChangedConfirmationAndTamperedStageWithoutStoppingOwner(t *testing.T) {
	for _, mode := range []string{"changed-version", "tampered-stage"} {
		t.Run(mode, func(t *testing.T) {
			f := newReleaseFixture(t)
			s := f.supervisor(t)
			pid := s.daemon.cmd.Process.Pid
			want := "1.2.0"
			if mode == "tampered-stage" {
				want = "1.1.0"
				release, err := update.New(f.c.Update, "1.0.0").Stage(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(release.Directory, "kee-route-managerd"), []byte("tampered"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			s.apply(want)
			if s.snapshot().Phase != "failed" || s.record().Active != "1.0.0" || s.daemon.cmd.Process.Pid != pid || !s.daemon.alive() {
				t.Fatalf("unconfirmed/untrusted release disturbed owner: %+v", s.snapshot())
			}
			cl := client.New(f.c.API.UnixSocket)
			defer cl.Close()
			b, err := cl.Do(context.Background(), "GET", "/healthz", nil)
			var h readiness
			if err != nil || json.Unmarshal(b, &h) != nil || h.Version != "1.0.0" {
				t.Fatalf("old controller unavailable: %s %v", b, err)
			}
		})
	}
}

func TestSlowCheckCannotPublishAvailabilityForPreviousVersionAfterCommit(t *testing.T) {
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	f.beforeManifest = func() {
		if held.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	done := make(chan error, 1)
	go func() { _, err := s.check(context.Background()); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("check did not reach discovery")
	}
	s.apply("1.1.0")
	if s.record().Active != "1.1.0" {
		t.Fatalf("update failed while check pending: %+v", s.snapshot())
	}
	unblock()
	if err := <-done; err == nil {
		t.Fatal("stale check succeeded after current version changed")
	}
	if status := s.snapshot(); status.Check != nil || status.LastError != "" || status.Phase != "idle" {
		t.Fatalf("stale discovery overwrote committed status: %+v", status)
	}
}

type blockedApplyBody struct {
	entered, release chan struct{}
	once             sync.Once
	reader           io.Reader
}

func (b *blockedApplyBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.release })
	return b.reader.Read(p)
}
func (b *blockedApplyBody) Close() error { return nil }

func TestApplyFinishingBodyAfterShutdownCannotScheduleWork(t *testing.T) {
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	body := &blockedApplyBody{entered: make(chan struct{}), release: make(chan struct{}), reader: strings.NewReader(`{"version":"1.1.0"}`)}
	req := httptest.NewRequest("POST", "/apply", body)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.handler().ServeHTTP(w, req); close(done) }()
	<-body.entered
	// A request admitted by net/http before Shutdown may still be parsing its
	// body when the supervisor starts draining its transition work.
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.wg.Wait()
	close(body.release)
	<-done
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("late apply accepted during shutdown: %d %s", w.Code, w.Body.String())
	}
	if s.snapshot().Applying || f.assetRequests.Load() != 0 {
		t.Fatal("shutdown admitted a new background transition")
	}
}

func TestRestartRecoversTrialButHonorsCommittedDecision(t *testing.T) {
	for _, phase := range []string{"trial", "committed"} {
		t.Run(phase, func(t *testing.T) {
			f := newReleaseFixture(t)
			newRelease, err := update.New(f.c.Update, "1.0.0").Stage(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			r := record{Schema: 1, Active: f.old.Version, ActiveDigest: f.old.ManifestSHA256, Phase: "trial", Candidate: newRelease.Version, CandidateDigest: newRelease.ManifestSHA256, Nonce: strings.Repeat("a", 64)}
			want := "1.0.0"
			if phase == "committed" {
				want = "1.1.0"
				r = record{Schema: 1, Active: newRelease.Version, ActiveDigest: newRelease.ManifestSHA256, Previous: f.old.Version, PreviousDigest: f.old.ManifestSHA256, Phase: "committed", LastResult: "updated"}
			}
			if err = saveRecord(f.c.Update.InstallDir, r); err != nil {
				t.Fatal(err)
			}
			// Stale convenience pointer models a crash between record fsync and link.
			if err = setCurrent(f.c.Update.InstallDir, "1.0.0"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- Serve(ctx, f.configFile) }()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(10 * time.Second):
					t.Error("launcher failed to stop")
				}
			})
			waitDaemonVersion(t, f.c, want)
			current, err := loadRecord(f.c.Update.InstallDir)
			if err != nil || current.Active != want || current.Phase != "committed" || current.Candidate != "" {
				t.Fatalf("crash recovery: %+v %v", current, err)
			}
			if phase == "trial" && current.LastResult != "rolled_back" {
				t.Fatal("interrupted trial wasn't recorded")
			}
			if target, _ := os.Readlink(filepath.Join(f.c.Update.InstallDir, "current")); target != filepath.Join("releases", want) {
				t.Fatal("stale convenience pointer won over durable decision")
			}
		})
	}
}
