package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func settingsFixture(t *testing.T) (string, *SettingsEditor, SettingsSnapshot) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := strings.Replace(validYAML, "schema_version: 1", "# operator's private configuration\nschema_version: 1 # keep schema comment", 1)
	data = strings.Replace(data, "cache_dir: cache", "cache_dir: cache # keep relative cache path", 1)
	data = strings.Replace(data, "url: \"file:///tmp/sub.txt\"", "url: \"https://private.invalid/secret-subscription-token\"\n      headers:\n        Authorization: \"Bearer private-header\"", 1)
	data = strings.Replace(data, "speed:\n    enabled: false", "full_interval: 5m # keep cadence comment\n  speed:\n    enabled: false", 1)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	editor := NewSettingsEditor(path)
	snapshot, err := editor.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return path, editor, snapshot
}
func readSettingsTest(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func requireSettingsError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestSettingsAllowlistAndStrictCompleteJSON(t *testing.T) {
	_, _, snapshot := settingsFixture(t)
	raw, err := json.Marshal(snapshot.Settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"private-header", "secret-subscription-token", "\"sources\"", "temporary_proxy_port_start", "state_dir", "\"size\"", "public_key"} {
		if bytes.Contains(raw, []byte(hidden)) {
			t.Fatalf("hidden field %q exposed", hidden)
		}
	}
	var decoded EditableSettings
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	for _, group := range []string{"benchmark", "health", "failover", "subscriptions", "pool"} {
		copyObject := map[string]json.RawMessage{}
		for key, value := range object {
			copyObject[key] = value
		}
		delete(copyObject, group)
		invalid, _ := json.Marshal(copyObject)
		requireSettingsError(t, json.Unmarshal(invalid, &decoded), ErrSettingsInvalid)
	}
	for _, change := range []func(map[string]json.RawMessage){
		func(m map[string]json.RawMessage) { m["paths"] = json.RawMessage(`{"state_dir":"/host"}`) },
		func(m map[string]json.RawMessage) {
			m["pool"] = json.RawMessage(`{"size":20,"provider_diversity":{"enabled":false,"max_per_provider":0}}`)
		},
		func(m map[string]json.RawMessage) {
			var b map[string]json.RawMessage
			_ = json.Unmarshal(m["benchmark"], &b)
			delete(b, "min_improvement_percent")
			m["benchmark"], _ = json.Marshal(b)
		},
		func(m map[string]json.RawMessage) {
			var u map[string]json.RawMessage
			_ = json.Unmarshal(m["subscriptions"], &u)
			delete(u, "cache_enabled")
			m["subscriptions"], _ = json.Marshal(u)
		},
	} {
		copied := map[string]json.RawMessage{}
		for k, v := range object {
			copied[k] = v
		}
		change(copied)
		invalid, _ := json.Marshal(copied)
		requireSettingsError(t, json.Unmarshal(invalid, &decoded), ErrSettingsInvalid)
	}
}
func TestSettingsStagePreservesHiddenValuesCommentsAndRelativePaths(t *testing.T) {
	path, editor, snapshot := settingsFixture(t)
	previous, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := snapshot.Settings
	settings.Benchmark.FullInterval = Dur(7 * time.Minute)
	settings.Subscriptions.CacheEnabled = false
	tx, err := editor.Stage(settings, snapshot.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !tx.Changed {
		t.Fatal("change ignored")
	}
	raw := readSettingsTest(t, path)
	for _, preserved := range []string{"# operator's private configuration", "# keep schema comment", "# keep relative cache path", "# keep cadence comment", "cache_dir: cache", "secret-subscription-token", "Bearer private-header"} {
		if !bytes.Contains(raw, []byte(preserved)) {
			t.Fatalf("lost private input %q", preserved)
		}
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Benchmark.FullInterval.Duration != 7*time.Minute || cfg.Subscriptions.CacheEnabled || cfg.Pool.Size != previous.Pool.Size || cfg.Benchmark.TemporaryProxyPortStart != previous.Benchmark.TemporaryProxyPortStart || cfg.Subscriptions.Sources[0].URL != previous.Subscriptions.Sources[0].URL {
		t.Fatal("wrong merge")
	}
	if cfg.SourcePath() != path {
		t.Fatal("lost source path")
	}
	requireSettingsError(t, editor.Validate(settings, snapshot.Revision), ErrSettingsBusy)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal("configuration permissions changed")
	}
	latest, err := editor.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if latest.Revision == snapshot.Revision {
		t.Fatal("revision unchanged")
	}
	for _, sidecar := range []string{editor.pending(), editor.backup()} {
		if _, err = os.Stat(sidecar); !os.IsNotExist(err) {
			t.Fatal("committed sidecar retained")
		}
	}
	requireSettingsError(t, tx.Rollback(), ErrSettingsConflict)
}
func TestSettingsNoopInvalidAndStaleNeverMutate(t *testing.T) {
	path, editor, snapshot := settingsFixture(t)
	before := readSettingsTest(t, path)
	st, _ := os.Stat(path)
	if err := editor.Validate(snapshot.Settings, snapshot.Revision); err != nil {
		t.Fatal(err)
	}
	tx, err := editor.Stage(snapshot.Settings, snapshot.Revision)
	if err != nil || tx.Changed {
		t.Fatalf("noop %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	now, _ := os.Stat(path)
	if !os.SameFile(st, now) || !bytes.Equal(before, readSettingsTest(t, path)) {
		t.Fatal("noop replaced file")
	}
	invalid := snapshot.Settings
	invalid.Benchmark.FullInterval = Dur(30 * time.Second)
	requireSettingsError(t, editor.Validate(invalid, snapshot.Revision), ErrSettingsInvalid)
	_, err = editor.Stage(invalid, snapshot.Revision)
	requireSettingsError(t, err, ErrSettingsInvalid)
	// Validation includes a hidden pool-size constraint.
	invalid = snapshot.Settings
	invalid.Subscriptions.MaxNodes = snapshot.PoolSize - 1
	_, err = editor.Stage(invalid, snapshot.Revision)
	requireSettingsError(t, err, ErrSettingsInvalid)
	invalid = snapshot.Settings
	invalid.Pool.ProviderDiversity = ProviderDiversity{Enabled: true, MaxPerProvider: snapshot.PoolSize + 1}
	_, err = editor.Stage(invalid, snapshot.Revision)
	requireSettingsError(t, err, ErrSettingsInvalid)
	invalid = snapshot.Settings
	invalid.Benchmark.Speed.Enabled = true
	invalid.Benchmark.Speed.URLTemplate = "https://example.com/no-placeholder"
	_, err = editor.Stage(invalid, snapshot.Revision)
	requireSettingsError(t, err, ErrSettingsInvalid)
	requireSettingsError(t, editor.Validate(snapshot.Settings, "stale"), ErrSettingsConflict)
	_, err = editor.Stage(snapshot.Settings, "stale")
	requireSettingsError(t, err, ErrSettingsConflict)
	if !bytes.Equal(before, readSettingsTest(t, path)) {
		t.Fatal("failed validation modified config")
	}
	if _, err = os.Stat(editor.pending()); !os.IsNotExist(err) {
		t.Fatal("failed validation wrote intent")
	}
}
func TestSettingsInterruptedStageStartupReadsBaselineThenRecovers(t *testing.T) {
	path, editor, snapshot := settingsFixture(t)
	before := readSettingsTest(t, path)
	settings := snapshot.Settings
	settings.Benchmark.FullInterval = Dur(9 * time.Minute)
	tx, err := editor.Stage(settings, snapshot.Revision)
	if err != nil {
		t.Fatal(err)
	}
	candidate := readSettingsTest(t, path)
	cfg, err := StartupConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Benchmark.FullInterval.Duration != 5*time.Minute {
		t.Fatal("startup used uncommitted config")
	}
	if !bytes.Equal(candidate, readSettingsTest(t, path)) {
		t.Fatal("startup performed write before ownership lock")
	}
	restarted := NewSettingsEditor(path)
	if err = restarted.Recover(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readSettingsTest(t, path)) {
		t.Fatal("baseline not restored")
	}
	requireSettingsError(t, tx.Commit(), ErrSettingsConflict)
}
func TestSettingsExplicitRollbackAndIntentBeforeReplacement(t *testing.T) {
	path, editor, snapshot := settingsFixture(t)
	before := readSettingsTest(t, path)
	settings := snapshot.Settings
	settings.Benchmark.FullInterval = Dur(9 * time.Minute)
	tx, err := editor.Stage(settings, snapshot.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{editor.backup(), editor.pending()} {
		st, _ := os.Stat(name)
		if st.Mode().Perm() != 0600 {
			t.Fatal("private sidecar exposed")
		}
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readSettingsTest(t, path)) {
		t.Fatal("rollback lost baseline")
	}
	// Kill between durable intent and replacing the main config leaves baseline.
	if err = writeSettingsAtomic(editor.backup(), before); err != nil {
		t.Fatal(err)
	}
	if err = editor.writeIntent(settingsIntent{Schema: 1, Before: settingsDigest(before), After: settingsDigest([]byte("candidate"))}); err != nil {
		t.Fatal(err)
	}
	if _, err = StartupConfig(path); err != nil {
		t.Fatal(err)
	}
	if err = editor.Recover(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readSettingsTest(t, path)) {
		t.Fatal("recovery changed baseline")
	}
}
func TestSettingsCommittedIntentRecoveryRetainsCandidate(t *testing.T) {
	path, editor, snapshot := settingsFixture(t)
	settings := snapshot.Settings
	settings.Benchmark.FullInterval = Dur(9 * time.Minute)
	tx, err := editor.Stage(settings, snapshot.Revision)
	if err != nil {
		t.Fatal(err)
	}
	// Durable commit happened, process died before sidecar cleanup.
	if err = editor.writeIntent(settingsIntent{Schema: 1, Before: tx.before, After: tx.after, Committed: true}); err != nil {
		t.Fatal(err)
	}
	candidate := readSettingsTest(t, path)
	cfg, err := StartupConfig(path)
	if err != nil || cfg.Benchmark.FullInterval.Duration != 9*time.Minute {
		t.Fatalf("committed startup %v", err)
	}
	if err = NewSettingsEditor(path).Recover(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(candidate, readSettingsTest(t, path)) {
		t.Fatal("committed candidate rolled back")
	}
}
func TestSettingsRecoveryRefusesDriftOrInvalidBackup(t *testing.T) {
	for _, damage := range []string{"config", "backup", "intent", "symlink-backup"} {
		t.Run(damage, func(t *testing.T) {
			path, editor, snapshot := settingsFixture(t)
			settings := snapshot.Settings
			settings.Benchmark.FullInterval = Dur(9 * time.Minute)
			tx, err := editor.Stage(settings, snapshot.Revision)
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "config":
				_ = os.WriteFile(path, append(readSettingsTest(t, path), []byte("# concurrent operator edit\n")...), 0600)
			case "backup":
				_ = os.WriteFile(editor.backup(), []byte("bad baseline"), 0600)
			case "intent":
				_ = os.WriteFile(editor.pending(), []byte(`{"schema":1,"before":"invalid","after":"invalid"}`), 0600)
			case "symlink-backup":
				_ = os.Remove(editor.backup())
				if err = os.Symlink(path, editor.backup()); err != nil {
					t.Fatal(err)
				}
			}
			unchanged := readSettingsTest(t, path)
			_, err = StartupConfig(path)
			requireSettingsError(t, err, ErrSettingsRecovery)
			requireSettingsError(t, editor.Recover(), ErrSettingsRecovery)
			requireSettingsError(t, tx.Rollback(), ErrSettingsRecovery)
			if !bytes.Equal(unchanged, readSettingsTest(t, path)) {
				t.Fatal("recovery overwrote drift")
			}
			if _, err = os.Lstat(editor.pending()); err != nil {
				t.Fatal("lost recovery intent")
			}
		})
	}
}
func TestSettingsConcurrentStageHasOneDurableWriter(t *testing.T) {
	_, editor, snapshot := settingsFixture(t)
	settings := snapshot.Settings
	settings.Benchmark.FullInterval = Dur(9 * time.Minute)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	transactions := make(chan *SettingsTransaction, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := editor.Stage(settings, snapshot.Revision)
			results <- err
			if tx != nil {
				transactions <- tx
			}
		}()
	}
	wg.Wait()
	close(results)
	close(transactions)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			requireSettingsError(t, err, ErrSettingsBusy)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d simultaneous updates", accepted)
	}
	for tx := range transactions {
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestSettingsEditorRefusesPublicAndSymlinkConfig(t *testing.T) {
	path, editor, _ := settingsFixture(t)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := editor.Snapshot()
	requireSettingsError(t, err, ErrSettingsUnavailable)
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), "linked.yaml")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	_, err = NewSettingsEditor(link).Snapshot()
	requireSettingsError(t, err, ErrSettingsUnavailable)
}
