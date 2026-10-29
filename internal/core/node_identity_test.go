package core

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/store"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
)

func namedPreferenceNode(id, source, name string) model.Node {
	return model.Node{ID: id, Label: name, Sources: []string{source}}
}

func installPreferenceNodes(t *testing.T, m *Manager, nodes []model.Node) {
	t.Helper()
	if err := m.store.ReplaceNodes(nodes); err != nil {
		t.Fatal(err)
	}
}

func preferenceEnabled(t *testing.T, m *Manager, id string) bool {
	t.Helper()
	for _, view := range m.Nodes() {
		if view.ID == id {
			return view.AutoSelectEnabled
		}
	}
	t.Fatalf("node %q is not visible", id)
	return false
}

func TestNodePreferenceSurvivesEndpointRotationDisappearanceAndRestore(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "provider", "Active")
	old := namedPreferenceNode("fallback", "provider", "Amsterdam")
	installPreferenceNodes(t, m, []model.Node{active, old})
	if err := m.SetNodeAutoSelect(context.Background(), old.ID, false); err != nil {
		t.Fatal(err)
	}
	// An unchanged healthy active route permits the disappearing reserve to leave cache.
	m.fetcher = fakeFetcher{nodes: []model.Node{active}}
	m.bench = &eligibilityBenchmark{}
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.store.Node(old.ID); ok {
		t.Fatal("test did not remove vanished reserve from cache")
	}
	if err := m.RestoreOriginalXray(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(m.store.Path(""), m.store.CachePath(""), model.NewState("test", m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	m.store = st
	rotated := old
	rotated.ID = "new-connection"
	rotated.Address = "203.0.113.8"
	rotated.Port = 8443
	m.fetcher = fakeFetcher{nodes: []model.Node{active, rotated}}
	runner := &eligibilityBenchmark{}
	m.bench = runner
	if err = m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.tested, []string{"active"}) || preferenceEnabled(t, m, rotated.ID) {
		t.Fatal("rotated returning node lost exclusion")
	}
	if err = m.SetNodeAutoSelect(context.Background(), rotated.ID, true); err != nil {
		t.Fatal(err)
	}
	if !preferenceEnabled(t, m, rotated.ID) {
		t.Fatal("rotated node could not be re-enabled")
	}
	// The old endpoint returning after re-enable must not resurrect a stale ID ban.
	installPreferenceNodes(t, m, []model.Node{active, old})
	if !preferenceEnabled(t, m, old.ID) {
		t.Fatal("old endpoint resurrected an exclusion after re-enable")
	}
}

func TestNodePreferenceIsScopedToSubscriptionAndRetainsAliases(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "first", "Active")
	a := namedPreferenceNode("fallback", "first", "Amsterdam")
	alias := namedPreferenceNode("fallback", "second", "NL premium")
	merged := subscription.Merge([]model.Node{a, alias}, 0)[0]
	installPreferenceNodes(t, m, []model.Node{active, merged})
	if err := m.SetNodeAutoSelect(context.Background(), merged.ID, false); err != nil {
		t.Fatal(err)
	}
	rotated := namedPreferenceNode("rotated", "second", "NL premium")
	unrelated := namedPreferenceNode("other-provider", "third", "Amsterdam")
	installPreferenceNodes(t, m, []model.Node{active, rotated, unrelated})
	if preferenceEnabled(t, m, rotated.ID) {
		t.Fatal("second subscription alias lost exclusion")
	}
	if !preferenceEnabled(t, m, unrelated.ID) {
		t.Fatal("same name in another subscription was excluded")
	}
}

