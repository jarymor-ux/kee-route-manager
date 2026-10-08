package operation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/redact"
)

var ErrBusy = errors.New("another operation is running")
var ErrSuperseded = errors.New("settings changed while benchmark was running")

type Operation struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	Stage      string    `json:"stage"`
	Current    int       `json:"current"`
	Total      int       `json:"total"`
	Message    string    `json:"message,omitempty"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// Keep the legacy top-level record readable by previous versions. Additional
// reservations share the same durable record; they never own routing journals.
type document struct {
	Operation
	Operations []Operation `json:"operations,omitempty"`
}
type Coordinator struct {
	mu        sync.RWMutex
	path      string
	active    map[string]*Operation
	last      *Operation
	completed []Operation
	lastWrite time.Time
	lastStage string
}

func New(dir string) (*Coordinator, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	c := &Coordinator{path: filepath.Join(dir, "operation.json"), active: map[string]*Operation{}}
	if b, err := os.ReadFile(c.path); err == nil {
		var doc document
		if json.Unmarshal(b, &doc) == nil && doc.ID != "" {
			records := doc.Operations
			if len(records) == 0 {
				records = []Operation{doc.Operation}
			}
			interrupted := false
			for i := range records {
				op := &records[i]
				if op.Status == "running" {
					interrupted = true
					op.Status = "unknown"
					op.Error = "service restarted while operation was running"
					op.FinishedAt = time.Now().UTC()
					op.UpdatedAt = op.FinishedAt
				}
				op.Error = redact.Text(op.Error)
				op.Message = redact.Text(op.Message)
			}
			op := doc.Operation
			for _, record := range records {
				if record.ID == doc.ID {
					op = record
				}
			}
			c.last = &op
			sort.Slice(records, func(i, j int) bool { return records[i].UpdatedAt.Before(records[j].UpdatedAt) })
			if len(records) > 16 {
				records = records[len(records)-16:]
			}
			c.completed = append([]Operation(nil), records...)
			if interrupted {
				_ = c.writeRecords(op, records)
			}
		}
	}
	return c, nil
}

// Start retains exclusive admission for callers that have not explicitly
// adopted resource compatibility.
func (c *Coordinator) Start(kind, source string) (*Handle, error) {
	return c.start(kind, source, false)
}

// StartCompatible permits a benchmark plus one independent control operation.
// Two controllers, two tests, and destructive lifecycle work remain exclusive.
func (c *Coordinator) StartCompatible(kind, source string) (*Handle, error) {
	return c.start(kind, source, true)
}
func compatible(a, b string) bool {
	control := func(kind string) bool {
		switch kind {
		case "client-policy", "wake-on-lan", "switch-slot", "switch-direct", "panel-dns":
			return true
		}
		return false
	}
	return a == "benchmark" && control(b) || b == "benchmark" && control(a)
}
func (c *Coordinator) start(kind, source string, share bool) (*Handle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, op := range c.active {
		if !share || !compatible(kind, op.Type) {
			return nil, ErrBusy
		}
	}
	now := time.Now().UTC()
	op := Operation{ID: newID(), Type: kind, Source: source, Status: "running", Stage: "starting", StartedAt: now, UpdatedAt: now}
	records := c.recordsLocked()
	records = append(records, op)
	if err := c.writeRecords(op, records); err != nil {
		return nil, err
	}
	c.active[op.ID] = &op
	c.lastWrite = now
	c.lastStage = op.Stage
	return &Handle{c: c, id: op.ID}, nil
}
func (c *Coordinator) recordsLocked() []Operation {
	records := make([]Operation, 0, len(c.active))
	for _, op := range c.active {
		records = append(records, *op)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].StartedAt.Before(records[j].StartedAt) })
	records = append(records, c.completed...)
	sort.Slice(records, func(i, j int) bool { return records[i].UpdatedAt.Before(records[j].UpdatedAt) })
	return records
}
func (c *Coordinator) Current() *Operation {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var selected *Operation
	for _, op := range c.active {
		if selected == nil || op.Type == "benchmark" || selected.Type != "benchmark" && op.StartedAt.Before(selected.StartedAt) {
			selected = op
		}
	}
	if selected == nil {
		selected = c.last
	}
	if selected == nil {
		return nil
	}
	copy := *selected
	return &copy
}
func (c *Coordinator) List() []Operation {
	c.mu.RLock()
	defer c.mu.RUnlock()
	records := c.recordsLocked()
	if len(records) == 0 && c.last != nil {
		records = append(records, *c.last)
	}
	return records
}

type Handle struct {
	c  *Coordinator
	id string
}

func (h *Handle) ID() string { return h.id }
func (h *Handle) Update(stage string, current, total int, msg string) error {
	return h.c.mutate(h.id, func(o *Operation) {
		o.Stage = stage
		o.Current = current
		o.Total = total
		o.Message = redact.Text(msg)
		o.UpdatedAt = time.Now().UTC()
	})
}
func (h *Handle) Success(msg string) error { return h.finish("succeeded", msg, "") }
func (h *Handle) Cancel(msg string) error  { return h.finish("canceled", msg, "") }
func (h *Handle) Fail(err error) error {
	if err == nil {
		err = errors.New("failed")
	}
	return h.finish("failed", "", err.Error())
}
func (h *Handle) finish(status, msg, failure string) error {
	return h.c.mutate(h.id, func(o *Operation) {
		now := time.Now().UTC()
		o.Status = status
		o.Stage = "finished"
		o.Message = redact.Text(msg)
		o.Error = redact.Text(failure)
		o.UpdatedAt = now
		o.FinishedAt = now
	})
}
func (c *Coordinator) mutate(id string, fn func(*Operation)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	op := c.active[id]
	if op == nil {
		return fmt.Errorf("operation not current")
	}
	fn(op)
	terminal := op.Status != "running"
	if terminal {
		defer func() {
			copy := *op
			c.last = &copy
			delete(c.active, id)
			c.completed = append(c.completed, copy)
			if len(c.completed) > 16 {
				c.completed = c.completed[len(c.completed)-16:]
			}
		}()
	}
	if terminal || op.Stage != c.lastStage || time.Since(c.lastWrite) >= 500*time.Millisecond {
		if err := c.writeRecords(*op, c.recordsLocked()); err != nil {
			if terminal {
				op.Status = "unknown"
				failure := fmt.Errorf("operation completion persistence failed: %w", err)
				if op.Error != "" {
					failure = fmt.Errorf("%s; %w", op.Error, failure)
				}
				op.Error = redact.Text(failure.Error())
			}
			return err
		}
		c.lastWrite = time.Now()
		c.lastStage = op.Stage
	}
	return nil
}
func (c *Coordinator) writeRecords(op Operation, records []Operation) error {
	// Prefer a still-running benchmark in the legacy view, even when an
	// independent action is the writer of this snapshot.
	for _, record := range records {
		if record.Status == "running" && (op.Status != "running" || record.Type == "benchmark") {
			op = record
		}
	}
	doc := document{Operation: op}
	if len(records) > 1 {
		doc.Operations = records
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(c.path), ".op-*")
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
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(n, c.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(c.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("op-%d", time.Now().UnixNano())
	}
	return "op-" + hex.EncodeToString(b)
}
