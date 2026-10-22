package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// routeCandidate is a read-only proposal. The installer must still run its
// existing strict Xray validation and preflight before adopting routing.
type routeCandidate struct {
	File        string
	InboundTags []string
	OutboundTag string
}

type discoveryRule struct {
	Type        string          `json:"type"`
	InboundTag  json.RawMessage `json:"inboundTag"`
	OutboundTag string          `json:"outboundTag"`
	BalancerTag string          `json:"balancerTag"`
}

type discoveryDocument struct {
	Inbounds []struct {
		Tag string `json:"tag"`
	} `json:"inbounds"`
	Outbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	} `json:"outbounds"`
	Routing *struct {
		Rules []json.RawMessage `json:"rules"`
	} `json:"routing"`
}

// discoverRouting scans only local regular strict-JSON files. It does not
// execute Xray, modify configuration, or inspect/print subscription secrets.
// Only unconditioned rules targeting an explicitly defined VPN outbound can
// be selected automatically. Ambiguous targets are returned for user choice.
func discoverRouting(dir string) ([]routeCandidate, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		// Xray provisioning can happen later; retain the manual wizard flow.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot inspect Xray config directory: %w", err)
	}

	outboundVPN := map[string]bool{}
	inbounds := map[string]bool{}
	var routingFile string
	var routingRules []json.RawMessage
	var total int64
	files := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// Symlinks and non-regular files need deliberate operator review.
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("non-regular Xray config entry: %s", entry.Name())
		}
		files++
		if files > 128 {
			return nil, fmt.Errorf("too many xray JSON configuration files")
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil || info.Size() > 4<<20 {
			return nil, fmt.Errorf("xray config file cannot be inspected or exceeds 4 MiB")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("xray configuration cannot be read: %w", err)
		}
		b, readErr := io.ReadAll(io.LimitReader(f, (4<<20)+1))
		f.Close()
		if readErr != nil || len(b) > 4<<20 {
			return nil, fmt.Errorf("xray configuration is unreadable or exceeds 4 MiB")
		}
		total += int64(len(b))
		if total > 16<<20 {
			return nil, fmt.Errorf("xray configuration exceeds 16 MiB total")
		}
		var doc discoveryDocument
		dec := json.NewDecoder(bytes.NewReader(b))
		if err := dec.Decode(&doc); err != nil {
			return nil, fmt.Errorf("xray configuration must be strict JSON")
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("xray configuration has trailing JSON")
		}
		for _, inbound := range doc.Inbounds {
			if inbound.Tag != "" {
				inbounds[inbound.Tag] = true
			}
		}
		for _, outbound := range doc.Outbounds {
			switch outbound.Protocol {
			case "vless", "vmess", "trojan", "shadowsocks":
				if outbound.Tag != "" {
					outboundVPN[outbound.Tag] = true
				}
			}
		}
		if doc.Routing != nil {
			if routingFile != "" {
				return nil, fmt.Errorf("multiple Xray routing sections: explicit review required")
			}
			routingFile = path
			routingRules = doc.Routing.Rules
		}
	}
	if routingFile == "" {
		return nil, nil
	}

	byOutbound := map[string]map[string]bool{}
	unsafeOutbound := map[string]bool{}
	for _, raw := range routingRules {
		var rule discoveryRule
		if err := json.Unmarshal(raw, &rule); err != nil {
			return nil, fmt.Errorf("invalid Xray routing rule")
		}
		if !outboundVPN[rule.OutboundTag] || rule.BalancerTag != "" {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, fmt.Errorf("invalid Xray routing fields")
		}
		// Extra match clauses mean the rule may be an exception rather than
		// the default traffic route. Never silently adopt such policies.
		for key := range fields {
			switch key {
			case "type", "inboundTag", "outboundTag":
			default:
				unsafeOutbound[rule.OutboundTag] = true
			}
		}
		if rule.Type != "field" {
			unsafeOutbound[rule.OutboundTag] = true
			continue
		}
		var tags []string
		if err := json.Unmarshal(rule.InboundTag, &tags); err != nil || len(tags) == 0 {
			unsafeOutbound[rule.OutboundTag] = true
			continue
		}
		for _, tag := range tags {
			if !inbounds[tag] || strings.HasPrefix(tag, "krm-") {
				unsafeOutbound[rule.OutboundTag] = true
			}
		}
		if byOutbound[rule.OutboundTag] == nil {
			byOutbound[rule.OutboundTag] = map[string]bool{}
		}
		for _, tag := range tags {
			byOutbound[rule.OutboundTag][tag] = true
		}
	}

	candidates := make([]routeCandidate, 0, len(byOutbound))
	for outbound, tags := range byOutbound {
		if unsafeOutbound[outbound] {
			continue
		}
		inboundTags := make([]string, 0, len(tags))
		for tag := range tags {
			inboundTags = append(inboundTags, tag)
		}
		sort.Strings(inboundTags)
		candidates = append(candidates, routeCandidate{File: routingFile, InboundTags: inboundTags, OutboundTag: outbound})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].OutboundTag < candidates[j].OutboundTag })
	return candidates, nil
}
