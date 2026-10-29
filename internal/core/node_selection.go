package core

import (
	"context"
	"errors"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
)

var (
	ErrActiveNode   = errors.New("active node cannot be excluded from automatic selection")
	ErrNodeNotFound = errors.New("node not found")
	ErrNodeExcluded = errors.New("enable automatic selection for this node before switching")
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
		if !enabled && id == state.ActiveNodeID {
			return ErrActiveNode
		}
		if _, exists := m.store.Node(id); !exists && !(enabled && state.ExcludedNodeIDs[id]) {
			return ErrNodeNotFound
		}
		if enabled == !state.ExcludedNodeIDs[id] {
			return nil
		}
		if err := m.store.Update(func(st *model.State) error {
			if enabled {
				delete(st.ExcludedNodeIDs, id)
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
		changed = true
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
		if !state.ExcludedNodeIDs[node.ID] {
			out = append(out, node)
		}
	}
	return out
}
