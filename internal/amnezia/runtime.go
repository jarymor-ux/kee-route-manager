package amnezia

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Runner interface {
	Run(context.Context, string, ...string) error
}
type SystemRunner struct{}

func (SystemRunner) Run(ctx context.Context, name string, args ...string) error {
	if name != "awg-quick" && name != "ip" {
		return errors.New("unsupported AWG system command")
	}
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, name, args...)
	if name == "awg-quick" {
		// Avoid reliance on an AWG kernel module. The official userspace backend
		// is selected without executing arbitrary profile-provided hooks.
		cmd.Env = append(os.Environ(), "WG_QUICK_USERSPACE_IMPLEMENTATION=amneziawg-go")
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("AWG runtime command %s failed: %w", name, err)
	}
	return nil
}

type Runtime struct {
	mu     sync.Mutex
	store  *ProfileStore
	dir    string
	runner Runner
	active map[string]bool
}

func NewRuntime(store *ProfileStore, dir string, runner Runner) (*Runtime, error) {
	if store == nil || runner == nil || !filepath.IsAbs(dir) {
		return nil, errors.New("invalid AWG runtime parameters")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("runtime directory must be private")
	}
	return &Runtime{store: store, dir: dir, runner: runner, active: map[string]bool{}}, nil
}
func deviceName(id string) string { return "krmawg" + id[:8] }
// RoutingMark identifies one AWG profile independently from its private tunnel IP.
// The same numeric value is used for the Xray SO_MARK and policy routing table.
func RoutingMark(id string) (uint32, error) {
	if !profileIDPattern.MatchString(id) {
		return 0, errors.New("invalid AWG profile identity")
	}
	n, err := strconv.ParseUint(id[:6], 16, 32)
	if err != nil {
		return 0, err
	}
	return uint32(n + 200000), nil
}
func tableNumber(id string) (string, error) {
	mark, err := RoutingMark(id)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(uint64(mark), 10), nil
}
func (r *Runtime) Ensure(ctx context.Context, id string) (string, string, error) {
	if !profileIDPattern.MatchString(id) {
		return "", "", errors.New("invalid AWG profile identity")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, err := r.store.Read(id)
	if err != nil {
		return "", "", err
	}
	p, err := ParseConfig(raw)
	if err != nil {
		return "", "", err
	}
	var address string
	for _, text := range p.Interface.Addresses {
		prefix, e := netip.ParsePrefix(text)
		if e == nil && prefix.Addr().Is4() {
			address = prefix.Addr().String()
			break
		}
	}
	if address == "" {
		return "", "", errors.New("AWG profile requires IPv4 interface address")
	}
	hasDefault := false
	for _, network := range p.Peer.AllowedIPs {
		if network == "0.0.0.0/0" {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		return "", "", errors.New("AWG profile requires IPv4 default AllowedIPs")
	}
	name := deviceName(id)
	table, err := tableNumber(id)
	if err != nil {
		return "", "", err
	}
	if r.active[id] {
		return name, address, nil
	}
	safe, err := isolatedConfig(raw)
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(r.dir, name+".conf")
	if err = atomicPrivateWrite(r.dir, path, safe); err != nil {
		return "", "", err
	}
	rollback := func() { _ = r.runner.Run(context.Background(), "awg-quick", "down", path); _ = os.Remove(path) }
	if err = r.runner.Run(ctx, "awg-quick", "up", path); err != nil {
		rollback()
		return "", "", err
	}
	if err = r.runner.Run(ctx, "ip", "route", "replace", "table", table, "default", "dev", name); err != nil {
		rollback()
		return "", "", err
	}
	if err = r.runner.Run(ctx, "ip", "rule", "add", "pref", table, "fwmark", table, "lookup", table); err != nil {
		_ = r.runner.Run(context.Background(), "ip", "route", "flush", "table", table)
		rollback()
		return "", "", err
	}
	r.active[id] = true
	return name, address, nil
}
func isolatedConfig(raw []byte) ([]byte, error) {
	p, err := ParseConfig(raw)
	if err != nil {
		return nil, err
	}
	_ = p
	var out bytes.Buffer
	reader := bufio.NewScanner(bytes.NewReader(raw))
	inserted := false
	for reader.Scan() {
		line := reader.Text()
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "[Interface]") {
			if inserted {
				return nil, errors.New("duplicate interface")
			}
			out.WriteString(line)
			out.WriteByte('\n')
			out.WriteString("Table = off\n")
			inserted = true
			continue
		}
		key, _, isKey := strings.Cut(trimmed, "=")
		if isKey && strings.EqualFold(strings.TrimSpace(key), "DNS") {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if err := reader.Err(); err != nil {
		return nil, err
	}
	if !inserted {
		return nil, errors.New("AWG interface section missing")
	}
	return out.Bytes(), nil
}
func (r *Runtime) Stop(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopLocked(ctx, id)
}
func (r *Runtime) DeleteProfile(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.stopLocked(ctx, id); err != nil {
		return err
	}
	return r.store.Delete(id)
}
func (r *Runtime) stopLocked(ctx context.Context, id string) error {
	if !profileIDPattern.MatchString(id) {
		return errors.New("invalid profile ID")
	}
	if !r.active[id] {
		return nil
	}
	name := deviceName(id)
	table, err := tableNumber(id)
	if err != nil {
		return err
	}
	raw, err := r.store.Read(id)
	if err != nil {
		return err
	}
	p, err := ParseConfig(raw)
	if err != nil {
		return err
	}
	var address string
	for _, text := range p.Interface.Addresses {
		prefix, e := netip.ParsePrefix(text)
		if e == nil && prefix.Addr().Is4() {
			address = prefix.Addr().String()
			break
		}
	}
	if address == "" {
		return errors.New("missing AWG local IPv4")
	}
	if err = r.runner.Run(ctx, "ip", "rule", "del", "pref", table, "fwmark", table, "lookup", table); err != nil {
		return err
	}
	if err = r.runner.Run(ctx, "ip", "route", "flush", "table", table); err != nil {
		return err
	}
	path := filepath.Join(r.dir, name+".conf")
	if err = r.runner.Run(ctx, "awg-quick", "down", path); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	delete(r.active, id)
	return nil
}

func (r *Runtime) StopAll(ctx context.Context) error {
	r.mu.Lock()
	ids := make([]string, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	var failures []error
	for _, id := range ids {
		if err := r.Stop(ctx, id); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
