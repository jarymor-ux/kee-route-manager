package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type StagedRelease struct {
	Version        string           `json:"version"`
	ManifestSHA256 string           `json:"manifest_sha256"`
	Directory      string           `json:"directory"`
	Assets         map[string]Asset `json:"assets"`
}

var ErrNoUpdate = errors.New("no update available")
var ErrStageBusy = errors.New("another release staging operation is active")

var componentOrder = []string{"daemon", "ui", "ctl"}

// Stage always discovers and authenticates the release again. CheckResult is a
// display value, never an authority to download or execute a caller-chosen asset.
// Publishing a staged directory does not activate it or stop any process.
func (u *Updater) Stage(ctx context.Context) (StagedRelease, error) {
	if u.applySupportErr != nil {
		return StagedRelease{}, u.applySupportErr
	}
	checked, err := u.checkSigned(ctx)
	if err != nil {
		return StagedRelease{}, err
	}
	if !checked.result.StageSupported {
		return StagedRelease{}, fmt.Errorf("release does not support update protocol 1")
	}
	if !checked.result.Available {
		return StagedRelease{}, ErrNoUpdate
	}
	return stageRelease(ctx, u.cfg.InstallDir, checked.manifest, checked.signature, u.cfg.PublicKey, u.cfg.Channel, func(ctx context.Context, asset Asset, destination string) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
		if err != nil {
			return err
		}
		request.Header.Set("User-Agent", "Kee-Route-Manager")
		response, err := u.client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("asset HTTP %d", response.StatusCode)
		}
		if response.ContentLength >= 0 && response.ContentLength != asset.Size {
			return fmt.Errorf("asset size differs from signed manifest")
		}
		return writeVerifiedBinary(destination, response.Body, asset)
	})
}

// ImportRelease seeds a bootstrap slot without executing downloaded programs.
// Source files may be public; the signature and exact signed hashes are the
// authority. The destination obeys the same private immutable layout as Stage.
func ImportRelease(root, sourceDir, publicKey, channel string) (StagedRelease, error) {
	if channel != "rc" && channel != "stable" {
		return StagedRelease{}, fmt.Errorf("unsupported update channel")
	}
	metadata := "manifest-" + channel + ".json"
	manifest, err := readRegularFile(filepath.Join(sourceDir, metadata), 4<<20, false, false)
	if err != nil {
		return StagedRelease{}, err
	}
	signature, err := readRegularFile(filepath.Join(sourceDir, metadata+".sig"), 4096, false, false)
	if err != nil {
		return StagedRelease{}, err
	}
	return stageRelease(context.Background(), root, manifest, signature, publicKey, channel, func(_ context.Context, asset Asset, destination string) error {
		file, err := openRegularFile(filepath.Join(sourceDir, asset.Name), false, false)
		if err != nil {
			return err
		}
		defer file.Close()
		return writeVerifiedBinary(destination, file, asset)
	})
}

// VerifyRelease must be called by the stable launcher immediately before use.
// Paths come from the install root and version, never from a pending JSON file.
// Version ordering/rollback authorization belongs to the launcher's transaction.
func VerifyRelease(root, version, publicKey, channel string) (StagedRelease, error) {
	canonical, err := releaseVersion(version)
	if err != nil {
		return StagedRelease{}, err
	}
	root, err = privateInstallRoot(root, false)
	if err != nil {
		return StagedRelease{}, err
	}
	releases := filepath.Join(root, "releases")
	if err = privateDirectory(releases, false); err != nil {
		return StagedRelease{}, err
	}
	return verifyReleaseDirectory(filepath.Join(releases, canonical), canonical, publicKey, channel)
}

