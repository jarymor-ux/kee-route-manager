package components

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestAWGLatestUsesVersionedUpstreamGitTagsWithoutReleases(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: fakeRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.github.com" ||
			r.URL.Path != "/repos/amnezia-vpn/amneziawg-go/git/refs/tags" {
			t.Errorf("invalid AWG metadata endpoint: %s", r.URL)
			return nil, errors.New("unexpected metadata endpoint")
		}
		// Git refs are lexical, not release chronological order; select the greatest numeric tag.
		refs := "[{\"ref\":\"refs/tags/v3.1.20260812\"},{\"ref\":\"refs/tags/v2.99.20261201\"}," +
			"{\"ref\":\"refs/tags/v3.1.20260828\"},{\"ref\":\"refs/tags/v3.0.20260831\"}," +
			"{\"ref\":\"refs/heads/malicious\"},{\"ref\":\"refs/tags/v3.2.0-rc1\"}," +
			"{\"ref\":\"refs/tags/v3.1.20260828-shell\"}]"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(refs)), Header: http.Header{}}, nil
	})}
	cfg := config.Default()
	cfg.Platform.Kind = "linux-systemd"
	backend := NewSystemBackend(cfg, client)
	got, err := backend.Latest(context.Background(), AmneziaWG)
	if err != nil || got != "3.1.20260828" || calls != 1 {
		t.Fatalf("AWG latest tag=%q, calls=%d, err=%v", got, calls, err)
	}
}

func TestAWGLatestFailsClosedWithNoStableTag(t *testing.T) {
	client := &http.Client{Transport: fakeRoundTrip(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK,
			Body:   io.NopCloser(strings.NewReader("[{\"ref\":\"refs/tags/foo\"},{\"ref\":\"refs/tags/v3.1.0-rc1\"}]")),
			Header: http.Header{}}, nil
	})}
	cfg := config.Default()
	backend := NewSystemBackend(cfg, client)
	if got, err := backend.Latest(context.Background(), AmneziaWG); err == nil || got != "" {
		t.Fatalf("unsupported AWG release data accepted: %q / %v", got, err)
	}
}

func TestComponentDiscoveryDoesNotReportUnknownInstalledVersionAsUpgrade(t *testing.T) {
	backend := &fakeBackend{
		installed: map[ID]string{AmneziaWG: "present"},
		latest: map[ID]string{AmneziaWG: "3.1.20260828", Xray: "26.9.9"},
	}
	m, err := NewManager("linux-systemd", 7 * 24 * time.Hour, backend, func() time.Time {
		return time.Date(2026, 10, 12, 1, 2, 3, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Check(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if findItem(t, snapshot, AmneziaWG).UpdateAvailable {
		t.Fatal("unknown installed version must not imply an update exists")
	}
}
