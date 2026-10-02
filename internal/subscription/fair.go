package subscription

import "github.com/jarymor-ux/kee-route-manager/internal/model"

// Apply per-source quotas, deduplicate attribution, then take candidates in
// deterministic configured-source round-robin order before the global cap.
func fairMerge(groups map[string][]model.Node, order []string, max, quota int) []model.Node {
	if max < 1 {
		return nil
	}
	if quota < 1 {
		quota = max
	}
	merged := []model.Node{}
	lists := make(map[string][]model.Node, len(order))
	for _, id := range order {
		lists[id] = Merge(groups[id], quota)
		merged = append(merged, lists[id]...)
	}
	byID := map[string]model.Node{}
	for _, n := range Merge(merged, 0) {
		byID[n.ID] = n
	}
	out := []model.Node{}
	selected := map[string]bool{}
	positions := map[string]int{}
	for len(out) < max {
		progressed := false
		for _, id := range order {
			xs := lists[id]
			for positions[id] < len(xs) {
				n := xs[positions[id]]
				positions[id]++
				progressed = true
				if selected[n.ID] {
					continue
				}
				selected[n.ID] = true
				out = append(out, byID[n.ID])
				break
			}
			if len(out) >= max {
				break
			}
		}
		if !progressed {
			break
		}
	}
	return out
}
