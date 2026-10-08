package launcher

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/control/client"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
)

func channelSupervisor(t *testing.T) *supervisor {
	t.Helper()
	c := config.Default()
	c.Update.Enabled = true
	c.Update.GitHubRepository = "owner/repo"
	c.Update.InstallDir = t.TempDir()
	return &supervisor{c: c, ctx: context.Background(), rec: record{Active: "1.1.0-rc.13"}, status: Status{Enabled: true, Check: &update.CheckResult{Channel: "rc", LatestVersion: "1.1.0-rc.14"}, CheckedAt: time.Now()}}
}

func requestChannel(s *supervisor, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, httptest.NewRequest("POST", "/channel", strings.NewReader(body)))
	return w
}

func TestChannelPreferencePersistsWithoutActivationAndPreservesCommitRecord(t *testing.T) {
	s := channelSupervisor(t)
	before := s.rec
	for _, channel := range []string{"stable", "rc"} {
		w := requestChannel(s, `{"channel":"`+channel+`"}`)
		if w.Code != 200 {
			t.Fatalf("switch: %d %s", w.Code, w.Body)
		}
		st := s.snapshot()
		if st.Channel != channel || !st.ChannelSwitchSupported || st.Check != nil || !st.CheckedAt.IsZero() || st.Applying || s.rec != before {
			t.Fatalf("switch activated/stale: %+v", st)
		}
		loaded, err := loadChannel(s.c.Update.InstallDir, "rc")
		if err != nil || loaded != channel {
			t.Fatalf("restart lost choice: %s %v", loaded, err)
		}
		if info, err := os.Stat(filepath.Join(s.c.Update.InstallDir, "update-channel.json")); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("preference not private: %v", err)
		}
	}
	if s.c.Update.Channel != "rc" {
		t.Fatal("startup config mutated")
	}
}

func TestChannelRejectsInvalidBusyDisabledCustomAndShutdownRequests(t *testing.T) {
	for _, kind := range []string{"invalid", "unknown", "null", "trailing", "applying", "disabled", "custom", "shutdown", "storage"} {
		t.Run(kind, func(t *testing.T) {
			s := channelSupervisor(t)
			body, want := `{"channel":"stable"}`, 400
			switch kind {
			case "invalid":
				body = `{"channel":"release"}`
			case "unknown":
				body = `{"channel":"stable","url":"https://untrusted.invalid"}`
			case "null":
				body = `null`
			case "trailing":
				body += `{}`
			case "applying":
				s.status.Applying = true
				want = 409
			case "disabled":
				s.c.Update.Enabled = false
				want = 409
			case "custom":
				s.c.Update.GitHubRepository = ""
				want = 409
			case "shutdown":
				s.closing = true
				want = 503
			case "storage":
				s.c.Update.InstallDir = filepath.Join(s.c.Update.InstallDir, "missing")
				want = 500
			}
			if w := requestChannel(s, body); w.Code != want {
				t.Fatalf("got %d %s", w.Code, w.Body)
			}
			if s.snapshot().Channel != "rc" || s.status.Check == nil || s.channelRevision != 0 {
				t.Fatal("rejected change became live")
			}
		})
	}
}

