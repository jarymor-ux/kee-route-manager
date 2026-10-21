package launcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemovedPlatformApplyRefusesBeforeDownloadOrPrepare(t *testing.T) {
	for _, entry := range []string{"http", "direct"} {
		t.Run(entry, func(t *testing.T) {
			f := newReleaseFixture(t)
			s := f.supervisor(t)
			pid := s.daemon.cmd.Process.Pid
			// Only the launcher's copy changes. Its running fixture daemon remains in
			// Keenetic mode, so a rejected request cannot transition the running owner.
			s.c.Platform.Kind = "openwrt"
			if entry == "http" {
				w := httptest.NewRecorder()
				s.handler().ServeHTTP(w, httptest.NewRequest("POST", "/apply", strings.NewReader(`{"version":"1.1.0"}`)))
				s.wg.Wait()
				if w.Code != http.StatusConflict {
					t.Fatalf("removed-platform apply accepted: %d %s", w.Code, w.Body.String())
				}
			} else {
				s.apply("1.1.0")
				if !strings.Contains(s.snapshot().LastError, "Keenetic/Netcraze") {
					t.Fatalf("missing platform refusal: %+v", s.snapshot())
				}
			}
			if f.manifestRequests.Load() != 0 || f.assetRequests.Load() != 0 || s.daemon.cmd.Process.Pid != pid || !s.daemon.alive() || s.record().Active != "1.0.0" {
				t.Fatal("platform rejection performed discovery, download or owner transition")
			}
		})
	}
}
