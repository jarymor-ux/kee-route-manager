package amnezia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// kernelInspector separates read-only kernel observation from network mutation.
// A probe failure must never be interpreted as an empty routing table.
type kernelInspector interface {
	Inspect(context.Context, string, ...string) ([]byte, error)
}

// Inspect only exposes three fixed, read-only iproute2 queries. It deliberately
// refuses all arbitrary commands, including other ip subcommands.
func (SystemRunner) Inspect(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name != "ip" {
		return nil, errors.New("unsupported AWG inspection command")
	}
	valid := len(args) == 3 && args[0] == "-j" &&
		((args[1] == "link" && args[2] == "show") ||
			(args[1] == "rule" && args[2] == "show"))
	if len(args) == 5 && args[0] == "-j" && args[1] == "route" &&
		args[2] == "show" && args[3] == "table" {
		_, err := strconv.ParseUint(args[4], 10, 32)
		valid = err == nil
	}
	if !valid {
		return nil, errors.New("unsupported AWG inspection arguments")
	}
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	data, err := exec.CommandContext(commandCtx, "ip", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("AWG kernel inspection failed: %w", err)
	}
	return data, nil
}

// inspectKernelAvailability is a conservative preflight, not recovery. Existing
// resources are foreign until durable KRM ownership can be established through
// reconciliation. Do not delete or overwrite resources discovered here.
func inspectKernelAvailability(ctx context.Context, runner Runner, iface, table string) error {
	inspector, ok := runner.(kernelInspector)
	if !ok {
		// Minimal mocked runners used by existing unit tests have no kernel.
		// Production always injects SystemRunner, which supports inspections.
		return nil
	}
	links, err := inspector.Inspect(ctx, "ip", "-j", "link", "show")
	if err != nil {
		return fmt.Errorf("inspect AWG interfaces: %w", err)
	}
	var linkRows []struct {
		Name string `json:"ifname"`
	}
	if err := json.Unmarshal(links, &linkRows); err != nil || linkRows == nil {
		return errors.New("invalid kernel interface inspection result")
	}
	for _, link := range linkRows {
		if link.Name == iface {
			return fmt.Errorf("AWG interface %s already exists in kernel", iface)
		}
	}
	routes, err := inspector.Inspect(ctx, "ip", "-j", "route", "show", "table", table)
	if err != nil {
		return fmt.Errorf("inspect AWG routing table: %w", err)
	}
	var routeRows []json.RawMessage
	if err := json.Unmarshal(routes, &routeRows); err != nil || routeRows == nil {
		return errors.New("invalid kernel route inspection result")
	}
	if len(routeRows) != 0 {
		return fmt.Errorf("AWG routing table %s is already in use", table)
	}
	rules, err := inspector.Inspect(ctx, "ip", "-j", "rule", "show")
	if err != nil {
		return fmt.Errorf("inspect AWG policy rules: %w", err)
	}
	var ruleRows []struct {
		Mark   json.RawMessage `json:"fwmark"`
		Table  json.RawMessage `json:"table"`
		Lookup json.RawMessage `json:"lookup"`
	}
	if err := json.Unmarshal(rules, &ruleRows); err != nil || ruleRows == nil {
		return errors.New("invalid kernel rule inspection result")
	}
	mark, err := strconv.ParseUint(table, 10, 32)
	if err != nil {
		return err
	}
	for _, rule := range ruleRows {
		if kernelTableMatches(rule.Table, mark) || kernelTableMatches(rule.Lookup, mark) {
			return fmt.Errorf("AWG routing table %s referenced by existing policy rule", table)
		}
		matches, err := kernelMarkMatches(rule.Mark, uint32(mark))
		if err != nil {
			return fmt.Errorf("inspect existing policy fwmark: %w", err)
		}
		if matches {
			return fmt.Errorf("AWG routing mark %s overlaps existing policy rule", table)
		}
	}
	return nil
}

func kernelTableMatches(raw json.RawMessage, table uint64) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value, err := strconv.ParseUint(text, 10, 32)
		return err == nil && value == table
	}
	var value uint64
	return json.Unmarshal(raw, &value) == nil && value == table
}

// A policy rule may select an entire range of marks via /mask or fwmask.
// Treat overlapping masked values as occupied, not just byte-for-byte matches.
func kernelMarkMatches(raw json.RawMessage, mark uint32) (bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		var num uint32
		if err := json.Unmarshal(raw, &num); err != nil {
			return false, err
		}
		return num == mark, nil
	}
	parts := strings.Split(value, "/")
	if len(parts) > 2 {
		return false, errors.New("invalid fwmark mask")
	}
	n, err := strconv.ParseUint(parts[0], 0, 32)
	if err != nil {
		return false, err
	}
	mask := uint64(0xffffffff)
	if len(parts) == 2 {
		mask, err = strconv.ParseUint(parts[1], 0, 32)
		if err != nil {
			return false, err
		}
	}
	return (n & mask) == (uint64(mark) & mask), nil
}
