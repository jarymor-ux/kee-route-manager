package core

import (
	"context"
	"fmt"
	"sort"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/platform"
	"github.com/jarymor-ux/kee-route-manager/internal/subscription"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

func (m *Manager) selectPool(nodes []model.Node, results []model.Measurement) []model.Node {
	byID := map[string]model.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	candidates := []model.Node{}
	for _, r := range results {
		if r.Healthy {
			if n, ok := byID[r.NodeID]; ok {
				candidates = append(candidates, n)
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return scoreOf(results, candidates[i].ID) < scoreOf(results, candidates[j].ID) })
	if !m.cfg.Pool.ProviderDiversity.Enabled {
		return take(candidates, m.cfg.Pool.Size)
	}
	out := []model.Node{}
	counts := map[string]int{}
	used := map[string]bool{}
	max := m.cfg.Pool.ProviderDiversity.MaxPerProvider
	if max < 1 {
		max = 1
	}
	for _, n := range candidates {
		allowed := true
		for _, source := range n.Sources {
			if counts[source] >= max {
				allowed = false
			}
		}
		if !allowed {
			continue
		}
		out = append(out, n)
		used[n.ID] = true
		for _, source := range n.Sources {
			counts[source]++
		}
		if len(out) >= m.cfg.Pool.Size {
			return out
		}
	}
	for _, n := range candidates {
		if used[n.ID] {
			continue
		}
		out = append(out, n)
		if len(out) >= m.cfg.Pool.Size {
			break
		}
	}
	return out
}
func assignSlots(old []model.Slot, selected []model.Node, measurements map[string]model.Measurement, prefix string, size int) []model.Slot {
	out := make([]model.Slot, size)
	selectedMap := map[string]model.Node{}
	for _, n := range selected {
		selectedMap[n.ID] = n
	}
	used := map[string]bool{}
	for i := 0; i < size; i++ {
		out[i] = model.Slot{Index: i, Tag: fmt.Sprintf("%s%d", prefix, i)}
	}
	for _, s := range old {
		if s.Index >= 0 && s.Index < size {
			if n, ok := selectedMap[s.NodeID]; ok {
				mm := measurements[n.ID]
				out[s.Index] = model.Slot{Index: s.Index, Tag: fmt.Sprintf("%s%d", prefix, s.Index), NodeID: n.ID, Label: n.Label, Sources: n.Sources, Healthy: mm.Healthy, LastVerifiedAt: mm.CheckedAt, Score: mm.Score}
				// A retained node may be absent from the current subscription. Its
				// historical score must not erase newer live health verification.
				if s.LastVerifiedAt.After(mm.CheckedAt) {
					out[s.Index].Healthy = s.Healthy
					out[s.Index].LastVerifiedAt = s.LastVerifiedAt
				}
				used[n.ID] = true
			}
		}
	}
	next := 0
	for _, n := range selected {
		if used[n.ID] {
			continue
		}
		for next < size && out[next].NodeID != "" {
			next++
		}
		if next >= size {
			break
		}
		mm := measurements[n.ID]
		out[next] = model.Slot{Index: next, Tag: fmt.Sprintf("%s%d", prefix, next), NodeID: n.ID, Label: n.Label, Sources: n.Sources, Healthy: mm.Healthy, LastVerifiedAt: mm.CheckedAt, Score: mm.Score}
		used[n.ID] = true
	}
	return out
}
func firstHealthy(slots []model.Slot) int {
	best := -1
	for i, s := range slots {
		if s.NodeID == "" || !s.Healthy {
			continue
		}
		if best < 0 || s.Score < slots[best].Score {
			best = i
		}
	}
	return best
}
func improvement(current, next float64) float64 {
	if current <= 0 || next <= 0 || next >= current {
		return 0
	}
	return 100 * (current - next) / current
}
func scoreOf(rs []model.Measurement, id string) float64 {
	for _, r := range rs {
		if r.NodeID == id {
			return r.Score
		}
	}
	return 999999
}
func take(xs []model.Node, n int) []model.Node {
	if len(xs) > n {
		return append([]model.Node(nil), xs[:n]...)
	}
	return append([]model.Node(nil), xs...)
}
func nodeSetChanged(a, b []model.Node) bool {
	if len(a) != len(b) {
		return true
	}
	set := map[string]bool{}
	for _, n := range a {
		set[n.ID] = true
	}
	for _, n := range b {
		if !set[n.ID] {
			return true
		}
	}
	return false
}
func trimResults(xs []model.Measurement, n int) []model.Measurement {
	if len(xs) > n {
		return append([]model.Measurement(nil), xs[:n]...)
	}
	return append([]model.Measurement(nil), xs...)
}
func (m *Manager) reconcileSelection(ctx context.Context, state model.State) error {
	if !state.XrayConfigured {
		return nil
	}
	actual, err := m.xray.ActualState(ctx)
	if err != nil {
		return err
	}
	if !actual.Configured || actual.Drift != "" || (state.XrayConfigHash != "" && state.XrayConfigHash != actual.ConfigHash) {
		return fmt.Errorf("managed tunnel drift requires reconciliation")
	}

	if err := m.xray.Ready(ctx); err != nil {
		return err
	}
	if state.DirectMode && m.platform.Capabilities().DirectBypass {
		return m.platform.EnterDirectBypass(ctx)
	}
	if state.DirectMode {
		return m.xray.EnterDirect(ctx)
	}
	if state.ActiveSlot >= 0 && state.ActiveSlot < len(state.Pool) {
		slot := state.Pool[state.ActiveSlot]
		if slot.NodeID != "" && (state.ActiveNodeID == "" || slot.NodeID == state.ActiveNodeID) {
			return m.xray.Select(ctx, tunnel.Selection{Tag: slot.Tag})
		}
	}
	if index := slotIndexForNode(state.Pool, state.ActiveNodeID); index >= 0 {
		return m.xray.Select(ctx, tunnel.Selection{Tag: state.Pool[index].Tag})
	}
	return fmt.Errorf("active VPN selection is not present in the hot pool")
}
func retainActiveNode(selected []model.Node, activeID string, all map[string]model.Node, size int) []model.Node {
	if activeID == "" || size < 1 {
		return take(selected, size)
	}
	for _, node := range selected {
		if node.ID == activeID {
			return take(selected, size)
		}
	}
	active, ok := all[activeID]
	if !ok {
		return take(selected, size)
	}
	out := append([]model.Node(nil), selected...)
	if len(out) >= size {
		out = out[:size-1]
	}
	out = append(out, active)
	return out
}
func slotIndexForNode(slots []model.Slot, nodeID string) int {
	if nodeID == "" {
		return -1
	}
	for i, slot := range slots {
		if slot.NodeID == nodeID {
			return i
		}
	}
	return -1
}
func retainedNodeList(fetched []model.Node, slots []model.Slot, all map[string]model.Node) []model.Node {
	byID := make(map[string]model.Node, len(fetched)+len(slots))
	for _, node := range fetched {
		byID[node.ID] = node
	}
	for _, slot := range slots {
		if slot.NodeID == "" {
			continue
		}
		if node, ok := all[slot.NodeID]; ok {
			byID[node.ID] = node
		}
	}
	out := make([]model.Node, 0, len(byID))
	for _, node := range byID {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