func TestUntrustedChannelPreferenceFailsClosedAndAbsentIsReadOnly(t *testing.T) {
	root := t.TempDir()
	if channel, err := loadChannel(root, "stable"); err != nil || channel != "stable" {
		t.Fatalf("fallback: %s %v", channel, err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatal("read-only startup wrote preference")
	}
	path := filepath.Join(root, "update-channel.json")
	for _, data := range []string{`{"schema":2,"channel":"rc"}`, `{"schema":1,"channel":"release"}`, `{"schema":1,"channel":"rc","url":"x"}`, `{"schema":1,"channel":"rc"}{}`, `null`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadChannel(root, "rc"); err == nil {
			t.Fatalf("invalid preference accepted: %s", data)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"channel":"stable"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadChannel(root, "rc"); err == nil {
		t.Fatal("public preference accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadChannel(root, "rc"); err == nil {
		t.Fatal("symlink preference accepted")
	}
}

func TestChannelSwitchInvalidatesSlowDiscoveryEvenAfterSwitchBack(t *testing.T) {
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	// Discovery stays on the signed fixture URLs until the request is admitted;
	// this test exercises generation invalidation independently of transport.
	entered, release := make(chan struct{}), make(chan struct{})
	f.beforeManifest = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() { _, err := s.check(context.Background()); done <- err }()
	<-entered
	s.c.Update.GitHubRepository = "owner/repo"
	for _, channel := range []string{"stable", "rc"} {
		if w := requestChannel(s, `{"channel":"`+channel+`"}`); w.Code != 200 {
			t.Fatalf("switch: %d", w.Code)
		}
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("stale discovery accepted after channel changes")
	}
	if st := s.snapshot(); st.Check != nil || !st.CheckedAt.IsZero() {
		t.Fatalf("stale check published: %+v", st)
	}
}

func TestApplyRejectsStaleChannelBeforeDownloading(t *testing.T) {
	s := channelSupervisor(t)
	s.channel = "stable"
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, httptest.NewRequest("POST", "/apply", strings.NewReader(`{"version":"1.1.0","channel":"rc"}`)))
	if w.Code != 409 || s.snapshot().Applying {
		t.Fatalf("stale apply admitted: %d %s", w.Code, w.Body)
	}
}

func TestStableUpdateCanStartAndRollbackPreviouslySignedRCSlot(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			f := newReleaseFixture(t)
			var m update.Manifest
			if err := json.Unmarshal(f.data, &m); err != nil {
				t.Fatal(err)
			}
			m.Channel = "stable"
			f.data, _ = json.Marshal(m)
			f.signature = ed25519.Sign(f.private, f.data)
			f.c.Update.Channel = "stable"
			if fail {
				t.Setenv("KRM_LAUNCHER_FAIL_VERSION", "1.1.0")
			}
			s := f.supervisor(t)
			s.apply("1.1.0")
			want := "1.1.0"
			if fail {
				want = "1.0.0"
			}
			if st := s.snapshot(); st.CurrentVersion != want || !s.daemon.alive() {
				t.Fatalf("cross-channel lifecycle: %+v", st)
			}
			if err := s.stop(); err != nil {
				t.Fatal(err)
			}
			r := s.record()
			if err := s.start(r.Active, r.ActiveDigest, ""); err != nil {
				t.Fatalf("cross-channel restart: %v", err)
			}
			waitDaemonVersion(t, f.c, want)
		})
	}
}

func TestLauncherRestartLoadsChannelPreferenceWithoutInvalidatingRCSlot(t *testing.T) {
	f := newReleaseFixture(t)
	f.c.Update.GitHubRepository = "owner/repo"
	// No network discovery is needed to prove restart ownership and signed-slot
	// identity. The saved discovery choice must still be reflected when disabled.
	f.c.Update.Enabled = false
	writeFixtureConfig(t, f.configFile, f.c)
	if err := saveRecord(f.c.Update.InstallDir, record{Schema: 1, Active: f.old.Version, ActiveDigest: f.old.ManifestSHA256, Phase: "committed"}); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(f.c.Update.InstallDir, "update-channel.json"), channelPreference{Schema: 1, Channel: "stable"}); err != nil {
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
			t.Error("launcher did not stop")
		}
	})
	waitDaemonVersion(t, f.c, f.old.Version)
	cl := client.New(f.c.Update.LauncherSocket)
	defer cl.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := cl.Do(context.Background(), "GET", "/status", nil)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var status Status
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatal(err)
		}
		if status.Channel != "stable" || status.CurrentVersion != f.old.Version || status.Enabled || status.ChannelSwitchSupported {
			t.Fatalf("restart preference: %+v", status)
		}
		return
	}
	t.Fatal("launcher status unavailable after restart")
}