func stageRelease(ctx context.Context, root string, data, signature []byte, key, channel string, download func(context.Context, Asset, string) error) (StagedRelease, error) {
	manifest, err := authenticateManifest(data, signature, key, channel)
	if err != nil {
		return StagedRelease{}, err
	}
	assets, err := bundleAssets(manifest)
	if err != nil {
		return StagedRelease{}, err
	}
	version, err := releaseVersion(manifest.Version)
	if err != nil {
		return StagedRelease{}, err
	}
	if err = ctx.Err(); err != nil {
		return StagedRelease{}, err
	}
	root, err = privateInstallRoot(root, true)
	if err != nil {
		return StagedRelease{}, err
	}
	lock, err := os.OpenFile(filepath.Join(root, "stage.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return StagedRelease{}, err
	}
	defer lock.Close()
	if err = checkPrivateFile(lock, false); err != nil {
		return StagedRelease{}, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return StagedRelease{}, ErrStageBusy
		}
		return StagedRelease{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	releases := filepath.Join(root, "releases")
	if err = privateDirectory(releases, true); err != nil {
		return StagedRelease{}, err
	}
	destination := filepath.Join(releases, version)
	if _, err = os.Lstat(destination); err == nil {
		existing, err := verifyReleaseDirectory(destination, version, key, channel)
		if err != nil {
			return StagedRelease{}, err
		}
		if existing.ManifestSHA256 != digest(data) {
			return StagedRelease{}, fmt.Errorf("immutable release version already has a different manifest")
		}
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return StagedRelease{}, err
	}
	temporary, err := os.MkdirTemp(releases, ".stage-")
	if err != nil {
		return StagedRelease{}, err
	}
	defer os.RemoveAll(temporary)
	for _, component := range componentOrder {
		if err = ctx.Err(); err != nil {
			return StagedRelease{}, err
		}
		if err = download(ctx, assets[component], filepath.Join(temporary, componentFiles[component])); err != nil {
			return StagedRelease{}, fmt.Errorf("stage %s: %w", component, err)
		}
	}
	for name, content := range map[string][]byte{"manifest.json": data, "manifest.sig": signature} {
		if err = writePrivateFile(filepath.Join(temporary, name), content); err != nil {
			return StagedRelease{}, err
		}
	}
	verified, err := verifyReleaseDirectory(temporary, version, key, channel)
	if err != nil {
		return StagedRelease{}, err
	}
	if err = syncDir(temporary); err != nil {
		return StagedRelease{}, err
	}
	if err = ctx.Err(); err != nil {
		return StagedRelease{}, err
	}
	if err = os.Rename(temporary, destination); err != nil {
		return StagedRelease{}, err
	}
	if err = syncDir(releases); err != nil {
		return StagedRelease{}, err
	}
	verified.Directory = destination
	return verified, nil
}

func verifyReleaseDirectory(directory, version, key, channel string) (StagedRelease, error) {
	if err := privateDirectory(directory, false); err != nil {
		return StagedRelease{}, err
	}
	data, err := readRegularFile(filepath.Join(directory, "manifest.json"), 4<<20, true, false)
	if err != nil {
		return StagedRelease{}, err
	}
	signature, err := readRegularFile(filepath.Join(directory, "manifest.sig"), 4096, true, false)
	if err != nil {
		return StagedRelease{}, err
	}
	manifest, err := authenticateManifest(data, signature, key, channel)
	if err != nil {
		return StagedRelease{}, err
	}
	canonical, err := releaseVersion(manifest.Version)
	if err != nil || canonical != version {
		return StagedRelease{}, fmt.Errorf("staged manifest version does not match requested directory")
	}
	assets, err := bundleAssets(manifest)
	if err != nil {
		return StagedRelease{}, err
	}
	for _, component := range componentOrder {
		asset := assets[component]
		file, err := openRegularFile(filepath.Join(directory, componentFiles[component]), true, true)
		if err != nil {
			return StagedRelease{}, err
		}
		_, verifyErr := verifyStream(io.Discard, file, asset)
		closeErr := file.Close()
		if verifyErr != nil {
			return StagedRelease{}, fmt.Errorf("verify %s: %w", component, verifyErr)
		}
		if closeErr != nil {
			return StagedRelease{}, closeErr
		}
	}
	return StagedRelease{Version: canonical, ManifestSHA256: digest(data), Directory: directory, Assets: assets}, nil
}

func releaseVersion(version string) (string, error) {
	if len(version) > 128 {
		return "", fmt.Errorf("release version too long")
	}
	if _, err := parseSemVer(version); err != nil {
		return "", err
	}
	return strings.TrimPrefix(version, "v"), nil
}

func privateInstallRoot(root string, create bool) (string, error) {
	if !filepath.IsAbs(root) || strings.ContainsRune(root, 0) {
		return "", fmt.Errorf("update install directory must be absolute")
	}
	root = filepath.Clean(root)
	if err := safeDirectoryAncestors(filepath.Dir(root)); err != nil {
		return "", err
	}
	if create {
		if err := makeDurableDirectories(root); err != nil {
			return "", err
		}
	}
	if err := privateDirectory(root, false); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(root)
}

func privateDirectory(path string, create bool) error {
	if create {
		if err := makeDurableDirectories(path); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode().Perm()&0500 != 0500 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("release directory must be an owner-only directory owned by this user")
	}
	return nil
}

// A private leaf beneath a directory writable by another user is replaceable.
// Trusted system symlinks (e.g. /var on macOS) are allowed, but both their
// original parents and resolved target parents must protect directory entries.
func safeDirectoryAncestors(path string) error {
	for {
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
				return fmt.Errorf("update directory has an untrusted ancestor owner")
			}
			if info.Mode()&os.ModeSymlink != 0 {
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil {
					return err
				}
				if err = safeDirectoryAncestors(resolved); err != nil {
					return err
				}
			} else if !info.IsDir() || info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
				return fmt.Errorf("update directory has an unsafe writable ancestor")
			}
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

func makeDurableDirectories(path string) error {
	err := os.Mkdir(path, 0700)
	if errors.Is(err, os.ErrNotExist) {
		if err = makeDurableDirectories(filepath.Dir(path)); err != nil {
			return err
		}
		err = os.Mkdir(path, 0700)
	}
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func checkPrivateFile(file *os.File, executable bool) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Mode().Perm()&0400 == 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return fmt.Errorf("release file must be a private regular file owned by this user")
	}
	if executable && info.Mode().Perm()&0100 == 0 {
		return fmt.Errorf("release binary is not executable")
	}
	return nil
}

func openRegularFile(path string, private, executable bool) (*os.File, error) {
	// O_NONBLOCK also prevents a malicious FIFO from hanging before fstat.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if private {
		err = checkPrivateFile(file, executable)
	} else {
		var info os.FileInfo
		info, err = file.Stat()
		if err == nil && !info.Mode().IsRegular() {
			err = fmt.Errorf("release source is not a regular file")
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func readRegularFile(path string, max int64, private, executable bool) ([]byte, error) {
	file, err := openRegularFile(path, private, executable)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("release metadata too large")
	}
	return data, nil
}

func verifyStream(destination io.Writer, source io.Reader, asset Asset) (int64, error) {
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(source, asset.Size+1))
	if err != nil {
		return count, err
	}
	if count != asset.Size {
		return count, fmt.Errorf("asset size differs from signed manifest")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), asset.SHA256) {
		return count, fmt.Errorf("asset checksum differs from signed manifest")
	}
	return count, nil
}

func writeVerifiedBinary(path string, source io.Reader, asset Asset) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = verifyStream(file, source, asset); err != nil {
		return err
	}
	if err = file.Chmod(0700); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func syncDir(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
