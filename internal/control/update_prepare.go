package control

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/jarymor-ux/kee-route-manager/internal/core"
)

type updateGate struct {
	mu       sync.RWMutex
	frozen   bool
	prepared bool
	manager  *core.Manager
	version  string
}

func replyUpdate(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (g *updateGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		g.mu.RLock()
		defer g.mu.RUnlock()
		if g.frozen {
			replyUpdate(w, http.StatusServiceUnavailable, map[string]string{"error": "controller prepared for update"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *updateGate) public(next http.Handler) http.Handler {
	guarded := g.wrap(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/update/") {
			http.NotFound(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

func (g *updateGate) local(next http.Handler) http.Handler {
	guarded := g.wrap(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/update/prepare" {
			guarded.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.frozen {
			g.frozen = true
			if err := g.manager.PrepareUpdate(); err == nil {
				g.prepared = true
			}
		}
		if !g.prepared {
			replyUpdate(w, http.StatusConflict, map[string]string{"error": "update preparation failed; restart current version"})
			return
		}
		replyUpdate(w, http.StatusOK, map[string]any{"prepared": true, "version": g.version, "pid": os.Getpid()})
	})
}