func TestNodePreferenceMigratesLegacyBeforeTestingFreshEndpoint(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "provider", "Active")
	old := namedPreferenceNode("fallback", "provider", "Amsterdam")
	installPreferenceNodes(t, m, []model.Node{active, old})
	if err := m.store.Update(func(st *model.State) error { st.ExcludedNodeIDs = map[string]bool{"fallback": true}; return nil }); err != nil {
		t.Fatal(err)
	}
	rotated := old
	rotated.ID = "rotated"
	rotated.Port = 8443
	m.fetcher = fakeFetcher{nodes: []model.Node{active, rotated}}
	runner := &eligibilityBenchmark{}
	m.bench = runner
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.tested, []string{"active"}) || preferenceEnabled(t, m, rotated.ID) {
		t.Fatal("legacy exclusion was not bound before testing new endpoint")
	}
	if m.State().ExcludedNodeIDs[old.ID] {
		t.Fatal("converted fingerprint exclusion was left behind")
	}
	if err := m.SetNodeAutoSelect(context.Background(), rotated.ID, true); err != nil {
		t.Fatal(err)
	}
	installPreferenceNodes(t, m, []model.Node{active, old})
	if !preferenceEnabled(t, m, old.ID) {
		t.Fatal("cleared legacy exclusion returned with old endpoint")
	}
}

func TestNodePreferenceExplicitAllowWinsOverLateLegacyMigration(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "provider", "Active")
	current := namedPreferenceNode("fallback", "provider", "Amsterdam")
	installPreferenceNodes(t, m, []model.Node{active, current})
	if err := m.store.Update(func(st *model.State) error { st.ExcludedNodeIDs = map[string]bool{"old-unknown": true}; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := m.SetNodeAutoSelect(context.Background(), current.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SetNodeAutoSelect(context.Background(), current.ID, true); err != nil {
		t.Fatal(err)
	}
	returned := current
	returned.ID = "old-unknown"
	m.fetcher = fakeFetcher{nodes: []model.Node{active, returned}}
	runner := &eligibilityBenchmark{}
	m.bench = runner
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.tested, []string{"active", "old-unknown"}) || !preferenceEnabled(t, m, returned.ID) {
		t.Fatal("late legacy ID overrode explicit re-enable")
	}
}

func TestNodePreferenceCannotExcludeAnActiveNameGroup(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "provider", "Same name")
	other := namedPreferenceNode("fallback", "provider", "Same name")
	installPreferenceNodes(t, m, []model.Node{active, other})
	before := m.State()
	if err := m.SetNodeAutoSelect(context.Background(), other.ID, false); err == nil {
		t.Fatal("a different endpoint excluded the active logical node")
	}
	if !reflect.DeepEqual(before, m.State()) {
		t.Fatal("rejected active-group exclusion changed state")
	}
	for _, view := range m.Nodes() {
		if !view.AutoSelectLocked {
			t.Fatal("active name group was not locked in panel view")
		}
	}
}

func TestNodePreferenceStatusProjectsRotatedPoolEligibility(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "provider", "Active")
	old := namedPreferenceNode("fallback", "provider", "Amsterdam")
	installPreferenceNodes(t, m, []model.Node{active, old})
	if err := m.SetNodeAutoSelect(context.Background(), old.ID, false); err != nil {
		t.Fatal(err)
	}
	rotated := old
	rotated.ID = "rotated"
	installPreferenceNodes(t, m, []model.Node{active, rotated})
	if err := m.store.Update(func(st *model.State) error { st.Pool[1].NodeID = rotated.ID; return nil }); err != nil {
		t.Fatal(err)
	}
	before := m.store.State()
	status := m.Status(context.Background())
	if !status.State.ExcludedNodeIDs[rotated.ID] {
		t.Fatal("status offered an excluded rotated slot for manual switching")
	}
	if !reflect.DeepEqual(before, m.store.State()) {
		t.Fatal("status polling mutated persistent preferences")
	}
	if err := m.SwitchSlot(context.Background(), 1); err == nil {
		t.Fatal("rotated excluded pool slot was manually selected")
	}
}

func TestNodePreferenceMigratesEvenWhenRefreshIsEmpty(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "provider", "Active")
	old := namedPreferenceNode("fallback", "provider", "Amsterdam")
	installPreferenceNodes(t, m, []model.Node{active, old})
	if err := m.store.Update(func(st *model.State) error { st.ExcludedNodeIDs = map[string]bool{old.ID: true}; return nil }); err != nil {
		t.Fatal(err)
	}
	m.fetcher = fakeFetcher{}
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err == nil {
		t.Fatal("expected empty subscription error")
	}
	rotated := old
	rotated.ID = "rotated"
	if m.State().NodeAutoSelectEnabled(rotated) || m.State().ExcludedNodeIDs[old.ID] {
		t.Fatal("empty refresh left exclusion tied only to old endpoint")
	}
}

