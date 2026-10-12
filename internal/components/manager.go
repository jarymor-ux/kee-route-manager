// Package components coordinates explicitly approved VPN runtime changes.
package components

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ID string

const (
	Xray      ID = "xray"
	XKeen     ID = "xkeen"
	AmneziaWG ID = "amneziawg"
)

type Backend interface {
	Installed(context.Context, ID) (string, error)
	Latest(context.Context, ID) (string, error)
	Install(context.Context, ID, string) error
}

type Item struct {
	ID               ID     `json:"id"`
	InstalledVersion string `json:"installed_version,omitempty"`
	LatestVersion    string `json:"latest_version,omitempty"`
	Missing          bool   `json:"missing"`
	UpdateAvailable  bool   `json:"update_available"`
	Error            string `json:"error,omitempty"`
}

type Snapshot struct {
	CheckedAt time.Time `json:"checked_at"`
	Items     []Item    `json:"items"`
}

type Manager struct {
	mu        sync.Mutex
	platform  string
	interval  time.Duration
	backend   Backend
	now       func() time.Time
	last      Snapshot
	cacheFile string
}

func supported(platform string) ([]ID, error) {
	switch platform {
	case "keenetic":
		return []ID{Xray, XKeen, AmneziaWG}, nil
	case "linux-systemd", "openwrt":
		return []ID{Xray, AmneziaWG}, nil
	default:
		return nil, fmt.Errorf("unsupported platform %q", platform)
	}
}

// NewManager refuses overly aggressive or invalid background schedules.
func NewManager(platform string, interval time.Duration, backend Backend, now func() time.Time) (*Manager, error) {
	if interval < time.Hour || interval > 365*24*time.Hour {
		return nil, fmt.Errorf("component update interval must be between 1h and 365d")
	}
	if backend == nil || now == nil {
		return nil, errors.New("component backend and clock are required")
	}
	if _, err := supported(platform); err != nil {
		return nil, err
	}
	return &Manager{platform: platform, interval: interval, backend: backend, now: now}, nil
}

func (m *Manager) Check(ctx context.Context, force bool) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	timestamp := m.now().UTC()
	if !force && !m.last.CheckedAt.IsZero() && timestamp.Sub(m.last.CheckedAt) < m.interval {
		return copySnapshot(m.last), nil
	}
	ids, _ := supported(m.platform)
	snapshot := Snapshot{CheckedAt: timestamp, Items: make([]Item, 0, len(ids))}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		item := Item{ID: id}
		installed, installedErr := m.backend.Installed(ctx, id)
		if installedErr != nil {
			item.Error = installedErr.Error()
		} else {
			item.InstalledVersion = installed
			item.Missing = installed == ""
		}
		latest, latestErr := m.backend.Latest(ctx, id)
		if latestErr != nil {
			if item.Error != "" {
				item.Error += "; "
			}
			item.Error += latestErr.Error()
		} else {
			item.LatestVersion = latest
			item.UpdateAvailable = installed != "" && latest != "" && installed != latest
		}
		snapshot.Items = append(snapshot.Items, item)
	}
	m.last = copySnapshot(snapshot)
	if err := m.persist(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// Install is deliberately separate from Check. No periodic or manual discovery executes it.
func (m *Manager) Install(ctx context.Context, id ID) (Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids, err := supported(m.platform)
	if err != nil {
		return Item{}, err
	}
	ok := false
	for _, candidate := range ids {
		if candidate == id {
			ok = true
			break
		}
	}
	if !ok {
		return Item{}, fmt.Errorf("unsupported component %q on %s", id, m.platform)
	}
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	latest, err := m.backend.Latest(ctx, id)
	if err != nil {
		return Item{}, err
	}
	if latest == "" {
		return Item{}, fmt.Errorf("latest version of %s unknown", id)
	}
	if err := m.backend.Install(ctx, id, latest); err != nil {
		return Item{}, err
	}
	installed, err := m.backend.Installed(ctx, id)
	if err != nil {
		return Item{}, err
	}
	if installed == "" {
		return Item{}, fmt.Errorf("%s installation could not be verified", id)
	}
	// Installation changes installed state: invalidate discovery cache.
	m.last = Snapshot{}
	if err := m.persist(); err != nil {
		return Item{}, err
	}
	return Item{ID: id, InstalledVersion: installed, LatestVersion: latest, UpdateAvailable: installed != latest}, nil
}

func copySnapshot(s Snapshot) Snapshot {
	out := s
	out.Items = append([]Item(nil), s.Items...)
	return out
}

// SetCacheFile restores and persists only public component update metadata.
// The private state directory must be created and locked by the daemon first.
func (m *Manager) SetCacheFile(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("component cache path must be absolute")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("component cache must be private regular file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(data) > 128*1024 {
			return errors.New("component cache oversized")
		}
		var snapshot Snapshot
		if err = json.Unmarshal(data, &snapshot); err != nil {
			return err
		}
		if snapshot.CheckedAt.After(m.now().Add(time.Hour)) {
			return errors.New("component cache timestamp is in the future")
		}
		ids, _ := supported(m.platform)
		if len(snapshot.Items) != 0 {
			if len(snapshot.Items) != len(ids) {
				return errors.New("component cache platform mismatch")
			}
			for i, item := range snapshot.Items {
				if item.ID != ids[i] {
					return errors.New("component cache platform mismatch")
				}
			}
		}
		m.last = copySnapshot(snapshot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	m.cacheFile = path
	return nil
}

func (m *Manager) persist() error {
	if m.cacheFile == "" {
		return nil
	}
	dir := filepath.Dir(m.cacheFile)
	tmp, err := os.CreateTemp(dir, ".krm-components-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	data, err := json.Marshal(m.last)
	if err != nil {
		tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), m.cacheFile)
}
