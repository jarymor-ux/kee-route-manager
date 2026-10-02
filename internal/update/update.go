package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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
	Version         string    `json:"version"`
	Channel         string    `json:"channel"`
	PublishedAt     time.Time `json:"published_at"`
	MinConfigSchema int       `json:"min_config_schema"`
	Assets          []Asset   `json:"assets"`
}
type CheckResult struct {
	CurrentVersion string   `json:"current_version"`
	LatestVersion  string   `json:"latest_version"`
	Available      bool     `json:"available"`
	Manifest       Manifest `json:"manifest"`
	Asset          Asset    `json:"asset"`
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
}

func New(c config.Update, stateDir, current string) *Updater {
	return &Updater{c, stateDir, current, &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !secureURL(req.URL.String()) {
			return errors.New("unsafe update redirect")
		}
		return nil
	}}}
}
func (u *Updater) Enabled() bool { return u.cfg.Enabled }
func (u *Updater) Check(ctx context.Context) (CheckResult, error) {
	if !u.cfg.Enabled {
		return CheckResult{}, fmt.Errorf("updates disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	manifestURL, signatureURL, releaseTag := u.cfg.ManifestURL, u.cfg.SignatureURL, ""
	if u.cfg.GitHubRepository != "" {
		var err error
		manifestURL, signatureURL, releaseTag, err = u.githubManifestURLs(ctx)
		if err != nil {
			return CheckResult{}, err
		}
	}
	manifestBytes, e := u.download(ctx, manifestURL, 4<<20)
	if e != nil {
		return CheckResult{}, e
	}
	sig, e := u.download(ctx, signatureURL, 4096)
	if e != nil {
		return CheckResult{}, e
	}
	pub, e := decodeKey(u.cfg.PublicKey, ed25519.PublicKeySize)
	if e != nil {
		return CheckResult{}, fmt.Errorf("public key: %w", e)
	}
	signature, e := decodeSignature(sig)
	if e != nil {
		return CheckResult{}, e
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), manifestBytes, signature) {
		return CheckResult{}, fmt.Errorf("manifest signature invalid")
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestBytes))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&m); e != nil {
		return CheckResult{}, e
	}
	if e = dec.Decode(new(any)); e != io.EOF {
		return CheckResult{}, fmt.Errorf("unexpected trailing manifest data")
	}
	if m.SchemaVersion != 1 {
		return CheckResult{}, fmt.Errorf("unsupported manifest schema")
	}
	if releaseTag != "" && strings.TrimPrefix(m.Version, "v") != strings.TrimPrefix(releaseTag, "v") {
		return CheckResult{}, fmt.Errorf("signed manifest version does not match GitHub release tag")
	}
	if m.Channel != u.cfg.Channel {
		return CheckResult{}, fmt.Errorf("manifest channel %q does not match %q", m.Channel, u.cfg.Channel)
	}
	if m.MinConfigSchema > config.SchemaVersion {
		return CheckResult{}, fmt.Errorf("release requires config schema %d", m.MinConfigSchema)
	}
	a, e := selectAsset(m.Assets)
	if e != nil {
		return CheckResult{}, e
	}
	if _, e := parseSemVer(m.Version); e != nil {
		return CheckResult{}, fmt.Errorf("invalid release version: %w", e)
	}
	if _, e := parseSemVer(u.current); e != nil {
		return CheckResult{}, fmt.Errorf("invalid current version: %w", e)
	}
	if u.cfg.Channel == "stable" && strings.Contains(strings.SplitN(m.Version, "+", 2)[0], "-") {
		return CheckResult{}, fmt.Errorf("stable channel cannot contain prerelease")
	}
	if a.Size < 1 || a.Size > 200<<20 {
		return CheckResult{}, fmt.Errorf("invalid asset size")
	}
	if len(a.SHA256) != 64 {
		return CheckResult{}, fmt.Errorf("invalid asset checksum")
	}
	if _, e := hex.DecodeString(a.SHA256); e != nil {
		return CheckResult{}, fmt.Errorf("invalid asset checksum")
	}
	if !secureURL(a.URL) {
		return CheckResult{}, fmt.Errorf("asset URL must use HTTPS")
	}
	cmp := compareVersions(m.Version, u.current)
	available := cmp > 0 || u.cfg.AllowDowngrade && cmp != 0
	return CheckResult{u.current, m.Version, available, m, a}, nil
}

// ErrApplyDisabled documents the RC2 safety boundary: installation must be done
// by the verified installer until a real A/B launcher with readiness exists.
var ErrApplyDisabled = errors.New("update.apply disabled in RC2: verified A/B launcher is not implemented; use the versioned installer")

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
func selectAsset(xs []Asset) (Asset, error) {
	goarm := os.Getenv("GOARM")
	for _, a := range xs {
		if (a.Component == "" || a.Component == "daemon") && a.OS == runtime.GOOS && a.Arch == runtime.GOARCH && (a.GOARM == "" || goarm == "" || a.GOARM == goarm) {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("no asset for %s/%s", runtime.GOOS, runtime.GOARCH)
}
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