func TestNodePreferenceAmbiguousLegacyCacheDoesNotInventSourceNames(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "first", "Active")
	old := model.Node{ID: "fallback", Label: "Alpha", Sources: []string{"first", "second"}}
	installPreferenceNodes(t, m, []model.Node{active, old})
	if err := m.store.Update(func(st *model.State) error { st.ExcludedNodeIDs = map[string]bool{old.ID: true}; return nil }); err != nil {
		t.Fatal(err)
	}
	other := namedPreferenceNode("unrelated", "second", "Alpha")
	m.fetcher = fakeFetcher{nodes: []model.Node{active, other}}
	m.bench = &eligibilityBenchmark{}
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if !preferenceEnabled(t, m, other.ID) || !m.State().ExcludedNodeIDs[old.ID] {
		t.Fatal("ambiguous cached source attribution was guessed")
	}
}

func TestNodePreferenceRefreshCannotAttachExcludedAliasToActiveCache(t *testing.T) {
	m, tun, _ := fixture(t)
	active := namedPreferenceNode("active", "first", "Active")
	other := namedPreferenceNode("fallback", "second", "Excluded")
	installPreferenceNodes(t, m, []model.Node{active, other})
	if err := m.SetNodeAutoSelect(context.Background(), other.ID, false); err != nil {
		t.Fatal(err)
	}
	alias := other
	alias.ID = active.ID
	fresh := subscription.Merge([]model.Node{active, alias}, 0)
	before, nodes := m.State(), m.store.Nodes()
	m.fetcher = fakeFetcher{nodes: fresh}
	runner := &eligibilityBenchmark{}
	m.bench = runner
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err == nil {
		t.Fatal("active alias conflict should refuse refresh")
	}
	if !reflect.DeepEqual(before, m.State()) || !reflect.DeepEqual(nodes, m.store.Nodes()) || len(runner.tested) != 0 || len(tun.selected) != 0 {
		t.Fatal("active alias conflict changed route/cache or started probes")
	}
	st, err := store.New(m.store.Path(""), m.store.CachePath(""), model.NewState("test", m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size))
	if err != nil || st.State().ActiveNodeID != "active" || st.State().NodeAutoSelectEnabled(other) {
		t.Fatal("active alias conflict left an invalid restart state or cleared exclusion")
	}
}

func TestNodePreferenceSourceMembershipIncludesExactNames(t *testing.T) {
	a := namedPreferenceNode("same-connection", "provider", "Old name")
	b := a
	b.Label = "New name"
	if !nodeSetChanged([]model.Node{a}, []model.Node{b}) {
		t.Fatal("name-only subscription change was ignored")
	}
	if nodeSetChanged([]model.Node{a}, subscription.Merge([]model.Node{a}, 0)) {
		t.Fatal("canonical aliases caused repeated source refresh")
	}
}

func TestNodePreferenceRechecksActiveAliasAfterConcurrentManualSwitch(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "first", "Active")
	reserve := namedPreferenceNode("fallback", "first", "Reserve")
	excluded := namedPreferenceNode("excluded", "second", "Excluded")
	installPreferenceNodes(t, m, []model.Node{active, reserve, excluded})
	if err := m.SetNodeAutoSelect(context.Background(), excluded.ID, false); err != nil {
		t.Fatal(err)
	}
	alias := excluded
	alias.ID = reserve.ID
	m.fetcher = fakeFetcher{nodes: subscription.Merge([]model.Node{active, reserve, alias}, 0)}
	started, release := make(chan struct{}), make(chan struct{})
	m.bench = fakeBenchmark{start: started, release: release}
	done := make(chan error, 1)
	go func() { done <- m.RunBenchmark(context.Background(), "manual", "test") }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("benchmark did not start")
	}
	switchErr := m.SwitchSlot(context.Background(), 1)
	before, nodes := m.State(), m.store.Nodes()
	close(release)
	runErr := <-done
	if switchErr != nil {
		t.Fatal(switchErr)
	}
	if runErr == nil {
		t.Fatal("newly active alias conflict was not rejected")
	}
	if !reflect.DeepEqual(before, m.State()) || !reflect.DeepEqual(nodes, m.store.Nodes()) {
		t.Fatal("benchmark replaced the newly active node's cache with excluded aliases")
	}
	st, err := store.New(m.store.Path(""), m.store.CachePath(""), model.NewState("test", m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size))
	if err != nil || st.State().ActiveNodeID != "fallback" {
		t.Fatal("concurrent activation left an invalid restart state")
	}
}

