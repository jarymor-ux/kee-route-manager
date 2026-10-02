package operation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrBusy = errors.New("another operation is running")

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
type Coordinator struct {
	mu        sync.RWMutex
	path      string
	current   *Operation
	last      *Operation
	lastWrite time.Time
	lastStage string
}

func New(dir string) (*Coordinator, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	c := &Coordinator{path: filepath.Join(dir, "operation.json")}
	if b, err := os.ReadFile(c.path); err == nil {
		var op Operation
		if json.Unmarshal(b, &op) == nil {
			if op.Status == "running" {
				op.Status = "unknown"
				op.Error = "service restarted while operation was running"
				op.FinishedAt = time.Now().UTC()
				op.UpdatedAt = op.FinishedAt
				_ = c.write(op)
			}
			c.last = &op
		}
	}
	return c, nil
}
func (c *Coordinator) Start(kind, source string) (*Handle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != nil {
		return nil, ErrBusy
	}
	now := time.Now().UTC()
	op := &Operation{ID: newID(), Type: kind, Source: source, Status: "running", Stage: "starting", StartedAt: now, UpdatedAt: now}
	if err := c.write(*op); err != nil {
		return nil, err
	}
	c.current = op
	c.lastWrite = now
	c.lastStage = op.Stage
	return &Handle{c: c, id: op.ID}, nil
}
func (c *Coordinator) Current() *Operation {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.current != nil {
		x := *c.current
		return &x
	}
	if c.last != nil {
		x := *c.last
		return &x
	}
	return nil
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
		o.Message = msg
		o.UpdatedAt = time.Now().UTC()
	})
}
func (h *Handle) Success(msg string) error { return h.finish("succeeded", msg, "") }
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
		o.Message = msg
		o.Error = failure
		o.UpdatedAt = now
		o.FinishedAt = now
	})
}
func (c *Coordinator) mutate(id string, fn func(*Operation)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil || c.current.ID != id {
		return fmt.Errorf("operation not current")
	}
	fn(c.current)
	mustWrite := c.current.Status != "running" || c.current.Stage != c.lastStage || time.Since(c.lastWrite) >= 500*time.Millisecond
	if mustWrite {
		if err := c.write(*c.current); err != nil {
			return err
		}
		c.lastWrite = time.Now()
		c.lastStage = c.current.Stage
	}
	if c.current.Status != "running" {
		x := *c.current
		c.last = &x
		c.current = nil
	}
	return nil
}
func (c *Coordinator) write(op Operation) error {
	b, err := json.MarshalIndent(op, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(c.path), ".op-*")
	if err != nil {
		return err
	}
	n := f.Name()
	defer os.Remove(n)
	_ = f.Chmod(0600)
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
