package xray

import (
	"bytes"
	"sync"
)

// commandOutput bounds RAM use and synchronizes concurrent process writes and
// diagnostic reads. Writers still consume the full stream after the cap.
type commandOutput struct {
	mu    sync.Mutex
	b     bytes.Buffer
	limit int
}

func (out *commandOutput) Write(p []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	n := len(p)
	limit := out.limit
	if limit <= 0 {
		limit = 64 << 10
	}
	remaining := limit - out.b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = out.b.Write(p)
	}
	return n, nil
}
func (out *commandOutput) String() string {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.b.String()
}
