package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
	"strings"
)

const (
	maxEvents       = 2000
	maxEventLogSize = 8 << 20
)

type Store struct {
	mu                 sync.RWMutex
	stateDir, cacheDir string
	state              model.State
	nodes              map[string]model.Node
	events             []event.Event
	seq                uint64
	dirty              bool
	initial            model.State
	committedNodes     map[string]model.Node
}

func New(stateDir, cacheDir string, initial model.State) (*Store, error) {
	for _, d := range []string{stateDir, cacheDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return nil, err
		}
	}
	s := &Store{stateDir: stateDir, cacheDir: cacheDir, state: initial, initial: clone(initial), nodes: map[string]model.Node{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	// Nodes and state are validated together before any routing mutation is allowed.
	if b, err := os.ReadFile(filepath.Join(s.cacheDir, "nodes.json")); err == nil {
		var xs []model.Node
		if json.Unmarshal(b, &xs) == nil {
			for _, x := range xs {
				if x.ID != "" {
					s.nodes[x.ID] = x
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var invalid bool
	var recoveredPrevious bool
	for _, name := range []string{"state.json", "state.previous.json"} {
		b, err := os.ReadFile(filepath.Join(s.stateDir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var candidate model.State
		candidateNodes := s.nodes
		if name == "state.previous.json" {
			var previous previousState
			if err = json.Unmarshal(b, &previous); err == nil && previous.State.SchemaVersion != 0 {
				candidate = previous.State
				candidateNodes = map[string]model.Node{}
				for _, n := range previous.Nodes {
					candidateNodes[n.ID] = n
				}
			} else {
				err = json.Unmarshal(b, &candidate)
			}
		} else {
			err = json.Unmarshal(b, &candidate)
		}
		if err == nil {
			err = s.validateWithNodes(candidate, candidateNodes)
		}
		if err != nil {
			invalid = true
			continue
		}
		s.state = candidate
		s.nodes = candidateNodes
		recoveredPrevious = name == "state.previous.json"
		if s.state.Measurements == nil {
			s.state.Measurements = map[string]model.Measurement{}
		}
		if s.state.Sources == nil {
			s.state.Sources = map[string]model.SourceState{}
		}
		invalid = false
		break
	}
	if invalid {
		s.state = clone(s.initial)
		s.state.XrayLastError = "state copies invalid; routing reconciliation required"
	}
	if recoveredPrevious {
		xs := make([]model.Node, 0, len(s.nodes))
		for _, node := range s.nodes {
			xs = append(xs, node)
		}
		if err := writeJSON(filepath.Join(s.cacheDir, "nodes.json"), xs); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(s.stateDir, "state.json"), s.state); err != nil {
			return err
		}
	}
	s.committedNodes = copyNodes(s.nodes)
	f, err := os.Open(s.eventsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1<<20)
	for sc.Scan() {
		var e event.Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			e.Message = redact.Text(e.Message)
			e.Fields = redactFields(e.Fields)
			s.events = append(s.events, e)
			if e.Sequence > s.seq {
				s.seq = e.Sequence
			}
			if len(s.events) > maxEvents {
				s.events = s.events[len(s.events)-maxEvents:]
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if info, err := os.Stat(s.eventsPath()); err == nil && info.Size() > maxEventLogSize {
		return s.compactEventsLocked()
	}
	return nil
}

func (s *Store) State() model.State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := clone(s.state)
	sanitizeState(&out)
	return out
}

func (s *Store) Update(fn func(*model.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := clone(s.state)
	if err := fn(&v); err != nil {
		return err
	}
	sanitizeState(&v)
	if reflect.DeepEqual(v, s.state) {
		return nil
	}
	if err := s.validate(v); err != nil {
		return err
	}
	v.UpdatedAt = time.Now().UTC()
	if err := s.persist(v); err != nil {
		return err
	}
	s.state = v
	s.dirty = false
	return nil
}

// UpdateVolatile updates in-memory state without forcing a flash/filesystem sync.
// Flush or a later durable Update persists the accumulated value.
func (s *Store) UpdateVolatile(fn func(*model.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := clone(s.state)
	if err := fn(&v); err != nil {
		return err
	}
	sanitizeState(&v)
	if reflect.DeepEqual(v, s.state) {
		return nil
	}
	if err := s.validate(v); err != nil {
		return err
	}
	v.UpdatedAt = time.Now().UTC()
	s.state = v
	s.dirty = true
	return nil
}

func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := s.persist(s.state); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func (s *Store) ReplaceNodes(xs []model.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	xs = append([]model.Node(nil), xs...)
	sort.Slice(xs, func(i, j int) bool { return xs[i].ID < xs[j].ID })
	current := make([]model.Node, 0, len(s.nodes))
	for _, node := range s.nodes {
		current = append(current, node)
	}
	sort.Slice(current, func(i, j int) bool { return current[i].ID < current[j].ID })
	if reflect.DeepEqual(xs, current) {
		return nil
	}
	if err := writeJSON(filepath.Join(s.cacheDir, "nodes.json"), xs); err != nil {
		return err
	}
	s.nodes = map[string]model.Node{}
	for _, x := range xs {
		s.nodes[x.ID] = x
	}
	return nil
}

func (s *Store) Nodes() []model.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	xs := make([]model.Node, 0, len(s.nodes))
	for _, x := range s.nodes {
		xs = append(xs, x)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i].ID < xs[j].ID })
	return xs
}

func (s *Store) Node(id string) (model.Node, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.nodes[id]
	return v, ok
}

func (s *Store) Append(e event.Event) (event.Event, error) {
	e.Message = redact.Text(e.Message)
	e.Fields = redactFields(e.Fields)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	e.Sequence = s.seq
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	p := s.eventsPath()
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return e, err
	}
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return e, err
	}
	e.Message = redact.Text(e.Message)
	e.Fields = redactFields(e.Fields)
	s.events = append(s.events, e)
	if len(s.events) > maxEvents {
		s.events = append([]event.Event(nil), s.events[len(s.events)-maxEvents:]...)
	}
	if info, statErr := os.Stat(p); statErr == nil && info.Size() > maxEventLogSize {
		if compactErr := s.compactEventsLocked(); compactErr != nil {
			return e, compactErr
		}
	}
	return e, nil
}

func (s *Store) Events(after uint64, limit int) []event.Event {
	if limit < 1 || limit > 1000 {
		limit = 200
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []event.Event{}
	for _, e := range s.events {
		if e.Sequence > after {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (s *Store) compactEventsLocked() error {
	path := s.eventsPath()
	tmp, err := os.CreateTemp(s.stateDir, ".events-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	writer := bufio.NewWriterSize(tmp, 64<<10)
	for _, e := range s.events {
		b, marshalErr := json.Marshal(e)
		if marshalErr != nil {
			tmp.Close()
			return marshalErr
		}
		if _, err = writer.Write(append(b, '\n')); err != nil {
			tmp.Close()
			return err
		}
	}
	if err = writer.Flush(); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(s.stateDir)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if err != nil {
		return err
	}
	n := f.Name()
	defer os.Remove(n)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(n, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func clone(v model.State) model.State {
	b, _ := json.Marshal(v)
	var x model.State
	_ = json.Unmarshal(b, &x)
	return x
}
func (s *Store) Path(name string) string      { return filepath.Join(s.stateDir, name) }
func (s *Store) CachePath(name string) string { return filepath.Join(s.cacheDir, name) }
func (s *Store) eventsPath() string           { return filepath.Join(s.stateDir, "events.jsonl") }

// validate enforces the configured pool topology and rejects partial selections.
func (s *Store) validate(v model.State) error { return s.validateWithNodes(v, s.nodes) }
func (s *Store) validateWithNodes(v model.State, nodes map[string]model.Node) error {
	if v.SchemaVersion != model.StateSchema {
		return fmt.Errorf("unsupported state schema")
	}
	if len(v.Pool) != len(s.initial.Pool) && !(!v.XrayConfigured && len(v.Pool) == 0) {
		return fmt.Errorf("state pool size differs from configuration")
	}
	if v.XrayGeneration < 0 {
		return fmt.Errorf("negative state generation")
	}
	seen := map[string]bool{}
	for i, slot := range v.Pool {
		if slot.Index != i || slot.Tag != s.initial.Pool[i].Tag {
			return fmt.Errorf("invalid slot topology at %d", i)
		}
		if slot.NodeID != "" {
			if seen[slot.NodeID] {
				return fmt.Errorf("duplicate pool node")
			}
			seen[slot.NodeID] = true
			if _, ok := nodes[slot.NodeID]; !ok {
				return fmt.Errorf("pool node is absent from node cache")
			}
		}
	}
	if v.ActiveSlot < -1 || v.ActiveSlot >= len(v.Pool) {
		return fmt.Errorf("invalid active slot")
	}
	if v.DirectMode && (v.ActiveSlot != -1 || v.ActiveNodeID != "") {
		return fmt.Errorf("direct state has VPN selection")
	}
	if v.ActiveSlot >= 0 {
		if v.ActiveNodeID == "" || v.Pool[v.ActiveSlot].NodeID != v.ActiveNodeID {
			return fmt.Errorf("active node differs from slot")
		}
	} else if v.ActiveNodeID != "" {
		return fmt.Errorf("active node has no slot")
	}
	if !v.XrayConfigured && (v.ActiveSlot != -1 || v.DirectMode) {
		return fmt.Errorf("unconfigured state has active selection")
	}
	for id, m := range v.Measurements {
		if id != m.NodeID {
			return fmt.Errorf("measurement node mismatch")
		}
		if _, ok := nodes[id]; !ok {
			return fmt.Errorf("measurement node absent from cache")
		}
	}
	return nil
}

type previousState struct {
	State model.State  `json:"state"`
	Nodes []model.Node `json:"nodes"`
}

func copyNodes(nodes map[string]model.Node) map[string]model.Node {
	out := make(map[string]model.Node, len(nodes))
	for id, node := range nodes {
		out[id] = node
	}
	return out
}
func (s *Store) persist(v model.State) error {
	if b, err := os.ReadFile(filepath.Join(s.stateDir, "state.json")); err == nil {
		var previous model.State
		if json.Unmarshal(b, &previous) == nil && s.validateWithNodes(previous, s.committedNodes) == nil {
			bundle := previousState{State: previous}
			for _, node := range s.committedNodes {
				bundle.Nodes = append(bundle.Nodes, node)
			}
			sort.Slice(bundle.Nodes, func(i, j int) bool { return bundle.Nodes[i].ID < bundle.Nodes[j].ID })
			if err = writeJSON(filepath.Join(s.stateDir, "state.previous.json"), bundle); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeJSON(filepath.Join(s.stateDir, "state.json"), v); err != nil {
		return err
	}
	s.committedNodes = copyNodes(s.nodes)
	return nil
}

func sanitizeState(state *model.State) {
	state.XrayLastError = redact.Text(state.XrayLastError)
	state.LastHealthMessage = redact.Text(state.LastHealthMessage)
	state.LastBenchmark.Error = redact.Text(state.LastBenchmark.Error)
	for id, value := range state.Measurements {
		value.Error = redact.Text(value.Error)
		state.Measurements[id] = value
	}
	for id, value := range state.Sources {
		value.LastError = redact.Text(value.LastError)
		state.Sources[id] = value
	}
	for i := range state.LastBenchmark.Results {
		state.LastBenchmark.Results[i].Error = redact.Text(state.LastBenchmark.Results[i].Error)
	}
}
func redactFields(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}
	out := make(map[string]any, len(fields))
	for key, value := range fields {
		normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
		switch normalized {
		case "authorization", "cookie", "setcookie", "uuid", "password", "publickey", "shortid", "secret", "token":
			out[key] = "<redacted>"
			continue
		}
		switch v := value.(type) {
		case string:
			out[key] = redact.Text(v)
		case map[string]any:
			out[key] = redactFields(v)
		case []any:
			xs := make([]any, len(v))
			for i, item := range v {
				if text, ok := item.(string); ok {
					xs[i] = redact.Text(text)
				} else {
					xs[i] = item
				}
			}
			out[key] = xs
		default:
			out[key] = value
		}
	}
	return out
}
