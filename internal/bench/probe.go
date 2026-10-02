package bench

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type ProbeResult struct {
	TargetID string        `json:"target_id"`
	Success  bool          `json:"success"`
	Status   int           `json:"status"`
	Duration time.Duration `json:"duration"`
	Bytes    int64         `json:"bytes"`
	Error    string        `json:"error,omitempty"`
}
type Prober struct {
	Timeout  time.Duration
	MaxBytes int64
}

func NewProber(timeout time.Duration, max int64) *Prober { return &Prober{timeout, max} }
func (p *Prober) Probe(ctx context.Context, proxy *url.URL, t config.Target) ProbeResult {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, e := http.NewRequestWithContext(cctx, http.MethodGet, t.URL, nil)
	if e != nil {
		return ProbeResult{TargetID: t.ID, Error: e.Error()}
	}
	req.Header.Set("User-Agent", "Kee-Route-Manager/1.0")
	start := time.Now()
	resp, e := p.client(proxy).Do(req)
	if e != nil {
		return ProbeResult{TargetID: t.ID, Duration: time.Since(start), Error: e.Error()}
	}
	defer resp.Body.Close()
	limit := int64(t.MaxResponseBytes)
	if limit <= 0 {
		limit = p.MaxBytes
	}
	if limit <= 0 {
		limit = 64 << 10
	}
	n, e := io.Copy(io.Discard, io.LimitReader(resp.Body, limit))
	d := time.Since(start)
	if e != nil {
		return ProbeResult{TargetID: t.ID, Status: resp.StatusCode, Duration: d, Bytes: n, Error: e.Error()}
	}
	ok := statusOK(resp.StatusCode, t.Policy)
	r := ProbeResult{TargetID: t.ID, Success: ok, Status: resp.StatusCode, Duration: d, Bytes: n}
	if !ok {
		r.Error = fmt.Sprintf("HTTP %d does not match %s", resp.StatusCode, t.Policy)
	}
	return r
}
func (p *Prober) CheckMajority(ctx context.Context, proxy *url.URL, targets []config.Target) (int, int, []ProbeResult) {
	passed := 0
	rs := []ProbeResult{}
	for _, t := range targets {
		r := p.Probe(ctx, proxy, t)
		rs = append(rs, r)
		if r.Success {
			passed++
		}
	}
	return passed, len(targets), rs
}
func Majority(passed, total int) bool { return total > 0 && passed*2 > total }
func (p *Prober) client(proxy *url.URL) *http.Client {
	tr := &http.Transport{DialContext: (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 15 * time.Second}).DialContext, Proxy: http.ProxyURL(proxy), ForceAttemptHTTP2: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: p.Timeout, MaxIdleConns: 10, MaxIdleConnsPerHost: 2, IdleConnTimeout: 15 * time.Second}
	if proxy == nil {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Transport: tr, Timeout: p.Timeout + 2*time.Second}
}
func statusOK(status int, policy string) bool {
	if policy == "2xx3xx" {
		return status >= 200 && status < 400
	}
	if strings.HasPrefix(policy, "exact:") {
		x, _ := strconv.Atoi(strings.TrimPrefix(policy, "exact:"))
		return status == x
	}
	return false
}
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	m := len(xs) / 2
	if len(xs)%2 == 1 {
		return xs[m]
	}
	return (xs[m-1] + xs[m]) / 2
}
