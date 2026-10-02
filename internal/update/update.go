package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type Asset struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	GOARM  string `json:"goarm,omitempty"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
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
	return &Updater{c, stateDir, current, &http.Client{Timeout: 2 * time.Minute}}
}
func (u *Updater) Enabled() bool { return u.cfg.Enabled }
func (u *Updater) Check(ctx context.Context) (CheckResult, error) {
	if !u.cfg.Enabled {
		return CheckResult{}, fmt.Errorf("updates disabled")
	}
	manifestBytes, e := u.download(ctx, u.cfg.ManifestURL, 4<<20)
	if e != nil {
		return CheckResult{}, e
	}
	sig, e := u.download(ctx, u.cfg.SignatureURL, 4096)
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
	if e = json.Unmarshal(manifestBytes, &m); e != nil {
		return CheckResult{}, e
	}
	if m.SchemaVersion != 1 {
		return CheckResult{}, fmt.Errorf("unsupported manifest schema")
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
	cmp := compareVersions(m.Version, u.current)
	available := cmp > 0 || u.cfg.AllowDowngrade && cmp != 0
	return CheckResult{u.current, m.Version, available, m, a}, nil
}
func (u *Updater) Apply(ctx context.Context, r CheckResult) (Pending, error) {
	if !r.Available {
		return Pending{}, fmt.Errorf("no update available")
	}
	if r.Asset.Size <= 0 || r.Asset.Size > 200<<20 {
		return Pending{}, fmt.Errorf("invalid asset size")
	}
	exe, e := os.Executable()
	if e != nil {
		return Pending{}, e
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return Pending{}, e
	}
	dir := filepath.Dir(exe)
	tmp, e := os.CreateTemp(dir, ".krm-update-*")
	if e != nil {
		return Pending{}, e
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, r.Asset.URL, nil)
	if e != nil {
		tmp.Close()
		return Pending{}, e
	}
	resp, e := u.client.Do(req)
	if e != nil {
		tmp.Close()
		return Pending{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		tmp.Close()
		return Pending{}, fmt.Errorf("asset HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, r.Asset.Size+1))
	if e == nil {
		e = tmp.Sync()
	}
	if e2 := tmp.Close(); e == nil {
		e = e2
	}
	if e != nil {
		return Pending{}, e
	}
	if n != r.Asset.Size {
		return Pending{}, fmt.Errorf("asset size mismatch: got %d want %d", n, r.Asset.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, r.Asset.SHA256) {
		return Pending{}, fmt.Errorf("asset checksum mismatch")
	}
	if e = os.Chmod(tmpPath, 0755); e != nil {
		return Pending{}, e
	}
	backup := exe + ".previous"
	_ = os.Remove(backup)
	if e = copyFile(exe, backup, 0755); e != nil {
		return Pending{}, e
	}
	pending := Pending{1, u.current, r.LatestVersion, backup, exe, time.Now().UTC(), 0}
	if e = u.savePending(pending); e != nil {
		_ = os.Remove(backup)
		return Pending{}, e
	}
	if e = os.Rename(tmpPath, exe); e != nil {
		_ = os.Remove(u.pendingPath())
		_ = os.Remove(backup)
		return Pending{}, e
	}
	if e = syncDir(dir); e != nil {
		_ = copyFile(backup, exe, 0755)
		_ = os.Remove(u.pendingPath())
		_ = os.Remove(backup)
		return Pending{}, e
	}
	return pending, nil
}
func (u *Updater) PrepareStartup() (bool, error) {
	b, e := os.ReadFile(u.pendingPath())
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var p Pending
	if e = json.Unmarshal(b, &p); e != nil {
		return false, e
	}
	if p.Attempts >= 1 {
		if e = copyFile(p.BackupPath, p.ExecutablePath, 0755); e != nil {
			return false, e
		}
		_ = os.Remove(u.pendingPath())
		_ = os.Remove(p.BackupPath)
		return true, nil
	}
	p.Attempts++
	return false, u.savePending(p)
}
func (u *Updater) MarkHealthy() error {
	b, e := os.ReadFile(u.pendingPath())
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var p Pending
	if e = json.Unmarshal(b, &p); e == nil {
		_ = os.Remove(p.BackupPath)
	}
	return os.Remove(u.pendingPath())
}
func (u *Updater) download(ctx context.Context, raw string, max int64) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, e
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
func (u *Updater) savePending(p Pending) error {
	if e := os.MkdirAll(u.stateDir, 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(u.stateDir, ".pending-*")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	_ = f.Chmod(0600)
	if _, e = f.Write(append(b, '\n')); e == nil {
		e = f.Sync()
	}
	if e2 := f.Close(); e == nil {
		e = e2
	}
	if e != nil {
		return e
	}
	if e = os.Rename(n, u.pendingPath()); e != nil {
		return e
	}
	return syncDir(u.stateDir)
}
func selectAsset(xs []Asset) (Asset, error) {
	goarm := os.Getenv("GOARM")
	for _, a := range xs {
		if a.OS == runtime.GOOS && a.Arch == runtime.GOARCH && (a.GOARM == "" || goarm == "" || a.GOARM == goarm) {
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
func copyFile(src, dst string, mode os.FileMode) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	dir := filepath.Dir(dst)
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	out, e := os.CreateTemp(dir, ".krm-copy-*")
	if e != nil {
		return e
	}
	name := out.Name()
	defer os.Remove(name)
	if e = out.Chmod(mode); e != nil {
		out.Close()
		return e
	}
	if _, e = io.Copy(out, in); e == nil {
		e = out.Sync()
	}
	if e2 := out.Close(); e == nil {
		e = e2
	}
	if e != nil {
		return e
	}
	if e = os.Rename(name, dst); e != nil {
		return e
	}
	return syncDir(dir)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func compareVersions(a, b string) int {
	pa := parseVersion(a)
	pb := parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] > pb[i] {
			return 1
		}
		if pa[i] < pb[i] {
			return -1
		}
	}
	arc := strings.Contains(a, "-rc")
	brc := strings.Contains(b, "-rc")
	if arc && !brc {
		return -1
	}
	if !arc && brc {
		return 1
	}
	return strings.Compare(a, b)
}
func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	v = strings.SplitN(v, "-", 2)[0]
	p := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < len(p) && i < 3; i++ {
		out[i], _ = strconv.Atoi(p[i])
	}
	return out
}
