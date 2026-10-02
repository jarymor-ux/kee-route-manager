package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Explicit priority distinguishes our full rule from rules sharing mark/table.
const managedRulePriority = 10000

func policyNumber(v any) (uint64, bool) {
	if n, ok := v.(float64); ok {
		if n < 0 || n > 0xffffffff || float64(uint64(n)) != n {
			return 0, false
		}
		return uint64(n), true
	}
	s := fmt.Sprint(v)
	base := 10
	if strings.HasPrefix(s, "0x") {
		base = 16
		s = strings.TrimPrefix(s, "0x")
	}
	n, err := strconv.ParseUint(s, base, 32)
	return n, err == nil
}
func policyNumberEquals(v any, want int) bool {
	n, ok := policyNumber(v)
	return ok && n == uint64(want)
}

func managedPolicyIdentity(rule map[string]any, c firewallConfig, priority int) bool {
	if priority <= 0 || !policyNumberEquals(rule["priority"], priority) || !policyNumberEquals(rule["table"], c.Table) || !policyNumberEquals(rule["fwmark"], c.Mark) {
		return false
	}
	// Whitelist only equivalent default selectors. Unknown fields fail closed:
	// future iproute2 selectors must never turn into implicit owned rule adoption.
	for key, value := range rule {
		switch key {
		case "priority", "table", "fwmark":
		case "fwmask":
			if n, ok := policyNumber(value); !ok || n != 0xffffffff {
				return false
			}
		case "src", "dst":
			s := stringValue(value)
			if s != "all" && s != "0.0.0.0/0" && s != "0.0.0.0" {
				return false
			}
			// iproute2 omits a host-length prefix, so a bare address must
			// explicitly carry length zero before it is equivalent to all.
			if s == "0.0.0.0" && !policyNumberEquals(rule[key+"len"], 0) {
				return false
			}
		case "srclen", "dstlen":
			if !policyNumberEquals(value, 0) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func managedPolicyCommand(action string, c firewallConfig, priority int) []string {
	return []string{"ip", "rule", action, "priority", strconv.Itoa(priority), "from", "all", "to", "all", "fwmark", strconv.Itoa(c.Mark) + "/0xffffffff", "table", strconv.Itoa(c.Table)}
}

// Shared preflight must finish before installation or cleanup mutates anything.
func (g *generic) checkPolicyResources(ctx context.Context, c firewallConfig, owner firewallOwnership, owned bool) (bool, bool, error) {
	rules, err := g.r.Run(ctx, []string{"ip", "-j", "rule", "show"})
	if err != nil {
		return false, false, err
	}
	var list []map[string]any
	if err = json.Unmarshal(rules, &list); err != nil {
		return false, false, fmt.Errorf("decode policy rules: %w", err)
	}
	policyExists := false
	for _, rule := range list {
		if policyNumberEquals(rule["table"], c.Table) || policyNumberEquals(rule["fwmark"], c.Mark) || policyNumberEquals(rule["priority"], managedRulePriority) {
			if !owned || owner.Priority != managedRulePriority || !managedPolicyIdentity(rule, c, owner.Priority) || policyExists {
				return false, false, fmt.Errorf("managed policy rule identity conflicts with existing rule; legacy ownership requires explicit operator recovery")
			}
			policyExists = true
		}
	}
	routes, err := g.r.Run(ctx, []string{"ip", "-j", "route", "show", "table", strconv.Itoa(c.Table)})
	if err != nil {
		if !isMissingRuleError(err) {
			return false, false, err
		}
		routes = nil
	}
	var entries []map[string]any
	if len(routes) > 0 {
		if err = json.Unmarshal(routes, &entries); err != nil {
			return false, false, err
		}
	}
	for _, route := range entries {
		if !owned || stringValue(route["type"]) != "local" || stringValue(route["dev"]) != "lo" || (stringValue(route["dst"]) != "default" && stringValue(route["dst"]) != "0.0.0.0/0") {
			return false, false, fmt.Errorf("managed route table is already in use")
		}
	}
	return policyExists, len(entries) > 0, nil
}
