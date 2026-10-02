package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedFirewallDiscoveryRemainsAvailableWithoutApplySupport(t *testing.T) {
	f := newReleaseFixture(t)
	s := f.supervisor(t)
	s.c.Platform.Linux.FirewallMode = "managed"
	result, err := s.check(context.Background())
	if err != nil || !result.Available || result.LatestVersion != "1.1.0" || result.StageSupported {
		t.Fatalf("managed update eligibility incorrect: %+v %v", result, err)
	}
	if f.assetRequests.Load() != 0 {
		t.Fatal("discovery staged managed update")
	}
}

func TestManagedFirewallApplyRefusesBeforeDownloadOrPrepare(t *testing.T) {
	for _, entry := range []string{"http", "direct"} {
		t.Run(entry, func(t *testing.T) {
			f := newReleaseFixture(t)
			s := f.supervisor(t)
			pid := s.daemon.cmd.Process.Pid
			// Only the launcher's copy changes. Its running fixture daemon remains in
			// existing mode, so even a regression cannot run host firewall effects.
			s.c.Platform.Kind = "openwrt"
			s.c.Platform.OpenWrt.FirewallMode = "managed"
			if entry == "http" {
				w := httptest.NewRecorder()
				s.handler().ServeHTTP(w, httptest.NewRequest("POST", "/apply", strings.NewReader(`{"version":"1.1.0"}`)))
				s.wg.Wait()
				if w.Code != http.StatusConflict {
					t.Fatalf("managed apply accepted: %d %s", w.Code, w.Body.String())
				}
			} else {
				s.apply("1.1.0")
				if !strings.Contains(s.snapshot().LastError, "managed firewall") {
					t.Fatalf("missing managed refusal: %+v", s.snapshot())
				}
			}
			if f.manifestRequests.Load() != 0 || f.assetRequests.Load() != 0 || s.daemon.cmd.Process.Pid != pid || !s.daemon.alive() || s.record().Active != "1.0.0" {
				t.Fatal("managed rejection performed discovery, download or owner transition")
			}
		})
	}
}

func TestManagedFirewallBootstrapRemainsSupported(t *testing.T) {
	f := newReleaseFixture(t)
	f.c.Platform.Linux.FirewallMode = "managed"
	writeFixtureConfig(t, f.configFile, f.c)
	if err := Install(f.configFile, "", f.seed, f.bin); err != nil {
		t.Fatalf("managed launcher bootstrap refused: %v", err)
	}
	if r, err := loadRecord(f.c.Update.InstallDir); err != nil || r.Active != "1.0.0" || r.LastResult != "installed" {
		t.Fatalf("managed bootstrap not recorded: %+v %v", r, err)
	}
	if target, err := os.Readlink(filepath.Join(f.bin, "kee-route-managerd")); err != nil || target != filepath.Join(f.c.Update.InstallDir, "current", "kee-route-managerd") {
		t.Fatalf("managed controller entrypoint missing: %q %v", target, err)
	}
}
