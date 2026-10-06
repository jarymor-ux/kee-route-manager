package subscription

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

const (
	sourceStoreSchema   = 1
	sourceStoreMaxBytes = 256 << 10
)

type SourceStore struct {
	path string
}

type sourceDocument struct {
	Schema  int             `json:"schema"`
	Sources []config.Source `json:"sources"`
}

func NewSourceStore(stateDir string) *SourceStore {
	return &SourceStore{path: filepath.Join(stateDir, "subscriptions.json")}
}

func (s *SourceStore) Load() ([]config.Source, bool, error) {
	if s == nil || s.path == "" {
		return nil, false, errors.New("subscription source store is unavailable")
	}
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("stat subscription source store: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, errors.New("subscription source store must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, false, errors.New("subscription source store must not be group/world accessible")
	}

	f, err := os.Open(s.path)
	if err != nil {
		return nil, false, fmt.Errorf("open subscription source store: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, sourceStoreMaxBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("read subscription source store: %w", err)
	}
	if len(data) > sourceStoreMaxBytes {
		return nil, false, errors.New("subscription source store is too large")
	}

	var doc sourceDocument
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, false, fmt.Errorf("decode subscription source store: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, false, errors.New("subscription source store contains trailing data")
	}
	if doc.Schema != sourceStoreSchema {
		return nil, false, fmt.Errorf("unsupported subscription source store schema %d", doc.Schema)
	}
	return cloneSources(doc.Sources), true, nil
}

func (s *SourceStore) Save(sources []config.Source) error {
	if s == nil || s.path == "" {
		return errors.New("subscription source store is unavailable")
	}
	data, err := json.MarshalIndent(sourceDocument{Schema: sourceStoreSchema, Sources: cloneSources(sources)}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal subscription source store: %w", err)
	}
	data = append(data, '\\n')
	if len(data) > sourceStoreMaxBytes {
		return errors.New("subscription source store is too large")
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create subscription source store directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".subscriptions-*")
	if err != nil {
		return fmt.Errorf("create temporary subscription source store: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temporary subscription source store: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary subscription source store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary subscription source store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary subscription source store: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace subscription source store: %w", err)
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open subscription source store directory: %w", err)
	}
	defer dirFile.Close()
	if err := dirFile.Sync(); err != nil {
		return fmt.Errorf("sync subscription source store directory: %w", err)
	}
	return nil
}

func cloneSources(in []config.Source) []config.Source {
	out := make([]config.Source, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].Headers != nil {
			out[i].Headers = make(map[string]string, len(in[i].Headers))
			for key, value := range in[i].Headers {
				out[i].Headers[key] = value
			}
		}
	}
	return out
}
