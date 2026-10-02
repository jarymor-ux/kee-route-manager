package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/event"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
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
}

func New(stateDir, cacheDir string, initial model.State) (*Store, error) {
	for _, d := range []string{stateDir, cacheDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return nil, err
		}
	}
	s := &Store{stateDir: stateDir, cacheDir: cacheDir, state: initial, nodes: map[string]model.Node{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	if b, err := os.ReadFile(filepath.Join(s.stateDir, "state.json")); err == nil {
		if err := json.Unmarshal(b, &s.state); err != nil {
			return err
		}
		if s.state.Measurements == nil {
			s.state.Measurements = map[string]model.Measurement{}
		}
		if s.state.Sources == nil {
			s.state.Sources = map[string]model.SourceState{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if b, err := os.ReadFile(filepath.Join(s.cacheDir, "nodes.json")); err == nil {
		var xs []model.Node
		if err := json.Unmarshal(b, &xs); err != nil {
			return err
		}
		for _, x := range xs {
			s.nodes[x.ID] = x
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
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
	return clone(s.state)
}

func (s *Store) Update(fn func(*model.State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := clone(s.state)
	if err := fn(&v); err != nil {
		return err
	}
	if reflect.DeepEqual(v, s.state) {
		return nil
	}
	v.UpdatedAt = time.Now().UTC()
	if err := writeJSON(filepath.Join(s.stateDir, "state.json"), v); err != nil {
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
	if reflect.DeepEqual(v, s.state) {
		return nil
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
	if err := writeJSON(filepath.Join(s.stateDir, "state.json"), s.state); err != nil {
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
