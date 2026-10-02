package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

func snapshotAliasFixture(t *testing.T) (*Manager, tunnel.DesiredPool, string) {
	t.Helper()
	c := reproConfig(t)
	c.Xray.BaseRoutingFile = filepath.Join(c.Xray.ConfigDir, "base.json")
	original := pretty(map[string]any{"routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"redirect"}, "outboundTag": "vless-reality"}}}})
	if err := os.WriteFile(c.Xray.BaseRoutingFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(c, &recordingRunner{}, &fakePlatform{})
	snapshot, err := m.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(snapshot) })
	if err = m.preserveOriginal(snapshot); err != nil {
		t.Fatal(err)
	}
	node := testNode()
	slots := []model.Slot{{Index: 0, NodeID: node.ID, Tag: c.Xray.SlotTagPrefix + "0"}}
	desired := tunnel.DesiredPool{Slots: slots, Nodes: map[string]model.Node{node.ID: node}, Selection: tunnel.Selection{Tag: slots[0].Tag}}
	managed, err := BuildManaged(c, slots, desired.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	managed.Outbounds, err = selectOutbound(managed.Outbounds, desired.Selection.Tag)
	if err != nil {
		t.Fatal(err)
	}
	if err = writeManaged(c.Xray.ManagedDir, managed); err != nil {
		t.Fatal(err)
	}
	if err = m.patchBaseRoute(c.Xray.BaseRoutingFile); err != nil {
		t.Fatal(err)
	}
	return m, desired, string(original)
}

func snapshotAliasManager(t *testing.T, m *Manager) *Manager {
	t.Helper()
	alias := filepath.Join(filepath.Dir(m.cfg.Xray.ConfigDir), "config-alias")
	if err := os.Symlink(m.cfg.Xray.ConfigDir, alias); err != nil {
		t.Fatal(err)
	}
	c := m.cfg
	c.Xray.ConfigDir, c.Xray.ManagedDir = alias, alias
	c.Xray.BaseRoutingFile = filepath.Join(alias, filepath.Base(c.Xray.BaseRoutingFile))
	return NewManager(c, m.r, m.p)
}

func TestSnapshotAliasMigrationPreservesReadinessReplayAndRestore(t *testing.T) {
	m, desired, original := snapshotAliasFixture(t)
	ctx := context.Background()
	before, err := m.ActualState(ctx)
	if err != nil || before.Drift != "" {
		t.Fatalf("original fixture drift: %+v %v", before, err)
	}
	poolProof, err := m.PrepareReplay(ctx, desired, false)
	if err != nil {
		t.Fatal(err)
	}
	aliased := snapshotAliasManager(t, m)
	after, err := aliased.ActualState(ctx)
	if err != nil || after.Drift != "" || after.ConfigHash != before.ConfigHash {
		t.Fatalf("equivalent alias changed actual state: %+v %v", after, err)
	}
	if err = aliased.ValidateReplay(ctx, poolProof); err != nil {
		t.Fatalf("equivalent alias invalidated pending replay: %v", err)
	}
	restoreProof, err := aliased.PrepareReplay(ctx, desired, true)
	if err != nil {
		t.Fatalf("equivalent alias prevents restore planning: %v", err)
	}
	if err = aliased.RestoreOriginal(ctx); err != nil {
		t.Fatalf("equivalent alias prevents restore: %v", err)
	}
	if err = aliased.ValidateReplay(ctx, restoreProof); err != nil {
		t.Fatalf("restored absent files invalidated replay: %v", err)
	}
	got, err := os.ReadFile(m.cfg.Xray.BaseRoutingFile)
	if err != nil || string(got) != original {
		t.Fatalf("alias restore changed original routing: %s %v", got, err)
	}
}

func TestRollbackSnapshotAcceptsEquivalentAlias(t *testing.T) {
	m, _, _ := snapshotAliasFixture(t)
	snapshot, err := m.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(snapshot)
	path := filepath.Join(m.cfg.Xray.ManagedDir, "03_90_kee_route_manager_inbounds.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = snapshotAliasManager(t, m).restore(snapshot); err != nil {
		t.Fatalf("equivalent alias prevents rollback: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, before) {
		t.Fatal("rollback did not restore snapshot contents")
	}
}

func TestSnapshotAliasesRejectUnexpectedOrDuplicateTargetsBeforeEffects(t *testing.T) {
	for _, kind := range []string{"outside", "duplicate", "retargeted-alias"} {
		t.Run(kind, func(t *testing.T) {
			m, desired, _ := snapshotAliasFixture(t)
			original := filepath.Join(m.cfg.Paths.StateDir, "xray-original")
			data, err := os.ReadFile(filepath.Join(original, "paths.json"))
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			if err = json.Unmarshal(data, &paths); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			if kind == "outside" {
				paths[len(paths)-1] = filepath.Join(outside, "base.json")
			} else {
				alias := filepath.Join(filepath.Dir(m.cfg.Xray.ConfigDir), "recorded-alias")
				target := m.cfg.Xray.ConfigDir
				if kind == "retargeted-alias" {
					target = outside
				}
				if err = os.Symlink(target, alias); err != nil {
					t.Fatal(err)
				}
				if kind == "duplicate" {
					paths[len(paths)-1] = filepath.Join(alias, filepath.Base(paths[0]))
				} else {
					paths[len(paths)-1] = filepath.Join(alias, "base.json")
				}
			}
			if err = os.WriteFile(filepath.Join(original, "paths.json"), pretty(paths), 0600); err != nil {
				t.Fatal(err)
			}
			before := map[string][]byte{}
			for _, path := range m.snapshotPaths() {
				before[path], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err = m.PrepareReplay(context.Background(), desired, true); err == nil {
				t.Fatal("unexpected snapshot target accepted in restore planning")
			}
			for _, restore := range []func(string) error{m.restore, m.reverseRestore} {
				if err = restore(original); err == nil {
					t.Fatal("unexpected snapshot target accepted in restore")
				}
				for path, data := range before {
					got, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(got, data) {
						t.Fatalf("rejected snapshot changed %s: %v", path, err)
					}
				}
			}
		})
	}
}

func TestReplayAliasesRejectDuplicateAndChangedFileIdentities(t *testing.T) {
	m, desired, _ := snapshotAliasFixture(t)
	proof, err := m.PrepareReplay(context.Background(), desired, false)
	if err != nil {
		t.Fatal(err)
	}
	aliased := snapshotAliasManager(t, m)
	paths := m.snapshotPaths()
	duplicate := tunnel.ReplayProof{}
	for path, file := range proof {
		duplicate[path] = file
	}
	delete(duplicate, paths[len(paths)-1])
	duplicate[filepath.Join(aliased.cfg.Xray.ManagedDir, filepath.Base(paths[0]))] = proof[paths[0]]
	if err = aliased.ValidateReplay(context.Background(), duplicate); err == nil {
		t.Fatal("duplicate physical replay target accepted")
	}
	if err = os.WriteFile(paths[0], []byte("operator edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = aliased.ValidateReplay(context.Background(), proof); err == nil {
		t.Fatal("directory alias bypassed file hash drift protection")
	}
}
