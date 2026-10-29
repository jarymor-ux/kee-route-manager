package core

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
)

var (
	ErrActiveNode                 = errors.New("active node cannot be excluded from automatic selection")
	ErrNodeNotFound               = errors.New("node not found")
	ErrNodeExcluded               = errors.New("enable automatic selection for this node before switching")
	ErrActiveNodeIdentityConflict = errors.New("subscription identity conflicts with the active route; switch nodes or enable its excluded alias")
)

// SetNodeAutoSelect serializes preferences with every routing decision. A
// benchmark may keep measuring its snapshot, but must recheck before applying.
func (m *Manager) SetNodeAutoSelect(ctx context.Context, id string, enabled bool) error {
	changed := false
	err := m.RunAction(ctx, "node-auto-select", "web", func(ctx context.Context) error {
		m.routeMu.Lock()
		defer m.routeMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		// An interrupted transaction can still select its recorded desired node.
		// Do not acknowledge preferences that replay could overwrite.
		if pending, err := m.store.PendingTransaction(); err != nil {
			return err
		} else if pending != nil {
			return operation.ErrBusy
		}
		state := m.store.State()
		if id == "" {
			return ErrNodeNotFound
		}
		node, exists := m.store.Node(id)
		active, _ := m.store.Node(state.ActiveNodeID)
		if !enabled && (id == state.ActiveNodeID || node.SharesPreference(active)) {
			return ErrActiveNode
		}
		if !exists && !(enabled && state.ExcludedNodeIDs[id]) {
			return ErrNodeNotFound
		}
		nodes := m.store.Nodes()
		if err := m.store.Update(func(st *model.State) error {
			migrateLegacyNodePreferences(st, nodes)
			keys := node.PreferenceKeys()
			if len(keys) > 0 {
				if st.NodeAutoSelect == nil {
					st.NodeAutoSelect = map[string]bool{}
				}
				for _, key := range keys {
					st.NodeAutoSelect[key] = enabled
				}
				if enabled || len(st.LegacyNodeSources[id]) == 0 {
					delete(st.ExcludedNodeIDs, id)
					delete(st.LegacyNodeSources, id)
				}
			} else if enabled {
				delete(st.ExcludedNodeIDs, id)
				delete(st.LegacyNodeSources, id)
			} else {
				if st.ExcludedNodeIDs == nil {
					st.ExcludedNodeIDs = map[string]bool{}
				}
				st.ExcludedNodeIDs[id] = true
			}
			return nil
		}); err != nil {
			return err
		}
		after := m.store.State()
		changed = !reflect.DeepEqual(state.NodeAutoSelect, after.NodeAutoSelect) || !reflect.DeepEqual(state.ExcludedNodeIDs, after.ExcludedNodeIDs)
		return nil
	})
	if err == nil && changed {
		m.queueConfigurationBenchmark("node-selection-change")
	}
	return err
}

func autoSelectNodes(nodes []model.Node, state model.State) []model.Node {
	out := make([]model.Node, 0, len(nodes))
	for _, node := range nodes {
		if state.NodeAutoSelectEnabled(node) {
			out = append(out, node)
		}
	}
	return out
}

// Migration only binds identities recoverable from the cache/fetch. Explicit
// allow records take precedence over old fingerprint tombstones.
func migrateLegacyNodePreferences(state *model.State, nodes []model.Node) {
	var active model.Node
	for _, node := range nodes {
		if node.ID == state.ActiveNodeID {
			active = node
			break
		}
	}
	for _, node := range nodes {
		if !state.ExcludedNodeIDs[node.ID] {
			continue
		}
		identities := node.SubscriptionIdentities()
		remaining, recorded := state.LegacyNodeSources[node.ID]
		if !recorded {
			remaining = append([]string(nil), node.Sources...)
			for _, identity := range identities {
				remaining = append(remaining, identity.SourceID)
			}
			remaining = slices.DeleteFunc(remaining, func(source string) bool { return source == "" })
			slices.Sort(remaining)
			remaining = slices.Compact(remaining)
			if len(remaining) > 0 {
				if state.LegacyNodeSources == nil {
					state.LegacyNodeSources = map[string][]string{}
				}
				state.LegacyNodeSources[node.ID] = remaining
			}
		}
		if node.SharesPreference(active) {
			continue
		}
		keys := node.PreferenceKeys()
		if len(keys) == 0 {
			continue
		}
		if state.NodeAutoSelect == nil {
			state.NodeAutoSelect = map[string]bool{}
		}
		for _, key := range keys {
			if _, exists := state.NodeAutoSelect[key]; !exists {
				state.NodeAutoSelect[key] = false
			}
		}
		remaining = slices.DeleteFunc(remaining, func(source string) bool {
			for _, identity := range identities {
				if identity.SourceID == source {
					return true
				}
			}
			return false
		})
		if len(remaining) == 0 {
			delete(state.ExcludedNodeIDs, node.ID)
			delete(state.LegacyNodeSources, node.ID)
		} else {
			state.LegacyNodeSources[node.ID] = remaining
		}
	}
}

// Caller holds routeMu. Bind before using fresh identities and before replacing
// the only legacy attribution cache; update trials never call this method.
func (m *Manager) migrateNodePreferences(fetched []model.Node) error {
	if pending, err := m.store.PendingTransaction(); err != nil {
		return err
	} else if pending != nil {
		return operation.ErrBusy
	}
	nodes := subscription.Merge(append(m.store.Nodes(), fetched...), 0)
	return m.store.Update(func(state *model.State) error {
		migrateLegacyNodePreferences(state, nodes)
		return checkActiveNodeIdentities(*state, fetched)
	})
}

func checkActiveNodeIdentities(state model.State, nodes []model.Node) error {
	for _, node := range nodes {
		if node.ID == state.ActiveNodeID && !state.NodeAutoSelectEnabled(node) {
			return ErrActiveNodeIdentityConflict
		}
	}
	return nil
}

func (m *Manager) nodeAutoSelectEnabled(state model.State, id string) bool {
	node, exists := m.store.Node(id)
	return exists && state.NodeAutoSelectEnabled(node)
}
