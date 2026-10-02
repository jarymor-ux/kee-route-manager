package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	return &Fetcher{cfg: cfg, backoff: b, dir: filepath.Join(cacheDir, "subscriptions"), client: &http.Client{Timeout: cfg.RequestTimeout.Duration, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many subscription redirects")
		}
		if len(via) > 0 && (req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme) {
			return errors.New("cross-origin subscription redirect refused")
		}
		return nil
	}}, now: func() time.Time { return time.Now().UTC() }}
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
	all := map[string][]model.Node{}
	emergency := map[string][]model.Node{}
	order := []string{}
	for _, s := range f.cfg.Sources {
		if s.Enabled {
			order = append(order, s.ID)
		}
	}
	for x := range ch {
		r.States[x.id] = x.state
		if x.state.Status == "unavailable" && x.state.UsingCache {
			emergency[x.id] = x.nodes
		} else if len(x.nodes) > 0 {
			all[x.id] = x.nodes
		}
		if x.err != nil {
			r.Errors = append(r.Errors, fmt.Errorf("%s: %s", x.id, redact.Text(x.err.Error())))
		}
	}
	if len(all) == 0 {
		all = emergency
	}
	r.Nodes = fairMerge(all, order, f.cfg.MaxNodes, f.cfg.MaxNodesPerSource)
	return r
}
func (f *Fetcher) one(ctx context.Context, s config.Source, old model.SourceState, force bool) ([]model.Node, model.SourceState, error) {
	now := f.now()
	st := old
	st.ID = s.ID
	st.Name = s.Name
	st.UsingCache = false
	st.CacheExpiresAt = time.Time{}
	if !force && !old.LastSuccessAt.IsZero() && now.Before(old.LastSuccessAt.Add(f.cfg.RefreshInterval.Duration)) && (old.Status == "healthy" || old.Status == "recovering") {
		if xs, c, e := f.load(s.ID); e == nil && now.Before(c.FetchedAt.Add(f.cfg.CacheTTL.Duration)) {
			st.UsingCache = true
			st.NodeCount = len(xs)
			st.CacheExpiresAt = c.FetchedAt.Add(f.cfg.CacheTTL.Duration)
			return xs, st, nil
		}
	}
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
		st.Status = "unavailable"
		st.UsingCache = false
		st.NodeCount = 0
		return nil, st, fmt.Errorf("backoff until %s", old.NextRetryAt.Format(time.RFC3339))
	}
	st.LastAttemptAt = now
	body, err := f.download(ctx, s)
	if err == nil {
		var xs []model.Node
		xs, err = ParsePayload(body, s.ID)
		if err == nil {
			if f.cfg.MaxNodesPerSource > 0 {
				xs = Merge(xs, f.cfg.MaxNodesPerSource)
			}
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
		st.LastError = redact.Text(err.Error())
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
	ctx, cancel := context.WithTimeout(ctx, f.cfg.RequestTimeout.Duration)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "file" {
		// Nonblocking open prevents a replaced path/FIFO from blocking before fstat.
		fd, err := os.OpenFile(u.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		defer fd.Close()
		info, err := fd.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("subscription source must be a regular file")
		}
		return limited(ctx, fd, int64(f.cfg.MaxResponseBytes))
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
	return limited(ctx, resp.Body, int64(f.cfg.MaxResponseBytes))
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

func limited(ctx context.Context, r io.Reader, n int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(contextReader{ctx, r}, n+1))
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
