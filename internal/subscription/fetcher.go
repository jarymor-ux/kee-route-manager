package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Fetcher struct {
	cfg     config.Subscriptions
	backoff []config.Duration
	dir     string
	client  *http.Client
	now     func() time.Time
}
type Result struct {
	Nodes  []model.Node
	States map[string]model.SourceState
	Errors []error
}
type cache struct {
	Schema    int          `json:"schema"`
	SourceID  string       `json:"source_id"`
	FetchedAt time.Time    `json:"fetched_at"`
	Nodes     []model.Node `json:"nodes"`
}

func New(cfg config.Subscriptions, b []config.Duration, cacheDir string) *Fetcher {
	return &Fetcher{cfg: cfg, backoff: b, dir: filepath.Join(cacheDir, "subscriptions"), client: &http.Client{Timeout: cfg.RequestTimeout.Duration}, now: func() time.Time { return time.Now().UTC() }}
}
func (f *Fetcher) FetchAll(ctx context.Context, prev map[string]model.SourceState, force bool) Result {
	_ = os.MkdirAll(f.dir, 0700)
	r := Result{States: map[string]model.SourceState{}}
	type item struct {
		id    string
		nodes []model.Node
		state model.SourceState
		err   error
	}
	ch := make(chan item, len(f.cfg.Sources))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, s := range f.cfg.Sources {
		if !s.Enabled {
			continue
		}
		wg.Add(1)
		go func(s config.Source) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				ch <- item{id: s.ID, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			n, st, e := f.one(ctx, s, prev[s.ID], force)
			ch <- item{s.ID, n, st, e}
		}(s)
	}
	go func() { wg.Wait(); close(ch) }()
	all := []model.Node{}
	emergency := []model.Node{}
	for x := range ch {
		r.States[x.id] = x.state
		if x.state.Status == "unavailable" && x.state.UsingCache {
			emergency = append(emergency, x.nodes...)
		} else {
			all = append(all, x.nodes...)
		}
		if x.err != nil {
			r.Errors = append(r.Errors, fmt.Errorf("%s: %w", x.id, x.err))
		}
	}
	if len(all) == 0 {
		all = emergency
	}
	r.Nodes = Merge(all, f.cfg.MaxNodes)
	return r
}
func (f *Fetcher) one(ctx context.Context, s config.Source, old model.SourceState, force bool) ([]model.Node, model.SourceState, error) {
	now := f.now()
	st := old
	st.ID = s.ID
	st.Name = s.Name
	if !force && !old.LastSuccessAt.IsZero() && now.Before(old.LastSuccessAt.Add(f.cfg.RefreshInterval.Duration)) && (old.Status == "healthy" || old.Status == "recovering") {
		if xs, c, e := f.load(s.ID); e == nil {
			st.UsingCache = true
			st.NodeCount = len(xs)
			st.CacheExpiresAt = c.FetchedAt.Add(f.cfg.CacheTTL.Duration)
			return xs, st, nil
		}
	}
	st.LastAttemptAt = now
	if !old.NextRetryAt.IsZero() && now.Before(old.NextRetryAt) {
		if xs, c, e := f.load(s.ID); e == nil {
			st.UsingCache = true
			st.NodeCount = len(xs)
			st.CacheExpiresAt = c.FetchedAt.Add(f.cfg.CacheTTL.Duration)
			if now.Before(st.CacheExpiresAt) {
				st.Status = "degraded"
			} else {
				st.Status = "unavailable"
			}
			return xs, st, fmt.Errorf("backoff until %s", old.NextRetryAt.Format(time.RFC3339))
		}
	}
	body, err := f.download(ctx, s)
	if err == nil {
		var xs []model.Node
		xs, err = ParsePayload(body, s.ID)
		if err == nil {
			c := cache{1, s.ID, now, xs}
			if err = f.save(c); err == nil {
				st.Status = "healthy"
				if old.Status == "unavailable" {
					st.Status = "recovering"
				}
				st.NodeCount = len(xs)
				st.LastSuccessAt = now
				st.LastError = ""
				st.ConsecutiveFailures = 0
				st.RetryLevel = 0
				st.NextRetryAt = time.Time{}
				st.UsingCache = false
				st.CacheExpiresAt = now.Add(f.cfg.CacheTTL.Duration)
				return xs, st, nil
			}
		}
	}
	st.ConsecutiveFailures++
	level := st.RetryLevel
	if level < 0 {
		level = 0
	}
	if len(f.backoff) > 0 {
		if level >= len(f.backoff) {
			level = len(f.backoff) - 1
		}
		st.NextRetryAt = now.Add(f.backoff[level].Duration)
		if st.RetryLevel < len(f.backoff)-1 {
			st.RetryLevel++
		}
	}
	if err != nil {
		st.LastError = err.Error()
	}
	if xs, c, e := f.load(s.ID); e == nil {
		st.UsingCache = true
		st.NodeCount = len(xs)
		st.CacheExpiresAt = c.FetchedAt.Add(f.cfg.CacheTTL.Duration)
		if now.Before(st.CacheExpiresAt) {
			st.Status = "degraded"
		} else {
			st.Status = "unavailable"
		}
		return xs, st, err
	}
	st.Status = "unavailable"
	st.NodeCount = 0
	return nil, st, err
}
func (f *Fetcher) download(ctx context.Context, s config.Source) ([]byte, error) {
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "file" {
		fd, err := os.Open(u.Path)
		if err != nil {
			return nil, err
		}
		defer fd.Close()
		return limited(fd, int64(f.cfg.MaxResponseBytes))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Kee-Route-Manager/1.0")
	for k, v := range s.Headers {
		if strings.ContainsAny(k+v, "\r\n") {
			return nil, fmt.Errorf("invalid header")
		}
		req.Header.Set(k, v)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return limited(resp.Body, int64(f.cfg.MaxResponseBytes))
}
func limited(r io.Reader, n int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, n+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > n {
		return nil, fmt.Errorf("response exceeds %d bytes", n)
	}
	return b, nil
}
func (f *Fetcher) save(c cache) error {
	b, _ := json.MarshalIndent(c, "", "  ")
	p := filepath.Join(f.dir, c.SourceID+".json")
	fd, err := os.CreateTemp(f.dir, ".cache-*")
	if err != nil {
		return err
	}
	n := fd.Name()
	defer os.Remove(n)
	_ = fd.Chmod(0600)
	if _, err = fd.Write(append(b, '\n')); err == nil {
		err = fd.Sync()
	}
	if e := fd.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(n, p)
}
func (f *Fetcher) load(id string) ([]model.Node, cache, error) {
	b, err := os.ReadFile(filepath.Join(f.dir, id+".json"))
	if err != nil {
		return nil, cache{}, err
	}
	var c cache
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, c, err
	}
	if c.Schema != 1 || c.SourceID != id || len(c.Nodes) == 0 {
		return nil, c, errors.New("invalid cache")
	}
	return c.Nodes, c, nil
}
