package launcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/web"
)

func fixtureUI(t *testing.T, f *releaseFixture) string {
	t.Helper()
	c := f.c
	c.Instance.Role = "ui"
	c.Web.Enabled = true
	c.Web.Listen = unusedAddress(t)
	c.Paths.LogFile = filepath.Join(f.root, "ui.log")
	c.Web.TLS.CertFile, c.Web.TLS.KeyFile = filepath.Join(f.root, "ui.crt"), filepath.Join(f.root, "ui.key")
	c.UIProxy.Upstream, c.UIProxy.UpstreamCAFile = "https://"+f.c.API.Listen, f.c.API.TLS.CertFile
	if err := web.EnsureTLS(c.Web.TLS, c.Web.Listen); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "ui.yaml")
	writeFixtureConfig(t, path, c)
	if _, err := config.Load(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTrialReadinessRequiresRealCoreAndUIWithMatchingNonceVersionPIDAndTrust(t *testing.T) {
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	if err := s.stop(); err != nil {
		t.Fatal(err)
	}
	uiConfig := fixtureUI(t, f)
	s.mu.Lock()
	s.rec.UIConfig = uiConfig
	s.mu.Unlock()
	nonce := strings.Repeat("b", 64)
	if err := s.start("1.0.0", f.old.ManifestSHA256, nonce); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := checkTrial(context.Background(), f.c, uiConfig, "1.0.0", nonce, s.daemon, s.ui)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, tc := range []struct {
		name, version, nonce string
		pid                  int
	}{
		{"wrong-version", "1.1.0", nonce, s.daemon.cmd.Process.Pid},
		{"wrong-nonce", "1.0.0", strings.Repeat("c", 64), s.daemon.cmd.Process.Pid},
		{"wrong-pid", "1.0.0", nonce, s.daemon.cmd.Process.Pid + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity := &child{cmd: &exec.Cmd{Process: &os.Process{Pid: tc.pid}}, done: s.daemon.done}
			if err := checkTrial(context.Background(), f.c, uiConfig, tc.version, tc.nonce, identity, s.ui); err == nil {
				t.Fatal("readiness accepted another process/release/attempt")
			}
		})
	}
	badTrust := f.c
	badTrust.API.TLS.CertFile = filepath.Join(f.root, "ui.crt")
	if err := checkTrial(context.Background(), badTrust, uiConfig, "1.0.0", nonce, s.daemon, s.ui); err == nil {
		t.Fatal("trial accepted untrusted core TLS")
	}
	if err := s.ui.stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := checkTrial(context.Background(), f.c, uiConfig, "1.0.0", nonce, s.daemon, s.ui); err == nil {
		t.Fatal("trial accepted dead UI")
	}
}

func TestActiveReadinessRejectsDeadUIAndUnavailableNetworkAPI(t *testing.T) {
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	if err := s.waitActive("1.0.0"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.rec.UIConfig = fixtureUI(t, f)
	s.mu.Unlock()
	if err := s.waitActive("1.0.0"); err == nil {
		t.Fatal("active readiness accepted a missing UI process")
	}
	s.mu.Lock()
	s.rec.UIConfig = ""
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	s.ctx = ctx
	s.c.API.Listen = unusedAddress(t)
	if err := s.waitActive("1.0.0"); err == nil {
		t.Fatal("active readiness accepted dead HTTPS API while Unix API worked")
	}
}
