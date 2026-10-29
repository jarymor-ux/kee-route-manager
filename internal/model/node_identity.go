package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
)

// NodeIdentity identifies the subscription entry, independently of its endpoint.
type NodeIdentity struct {
	SourceID string `json:"source_id"`
	Name     string `json:"name"`
}

// SubscriptionIdentities keeps exact attribution through connection deduplication.
// Old multi-source caches have only one display label and cannot recover which
// source supplied that label. Retain their fingerprint fallback until a fetch
// provides the actual identities instead of inventing cross-source pairs.
func (n Node) SubscriptionIdentities() []NodeIdentity {
	identities := append([]NodeIdentity(nil), n.SubscriptionNodes...)
	if len(identities) == 0 && len(n.Sources) == 1 && n.Label != "" {
		identities = append(identities, NodeIdentity{SourceID: n.Sources[0], Name: n.Label})
	}
	identities = slices.DeleteFunc(identities, func(id NodeIdentity) bool { return id.SourceID == "" || id.Name == "" })
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].SourceID != identities[j].SourceID {
			return identities[i].SourceID < identities[j].SourceID
		}
		return identities[i].Name < identities[j].Name
	})
	return slices.Compact(identities)
}

func (n Node) PreferenceKeys() []string {
	var keys []string
	for _, identity := range n.SubscriptionIdentities() {
		encoded, _ := json.Marshal(identity)
		digest := sha256.Sum256(encoded)
		keys = append(keys, hex.EncodeToString(digest[:]))
	}
	return keys
}

func (s State) NodeAutoSelectEnabled(n Node) bool {
	keys := n.PreferenceKeys()
	explicitAllow := len(keys) > 0
	for _, key := range keys {
		enabled, exists := s.NodeAutoSelect[key]
		if exists && !enabled {
			return false
		}
		if !exists {
			explicitAllow = false
		}
	}
	return !s.ExcludedNodeIDs[n.ID] || explicitAllow
}

func (n Node) SharesPreference(other Node) bool {
	keys := n.PreferenceKeys()
	for _, key := range other.PreferenceKeys() {
		if slices.Contains(keys, key) {
			return true
		}
	}
	return false
}
