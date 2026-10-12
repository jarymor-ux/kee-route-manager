package amnezia

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var profileIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type ProfileInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
}

type ProfileStore struct {
	mu  sync.Mutex
	dir string
}

func NewProfileStore(stateDir string) (*ProfileStore, error) {
	if !filepath.IsAbs(stateDir) {
		return nil, errors.New("state directory must be absolute")
	}
	dir := filepath.Join(stateDir, "awg-profiles")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("AmneziaWG profile directory must be private, not a symlink")
	}
	return &ProfileStore{dir: dir}, nil
}

func (s *ProfileStore) Import(name string, raw []byte) (ProfileInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "\r\n\x00") {
		return ProfileInfo{}, errors.New("profile name must contain 1-128 printable bytes")
	}
	parsed, err := ParseConfig(raw)
	if err != nil {
		return ProfileInfo{}, err
	}
	hash := sha256.Sum256(raw)
	id := hex.EncodeToString(hash[:16])
	item := ProfileInfo{ID: id, Name: name, Endpoint: parsed.Peer.Endpoint}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, id+".conf")
	if _, err = os.Lstat(path); err == nil {
		current, readErr := s.readLocked(id)
		if readErr != nil {
			return ProfileInfo{}, readErr
		}
		if string(current) != string(raw) {
			return ProfileInfo{}, errors.New("profile fingerprint collision")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ProfileInfo{}, err
	} else if err = atomicPrivateWrite(s.dir, path, raw); err != nil {
		return ProfileInfo{}, err
	}
	meta, err := json.Marshal(item)
	if err != nil {
		return ProfileInfo{}, err
	}
	if err = atomicPrivateWrite(s.dir, filepath.Join(s.dir, id+".json"), meta); err != nil {
		return ProfileInfo{}, err
	}
	return item, nil
}

func (s *ProfileStore) List() ([]ProfileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]ProfileInfo, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".conf") {
			continue
		}
		id := strings.TrimSuffix(name, ".conf")
		if !profileIDPattern.MatchString(id) {
			return nil, errors.New("unknown profile filename")
		}
		raw, err := s.readLocked(id)
		if err != nil {
			return nil, err
		}
		p, err := ParseConfig(raw)
		if err != nil {
			return nil, errors.New("invalid profile in private store")
		}
		item := ProfileInfo{ID: id, Name: id, Endpoint: p.Peer.Endpoint}
		meta, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
		if err == nil {
			var saved ProfileInfo
			if json.Unmarshal(meta, &saved) == nil && saved.ID == id && saved.Name != "" {
				item.Name = saved.Name
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *ProfileStore) Read(id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(id)
}
func (s *ProfileStore) readLocked(id string) ([]byte, error) {
	if !profileIDPattern.MatchString(id) {
		return nil, errors.New("invalid profile ID")
	}
	path := filepath.Join(s.dir, id+".conf")
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0077 != 0 || fi.Size() > 256*1024 {
		return nil, errors.New("unsafe profile file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if _, err = ParseConfig(b); err != nil {
		return nil, fmt.Errorf("invalid stored profile: %w", err)
	}
	return b, nil
}
func (s *ProfileStore) Delete(id string) error {
	if !profileIDPattern.MatchString(id) {
		return errors.New("invalid profile ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readLocked(id); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.dir, id+".conf")); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.dir, id+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func atomicPrivateWrite(dir, path string, raw []byte) error {
	f, err := os.CreateTemp(dir, ".awg-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
