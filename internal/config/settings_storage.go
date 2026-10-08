package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"syscall"

	"go.yaml.in/yaml/v3"
)

const settingsMaxBytes = 2 << 20

type SettingsSnapshot struct {
	Settings EditableSettings `json:"settings"`
	Revision string           `json:"revision"`
	PoolSize int              `json:"pool_size"`
}

type SettingsEditor struct {
	path string
	mu   sync.Mutex
}
type SettingsTransaction struct {
	Config        Config
	Changed       bool
	editor        *SettingsEditor
	before, after string
	done          bool
}

// Revision identifies the persisted candidate, or the untouched no-op baseline.
func (tx *SettingsTransaction) Revision() string {
	if tx.Changed {
		return tx.after
	}
	return tx.before
}

type settingsIntent struct {
	Schema    int    `json:"schema"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Committed bool   `json:"committed"`
}

func NewSettingsEditor(path string) *SettingsEditor {
	absolute, err := filepath.Abs(path)
	if err != nil || path == "" {
		absolute = ""
	}
	return &SettingsEditor{path: absolute}
}
func (e *SettingsEditor) backup() string  { return e.path + ".settings-backup" }
func (e *SettingsEditor) pending() string { return e.path + ".settings-pending.json" }
func settingsDigest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func privateSettingsRead(path string) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > settingsMaxBytes {
		return nil, ErrSettingsUnavailable
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return nil, ErrSettingsUnavailable
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrSettingsUnavailable
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return nil, ErrSettingsUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, settingsMaxBytes+1))
	if err != nil || len(data) > settingsMaxBytes {
		return nil, ErrSettingsUnavailable
	}
	return data, nil
}
func (e *SettingsEditor) read() ([]byte, Config, error) {
	if e.path == "" {
		return nil, Config{}, ErrSettingsUnavailable
	}
	data, err := privateSettingsRead(e.path)
	if err != nil {
		return nil, Config{}, err
	}
	cfg, err := LoadBytes(data, e.path)
	if err != nil || cfg.Instance.Role != "controller" {
		return nil, Config{}, ErrSettingsUnavailable
	}
	return data, cfg, nil
}
func (e *SettingsEditor) Snapshot() (SettingsSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := os.Lstat(e.pending()); !os.IsNotExist(err) {
		return SettingsSnapshot{}, ErrSettingsBusy
	}
	data, cfg, err := e.read()
	if err != nil {
		return SettingsSnapshot{}, err
	}
	return SettingsSnapshot{SettingsFromConfig(cfg), settingsDigest(data), cfg.Pool.Size}, nil
}

// Validate is a revision-checked dry run without file or journal mutations.
func (e *SettingsEditor) Validate(settings EditableSettings, expectedRevision string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := os.Lstat(e.pending()); !os.IsNotExist(err) {
		return ErrSettingsBusy
	}
	data, cfg, err := e.read()
	if err != nil {
		return err
	}
	if settingsDigest(data) != expectedRevision {
		return ErrSettingsConflict
	}
	_, err = settings.ApplyTo(cfg)
	return err
}

func (e *SettingsEditor) Stage(settings EditableSettings, expectedRevision string) (*SettingsTransaction, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := os.Lstat(e.pending()); !os.IsNotExist(err) {
		return nil, ErrSettingsBusy
	}
	data, cfg, err := e.read()
	if err != nil {
		return nil, err
	}
	before := settingsDigest(data)
	if before != expectedRevision {
		return nil, ErrSettingsConflict
	}
	candidate, err := settings.ApplyTo(cfg)
	if err != nil {
		return nil, err
	}
	tx := &SettingsTransaction{Config: candidate, editor: e, before: before}
	if reflect.DeepEqual(SettingsFromConfig(cfg), settings) {
		return tx, nil
	}
	var document yaml.Node
	if yaml.Unmarshal(normalizeLegacyPlainMappingScalars(data), &document) != nil || len(document.Content) != 1 {
		return nil, ErrSettingsInvalid
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, ErrSettingsInvalid
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return nil, ErrSettingsInvalid
	}
	patchSettingsNode(document.Content[0], values)
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if encoder.Encode(&document) != nil || encoder.Close() != nil {
		return nil, ErrSettingsInvalid
	}
	if out.Len() > settingsMaxBytes {
		return nil, ErrSettingsInvalid
	}
	candidate, err = LoadBytes(out.Bytes(), e.path)
	if err != nil {
		return nil, ErrSettingsInvalid
	}
	tx.Config = candidate
	tx.Changed = true
	tx.after = settingsDigest(out.Bytes())
	if err = writeSettingsAtomic(e.backup(), data); err != nil {
		return nil, err
	}
	intent := settingsIntent{Schema: 1, Before: before, After: tx.after}
	if err = e.writeIntent(intent); err != nil {
		return nil, err
	}
	// Recheck external writes after the durable intent, before replacement.
	latest, err := privateSettingsRead(e.path)
	if err != nil || settingsDigest(latest) != before {
		return nil, ErrSettingsRecovery
	}
	if err = writeSettingsAtomic(e.path, out.Bytes()); err != nil {
		return nil, err
	}
	return tx, nil
}

func patchSettingsNode(node *yaml.Node, values map[string]any) {
	if node.Kind != yaml.MappingNode {
		node.Kind = yaml.MappingNode
		node.Tag = "!!map"
		node.Content = nil
		node.Style = 0
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var child *yaml.Node
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				child = node.Content[i+1]
				break
			}
		}
		if child == nil {
			child = &yaml.Node{}
			node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
		}
		value := values[key]
		if mapping, ok := value.(map[string]any); ok {
			patchSettingsNode(child, mapping)
			continue
		}
		var next yaml.Node
		raw, _ := json.Marshal(value)
		_ = yaml.Unmarshal(raw, &next)
		replacement := next.Content[0]
		replacement.HeadComment = child.HeadComment
		replacement.LineComment = child.LineComment
		replacement.FootComment = child.FootComment
		if child.Kind == yaml.ScalarNode && child.Tag == replacement.Tag {
			replacement.Style = child.Style
		}
		*child = *replacement
	}
}

func writeSettingsAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".krm-settings-*")
	if err != nil {
		return ErrSettingsUnavailable
	}
	name := f.Name()
	defer os.Remove(name)
	if f.Chmod(0600) != nil {
		f.Close()
		return ErrSettingsUnavailable
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrSettingsUnavailable
	}
	if os.Rename(name, path) != nil {
		return ErrSettingsUnavailable
	}
	return syncSettingsDir(dir)
}
func syncSettingsDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return ErrSettingsUnavailable
	}
	defer f.Close()
	if f.Sync() != nil {
		return ErrSettingsUnavailable
	}
	return nil
}
func (e *SettingsEditor) writeIntent(intent settingsIntent) error {
	raw, err := json.Marshal(intent)
	if err != nil {
		return ErrSettingsUnavailable
	}
	return writeSettingsAtomic(e.pending(), raw)
}
func (e *SettingsEditor) intent() (*settingsIntent, error) {
	if _, err := os.Lstat(e.pending()); os.IsNotExist(err) {
		return nil, nil
	}
	raw, err := privateSettingsRead(e.pending())
	if err != nil {
		return nil, ErrSettingsRecovery
	}
	var intent settingsIntent
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&intent) != nil || dec.Decode(new(any)) != io.EOF || intent.Schema != 1 {
		return nil, ErrSettingsRecovery
	}
	for _, digest := range []string{intent.Before, intent.After} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return nil, ErrSettingsRecovery
		}
	}
	if intent.Before == intent.After {
		return nil, ErrSettingsRecovery
	}
	return &intent, nil
}
func (e *SettingsEditor) cleanup() error {
	// Removing the journal first is safe only after restore or durable commit.
	if err := os.Remove(e.pending()); err != nil && !os.IsNotExist(err) {
		return ErrSettingsUnavailable
	}
	if err := syncSettingsDir(filepath.Dir(e.path)); err != nil {
		return err
	}
	if err := os.Remove(e.backup()); err != nil && !os.IsNotExist(err) {
		return ErrSettingsUnavailable
	}
	return syncSettingsDir(filepath.Dir(e.path))
}
func (tx *SettingsTransaction) Commit() error {
	e := tx.editor
	e.mu.Lock()
	defer e.mu.Unlock()
	if tx.done {
		return ErrSettingsConflict
	}
	if !tx.Changed {
		tx.done = true
		return nil
	}
	intent, err := e.intent()
	if err != nil {
		return err
	}
	if intent == nil || intent.Before != tx.before || intent.After != tx.after {
		return ErrSettingsConflict
	}
	raw, err := privateSettingsRead(e.path)
	if err != nil || settingsDigest(raw) != tx.after {
		return ErrSettingsRecovery
	}
	intent.Committed = true
	if err = e.writeIntent(*intent); err != nil {
		return err
	}
	tx.done = true
	// Commit is already durable; failed cleanup is retried on startup.
	_ = e.cleanup()
	return nil
}
func (tx *SettingsTransaction) Rollback() error {
	e := tx.editor
	e.mu.Lock()
	defer e.mu.Unlock()
	if tx.done {
		return ErrSettingsConflict
	}
	if !tx.Changed {
		tx.done = true
		return nil
	}
	intent, err := e.intent()
	if err != nil {
		return err
	}
	if intent == nil || intent.Committed || intent.Before != tx.before || intent.After != tx.after {
		return ErrSettingsConflict
	}
	if err = e.recover(); err != nil {
		return err
	}
	tx.done = true
	return nil
}
func (e *SettingsEditor) Recover() error { e.mu.Lock(); defer e.mu.Unlock(); return e.recover() }
func (e *SettingsEditor) recover() error {
	intent, err := e.intent()
	if err != nil || intent == nil {
		return err
	}
	current, err := privateSettingsRead(e.path)
	if err != nil {
		return ErrSettingsRecovery
	}
	hash := settingsDigest(current)
	if intent.Committed {
		if hash != intent.After {
			return ErrSettingsRecovery
		}
		return e.cleanup()
	}
	if hash != intent.Before && hash != intent.After {
		return ErrSettingsRecovery
	}
	backup, err := privateSettingsRead(e.backup())
	if err != nil || settingsDigest(backup) != intent.Before {
		return ErrSettingsRecovery
	}
	if _, err = LoadBytes(backup, e.path); err != nil {
		return ErrSettingsRecovery
	}
	if hash != intent.Before {
		if err = writeSettingsAtomic(e.path, backup); err != nil {
			return err
		}
	}
	return e.cleanup()
}

// StartupConfig selects a verified baseline without writing. The daemon must
// acquire its ownership locks before Recover performs any disk mutation.
func StartupConfig(path string) (Config, error) {
	e := NewSettingsEditor(path)
	intent, err := e.intent()
	if err != nil {
		return Config{}, err
	}
	if intent == nil {
		return Load(path)
	}
	current, err := privateSettingsRead(e.path)
	if err != nil {
		return Config{}, ErrSettingsRecovery
	}
	hash := settingsDigest(current)
	if intent.Committed {
		if hash != intent.After {
			return Config{}, ErrSettingsRecovery
		}
		cfg, err := LoadBytes(current, e.path)
		if err != nil {
			return Config{}, ErrSettingsRecovery
		}
		return cfg, nil
	}
	if hash != intent.Before && hash != intent.After {
		return Config{}, ErrSettingsRecovery
	}
	backup, err := privateSettingsRead(e.backup())
	if err != nil || settingsDigest(backup) != intent.Before {
		return Config{}, ErrSettingsRecovery
	}
	cfg, err := LoadBytes(backup, e.path)
	if err != nil {
		return Config{}, ErrSettingsRecovery
	}
	return cfg, nil
}
