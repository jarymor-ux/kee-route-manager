package bench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type ProbeResult struct {
	FailureClass string        `json:"failure_class,omitempty"`
	TargetID     string        `json:"target_id"`
	Success      bool          `json:"success"`
	Status       int           `json:"status"`
	Duration     time.Duration `json:"duration"`
	Bytes        int64         `json:"bytes"`
	Error        string        `json:"error,omitempty"`
}
type Prober struct {
	Timeout  time.Duration
	MaxBytes int64
	mu       sync.Mutex
	clients  map[string]*http.Client
}

func NewProber(timeout time.Duration, max int64) *Prober {
	return &Prober{Timeout: timeout, MaxBytes: max, clients: map[string]*http.Client{}}
}
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
	req.Header.Set("Accept-Encoding", "identity")
	start := time.Now()
	resp, e := p.client(proxy).Do(req)
	if e != nil {
		return ProbeResult{TargetID: t.ID, Duration: time.Since(start), Error: e.Error(), FailureClass: ClassifyProbeError(e)}
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
	results := make([]ProbeResult, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target config.Target) { defer wg.Done(); results[i] = p.Probe(ctx, proxy, target) }(i, target)
	}
	wg.Wait()
	passed := 0
	for _, r := range results {
		if r.Success {
			passed++
		}
	}
	return passed, len(targets), results
}
func Majority(passed, total int) bool { return total > 0 && passed*2 > total }
func (p *Prober) client(proxy *url.URL) *http.Client {
	key := "direct"
	if proxy != nil {
		key = proxy.String()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.clients == nil {
		p.clients = map[string]*http.Client{}
	}
	if client := p.clients[key]; client != nil {
		return client
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	tr := &http.Transport{DialContext: (&net.Dialer{Timeout: timeout, KeepAlive: 15 * time.Second}).DialContext, ForceAttemptHTTP2: true, TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout, MaxIdleConns: 10, MaxIdleConnsPerHost: 2, IdleConnTimeout: 15 * time.Second, DisableCompression: true}
	// WAN comparison must bypass HTTP_PROXY/HTTPS_PROXY supplied by the environment.
	if proxy != nil {
		tr.Proxy = http.ProxyURL(proxy)
	}
	client := &http.Client{Transport: tr, Timeout: timeout + 2*time.Second}
	p.clients[key] = client
	return client
}
func (p *Prober) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, client := range p.clients {
		client.CloseIdleConnections()
	}
	p.clients = map[string]*http.Client{}
}

// Quorum counts independent hostnames; multiple URLs on one host cannot satisfy it.
func Quorum(targets []config.Target, results []ProbeResult, quorum int) bool {
	if quorum < 2 {
		quorum = 2
	}
	successes := map[string]bool{}
	byID := map[string]bool{}
	for _, r := range results {
		byID[r.TargetID] = r.Success
	}
	for _, target := range targets {
		if !byID[target.ID] {
			continue
		}
		u, err := url.Parse(target.URL)
		if err == nil && u.Hostname() != "" {
			successes[strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")] = true
		}
	}
	return len(successes) >= quorum
}
func Classify(targets []config.Target, vpn, wan []ProbeResult, quorum int) string {
	independent := map[string]bool{}
	for _, target := range targets {
		u, err := url.Parse(target.URL)
		if err == nil {
			independent[strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")] = true
		}
	}
	if quorum < 2 {
		quorum = 2
	}
	if len(independent) < quorum {
		return "monitoring_inconclusive"
	}
	if Quorum(targets, vpn, quorum) {
		return "healthy"
	}
	if Quorum(targets, wan, quorum) {
		return "vpn_path_failed"
	}
	for _, kind := range []string{"dns_failed", "wan_failed"} {
		failures := append([]ProbeResult(nil), wan...)
		for i := range failures {
			failures[i].Success = failures[i].FailureClass == kind
		}
		if Quorum(targets, failures, quorum) {
			return kind
		}
	}
	for _, r := range vpn {
		if r.Success {
			return "target_failed"
		}
	}
	return "monitoring_inconclusive"
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

func ClassifyProbeError(err error) string {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns_failed"
	}
	if errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.ENETDOWN) {
		return "wan_failed"
	}
	return "monitoring_inconclusive"
}