func TestNodePreferencePartialLegacyBindingSurvivesCacheReplacement(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "first", "Active")
	old := model.Node{ID: "fallback", Label: "Alpha", Sources: []string{"first", "second"}}
	installPreferenceNodes(t, m, []model.Node{active, old})
	if err := m.store.Update(func(st *model.State) error { st.ExcludedNodeIDs = map[string]bool{old.ID: true}; return nil }); err != nil {
		t.Fatal(err)
	}
	first := namedPreferenceNode(old.ID, "first", "Alpha")
	m.fetcher = fakeFetcher{nodes: subscription.Merge([]model.Node{active, first}, 0)}
	m.bench = &eligibilityBenchmark{}
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if !m.State().ExcludedNodeIDs[old.ID] {
		t.Fatal("partially recovered legacy attribution discarded its fingerprint ban")
	}
	// Drop its cache entirely, then restart before the missing source returns.
	m.fetcher = fakeFetcher{nodes: []model.Node{active}}
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(m.store.Path(""), m.store.CachePath(""), model.NewState("test", m.cfg.Xray.SlotTagPrefix, m.cfg.Pool.Size))
	if err != nil {
		t.Fatal(err)
	}
	m.store = st
	second := namedPreferenceNode(old.ID, "second", "Beta")
	m.fetcher = fakeFetcher{nodes: subscription.Merge([]model.Node{active, second}, 0)}
	runner := &eligibilityBenchmark{}
	m.bench = runner
	if err = m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.tested, []string{"active"}) || preferenceEnabled(t, m, old.ID) {
		t.Fatal("return through missing source lost legacy exclusion")
	}
	if m.State().ExcludedNodeIDs[old.ID] {
		t.Fatal("fully resolved legacy attribution was not converted")
	}
}

func TestNodePreferenceExplicitAllowOverridesUnresolvedLegacySource(t *testing.T) {
	m, _, _ := fixture(t)
	active := namedPreferenceNode("active", "first", "Active")
	old := model.Node{ID: "fallback", Label: "Alpha", Sources: []string{"first", "second"}}
	installPreferenceNodes(t, m, []model.Node{active, old})
	if err := m.store.Update(func(st *model.State) error { st.ExcludedNodeIDs = map[string]bool{old.ID: true}; return nil }); err != nil {
		t.Fatal(err)
	}
	first := namedPreferenceNode(old.ID, "first", "Alpha")
	m.fetcher = fakeFetcher{nodes: subscription.Merge([]model.Node{active, first}, 0)}
	m.bench = &eligibilityBenchmark{}
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	rotated := first
	rotated.ID = "rotated"
	m.fetcher = fakeFetcher{nodes: []model.Node{active, rotated}}
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetNodeAutoSelect(context.Background(), rotated.ID, true); err != nil {
		t.Fatal(err)
	}
	m.fetcher = fakeFetcher{nodes: []model.Node{active, first}}
	runner := &eligibilityBenchmark{}
	m.bench = runner
	if err := m.RunBenchmark(context.Background(), "manual", "test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.tested, []string{"active", "fallback"}) {
		t.Fatal("unresolved source resurrected the old fingerprint ban after explicit allow")
	}
	if err := m.SwitchSlot(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if m.Status(context.Background()).State.ExcludedNodeIDs[old.ID] {
		t.Fatal("status projects an explicitly allowed active endpoint as excluded")
	}
}
