package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/daemonlock"
	"github.com/jarymor-ux/kee-route-manager/internal/update"
)

// Install imports an already downloaded signed release. It is a one-time offline
// bootstrap, refuses live controller owners, and never executes downloaded code.
func Install(configFile, uiConfig, releaseDir, binDir string) error {
	c, err := config.Load(configFile)
	if err != nil {
		return err
	}
	if c.Instance.Role != "controller" {
		return errors.New("launcher requires a controller configuration")
	}
	if err = privateDir(c.Update.InstallDir); err != nil {
		return err
	}
	lock, err := daemonlock.Acquire(filepath.Join(c.Update.InstallDir, "launcher.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	var existing *record
	if _, err = os.Lstat(filepath.Join(c.Update.InstallDir, "launcher.json")); err == nil {
		r, e := loadRecord(c.Update.InstallDir)
		if e != nil {
			return e
		}
		if r.Phase != "committed" || r.LastResult != "installed" || r.Previous != "" || r.Candidate != "" {
			return errors.New("launcher installation already exists; use update-apply")
		}
		existing = &r
	} else if !os.IsNotExist(err) {
		return err
	}
	owner, err := daemonlock.Acquire(filepath.Join(c.Paths.StateDir, "daemon.lock"))
	if err != nil {
		return fmt.Errorf("stop the controller before launcher installation: %w", err)
	}
	defer owner.Close()
	tunnelOwner, err := daemonlock.Acquire(filepath.Join(c.Xray.ConfigDir, ".krm-daemon.lock"))
	if err != nil {
		return err
	}
	defer tunnelOwner.Close()
	if uiConfig != "" {
		if !filepath.IsAbs(uiConfig) {
			return errors.New("UI config must be absolute")
		}
		ui, err := config.Load(uiConfig)
		if err != nil {
			return err
		}
		if ui.Instance.Role != "ui" || c.Platform.Kind != "keenetic" && c.Platform.Kind != "openwrt" {
			return errors.New("joint local UI supervision supports Keenetic/OpenWrt; keep Linux UI in its unprivileged service")
		}
		// Hold the configured port through bootstrap, both proving the prior UI
		// stopped and preventing a competing supervisor from binding mid-install.
		listener, err := net.Listen("tcp", ui.Web.Listen)
		if err != nil {
			return fmt.Errorf("stop the local UI before launcher installation: %w", err)
		}
		defer listener.Close()
	}
	release, err := update.ImportSeedRelease(c.Update.InstallDir, releaseDir, c.Update.PublicKey)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(binDir) {
		return errors.New("binary directory must be absolute")
	}
	if existing != nil && (existing.Active != release.Version || existing.ActiveDigest != release.ManifestSHA256 || existing.UIConfig != uiConfig) {
		return errors.New("existing launcher bootstrap differs from the supplied signed seed")
	}
	if info, e := os.Lstat(filepath.Join(c.Update.InstallDir, "current")); e == nil && info.Mode()&os.ModeSymlink == 0 {
		return errors.New("refusing non-symlink current path")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	components := []string{"kee-route-managerd", "kee-route-managerctl"}
	if uiConfig != "" {
		components = append(components, "kee-route-manager-ui")
	}
	// Preflight every replacement before changing any of the standard entrypoints.
	for _, name := range components {
		path := filepath.Join(binDir, name)
		if _, err := os.Lstat(path + ".launcher-new"); !os.IsNotExist(err) {
			return fmt.Errorf("temporary executable entry already exists: %s", path+".launcher-new")
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if existing != nil && info.Mode()&os.ModeSymlink != 0 {
			target, e := os.Readlink(path)
			if e == nil && target == filepath.Join(c.Update.InstallDir, "current", name) {
				continue
			}
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular executable %s", name)
		}
		old, err := digestFile(path)
		if err != nil {
			return err
		}
		seed, err := digestFile(filepath.Join(release.Directory, name))
		if err != nil || old != seed {
			return fmt.Errorf("existing %s differs from signed seed; stage a verified matching bootstrap first", name)
		}
	}
	rec := record{Schema: 1, Active: release.Version, ActiveDigest: release.ManifestSHA256, Phase: "committed", UIConfig: uiConfig, LastResult: "installed"}
	if err = saveRecord(c.Update.InstallDir, rec); err != nil {
		return err
	}
	if err = setCurrent(c.Update.InstallDir, release.Version); err != nil {
		return err
	}
	for _, name := range components {
		path := filepath.Join(binDir, name)
		tmp := path + ".launcher-new"
		if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
			return fmt.Errorf("temporary executable entry already exists: %s", tmp)
		}
		if err = os.Symlink(filepath.Join(c.Update.InstallDir, "current", name), tmp); err != nil {
			return err
		}
		if err = os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return nil
}

func digestFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
