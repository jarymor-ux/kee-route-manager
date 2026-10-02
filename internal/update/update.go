package update

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type Asset struct {
	Name      string `json:"name,omitempty"`
	Component string `json:"component,omitempty"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GOARM     string `json:"goarm,omitempty"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
}
type Manifest struct {
	SchemaVersion   int       `json:"schema_version"`
	UpdateProtocol  int       `json:"update_protocol,omitempty"`
	Version         string    `json:"version"`
	Channel         string    `json:"channel"`
	PublishedAt     time.Time `json:"published_at"`
	MinConfigSchema int       `json:"min_config_schema"`
	Assets          []Asset   `json:"assets"`
}
type CheckResult struct {
	CurrentVersion string           `json:"current_version"`
	LatestVersion  string           `json:"latest_version"`
	Available      bool             `json:"available"`
	Manifest       Manifest         `json:"manifest"`
	Asset          Asset            `json:"asset"`
	Assets         map[string]Asset `json:"assets,omitempty"`
	StageSupported bool             `json:"stage_supported"`
}
type Pending struct {
	SchemaVersion  int       `json:"schema_version"`
	FromVersion    string    `json:"from_version"`
	ToVersion      string    `json:"to_version"`
	BackupPath     string    `json:"backup_path"`
	ExecutablePath string    `json:"executable_path"`
	InstalledAt    time.Time `json:"installed_at"`
	Attempts       int       `json:"attempts"`
}
type Updater struct {
	cfg               config.Update
	stateDir, current string
	client            *http.Client
	applySupportErr   error
}

func New(c config.Update, stateDir, current string) *Updater {
	return &Updater{cfg: c, stateDir: stateDir, current: current, client: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !secureURL(req.URL.String()) {
			return errors.New("unsafe update redirect")
		}
		return nil
	}}}
}

// NewForConfig binds update eligibility to the controller platform. Production
// discovery/staging must use this constructor; New remains a signed-bundle client.
func NewForConfig(c config.Config, current string) *Updater {
	u := New(c.Update, c.Paths.StateDir, current)
	u.applySupportErr = c.UpdateApplySupport()
	return u
}
func (u *Updater) Enabled() bool { return u.cfg.Enabled }
func (u *Updater) Check(ctx context.Context) (CheckResult, error) {
	checked, err := u.checkSigned(ctx)
	return checked.result, err
}

type checkedRelease struct {
	result              CheckResult
	manifest, signature []byte
}

func (u *Updater) checkSigned(ctx context.Context) (checkedRelease, error) {
	if !u.cfg.Enabled {
		return checkedRelease{}, fmt.Errorf("updates disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	manifestURL, signatureURL, releaseTag := u.cfg.ManifestURL, u.cfg.SignatureURL, ""
	if u.cfg.GitHubRepository != "" {
		var err error
		manifestURL, signatureURL, releaseTag, err = u.githubManifestURLs(ctx)
		if err != nil {
			return checkedRelease{}, err
		}
	}
	manifestBytes, e := u.download(ctx, manifestURL, 4<<20)
	if e != nil {
		return checkedRelease{}, e
	}
	sig, e := u.download(ctx, signatureURL, 4096)
	if e != nil {
		return checkedRelease{}, e
	}
	m, e := authenticateManifest(manifestBytes, sig, u.cfg.PublicKey, u.cfg.Channel)
	if e != nil {
		return checkedRelease{}, e
	}
	if releaseTag != "" && strings.TrimPrefix(m.Version, "v") != strings.TrimPrefix(releaseTag, "v") {
		return checkedRelease{}, fmt.Errorf("signed manifest version does not match GitHub release tag")
	}
	a, e := selectAsset(m.Assets)
	if e != nil {
		return checkedRelease{}, e
	}
	if _, e := parseSemVer(u.current); e != nil {
		return checkedRelease{}, fmt.Errorf("invalid current version: %w", e)
	}
	if e = validateAsset(a, false); e != nil {
		return checkedRelease{}, e
	}
	var assets map[string]Asset
	if m.UpdateProtocol == 1 {
		assets, e = bundleAssets(m)
		if e != nil {
			return checkedRelease{}, e
		}
		if releaseTag != "" {
			for _, asset := range assets {
				want := "https://github.com/" + u.cfg.GitHubRepository + "/releases/download/" + releaseTag + "/" + asset.Name
				if asset.URL != want {
					return checkedRelease{}, fmt.Errorf("asset is not bound to selected GitHub release")
				}
			}
		}
	}
	cmp := compareVersions(m.Version, u.current)
	available := cmp > 0 || u.cfg.AllowDowngrade && cmp != 0
	result := CheckResult{CurrentVersion: u.current, LatestVersion: m.Version, Available: available, Manifest: m, Asset: a, Assets: assets, StageSupported: m.UpdateProtocol == 1 && u.applySupportErr == nil}
	return checkedRelease{result: result, manifest: manifestBytes, signature: sig}, nil
}

// In-process apply remains forbidden: only the separate launcher may activate
// a verified staged release and supervise readiness/rollback.
var ErrApplyDisabled = errors.New("in-process update apply is disabled; use the separately installed signed-release launcher")

func (u *Updater) Apply(ctx context.Context, r CheckResult) (Pending, error) {
	return Pending{}, ErrApplyDisabled
}
func (u *Updater) PrepareStartup() (bool, error) {
	if _, err := os.Stat(u.pendingPath()); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	// RC1 journals contain executable paths. Never trust them to copy files as root.
	return false, errors.New("legacy pending update found; recover manually using the verified installer")
}
func (u *Updater) MarkHealthy() error { return nil }
func (u *Updater) download(ctx context.Context, raw string, max int64) ([]byte, error) {
	if !secureURL(raw) {
		return nil, errors.New("update URL must use HTTPS without credentials")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "Kee-Route-Manager")
	if req.URL.Host == "api.github.com" {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	}
	resp, e := u.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("response too large")
	}
	return b, nil
}
func (u *Updater) pendingPath() string { return filepath.Join(u.stateDir, "update-pending.json") }
func decodeKey(s string, n int) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, e := enc.DecodeString(s); e == nil && len(b) == n {
			return b, nil
		}
	}
	return nil, fmt.Errorf("expected %d-byte base64 value", n)
}
func decodeSignature(b []byte) ([]byte, error) {
	if len(b) == ed25519.SignatureSize {
		return b, nil
	}
	return decodeKey(string(b), ed25519.SignatureSize)
}

func secureURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
