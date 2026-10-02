package bench

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
)

type parallelTestRunner struct {
	proxies map[string]*url.URL
	starts  [][]string
}

func (r *parallelTestRunner) Start(ctx context.Context, nodes []model.Node) (*xray.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	proxies := make(map[string]*url.URL, len(nodes))
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		proxies[n.ID] = r.proxies[n.ID]
		ids = append(ids, n.ID)
	}
	r.starts = append(r.starts, ids)
	return &xray.Batch{Proxies: proxies}, nil
}

type parallelTestRequests struct {
	mu              sync.Mutex
	latency         int
	expectedLatency int
	earlySpeed      bool
	active, peak    int
	sizes           map[string][]int
}

func (r *parallelTestRequests) proxy(t *testing.T, id string, healthy bool, download func(http.ResponseWriter, *http.Request, int)) *url.URL {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/latency" {
			r.mu.Lock()
			r.latency++
			r.mu.Unlock()
			if !healthy {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			_, _ = w.Write([]byte("probe"))
			return
		}
		size, err := strconv.Atoi(req.URL.Query().Get("bytes"))
		if req.URL.Path != "/speed" || err != nil || size < 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		if r.latency != r.expectedLatency {
			r.earlySpeed = true
		}
		r.active++
		if r.active > r.peak {
			r.peak = r.active
		}
		r.sizes[id] = append(r.sizes[id], size)
		r.mu.Unlock()
		defer func() {
			r.mu.Lock()
			r.active--
			r.mu.Unlock()
		}()
		if download != nil {
			download(w, req, size)
			return
		}
		_, _ = w.Write(make([]byte, size))
	}))
	t.Cleanup(s.Close)
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func parallelTestConfig(nodes int) config.Config {
	c := config.Default()
	c.Benchmark.BatchSize = nodes
	c.Benchmark.LatencyWorkers = 2
	c.Benchmark.RequestsPerWeight = 1
	c.Benchmark.Finalists = nodes
	c.Health.RequestTimeout = config.Dur(5 * time.Second)
	c.Targets = []config.Target{{ID: "score", URL: "http://score.invalid/latency", Role: "score", Weight: 1, Policy: "2xx3xx"}}
	c.Benchmark.Speed.Enabled = true
	c.Benchmark.Speed.URLTemplate = "http://download.invalid/speed?bytes={bytes}"
	c.Benchmark.Speed.WarmupBytes = 8
	c.Benchmark.Speed.MinSampleBytes = 16
	c.Benchmark.Speed.MaxSampleBytes = 64
	// Ensure the adaptive next sample clamps to the maximum without timing assertions.
	c.Benchmark.Speed.TargetDuration = config.Dur(time.Hour)
	c.Benchmark.Speed.Repetitions = 3
	return c
}

func parallelTestEngine(c config.Config, runner *parallelTestRunner) *Engine {
	return &Engine{cfg: c, runner: runner, prober: NewProber(c.Health.RequestTimeout.Duration, int64(c.Health.MaxResponseBytes))}
}

type parallelTestResult struct {
	measurements []model.Measurement
	err          error
}

func awaitParallelResult(t *testing.T, ctx context.Context, done <-chan parallelTestResult) parallelTestResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		t.Fatal("benchmark did not finish before test deadline")
		return parallelTestResult{}
	}
}

func TestSpeedWorkersOverlapAfterLatencyAndPreserveSamples(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nodes := []model.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "unhealthy"}}
	c := parallelTestConfig(len(nodes))
	c.Benchmark.Finalists = 3
	if c.Benchmark.Speed.Workers != 2 {
		t.Fatalf("default speed workers = %d, want 2", c.Benchmark.Speed.Workers)
	}
	requests := &parallelTestRequests{expectedLatency: len(nodes), sizes: map[string][]int{}}
	firstSamples := make(chan string, len(nodes))
	release := make(chan struct{})
	runner := &parallelTestRunner{proxies: map[string]*url.URL{}}
	for _, n := range nodes {
		id := n.ID
		runner.proxies[id] = requests.proxy(t, id, id != "unhealthy", func(w http.ResponseWriter, req *http.Request, size int) {
			if size == 16 {
				firstSamples <- id
				select {
				case <-release:
				case <-req.Context().Done():
					return
				}
			}
			_, _ = w.Write(make([]byte, size))
		})
	}
	var progressActive atomic.Int32
	var progressMu sync.Mutex
	var counts []int
	done := make(chan parallelTestResult, 1)
	go func() {
		measurements, err := parallelTestEngine(c, runner).Run(ctx, nodes, func(stage string, current, total int, _ string) {
			if progressActive.Add(1) != 1 {
				t.Error("progress callbacks overlap")
			}
			defer progressActive.Add(-1)
			if stage == "speed" {
				progressMu.Lock()
				counts = append(counts, current)
				progressMu.Unlock()
				if total != 3 {
					t.Errorf("speed total = %d, want 3", total)
				}
			}
		})
		done <- parallelTestResult{measurements, err}
	}()
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-firstSamples:
			seen[id] = true
		case <-ctx.Done():
			t.Fatal("two speed samples did not overlap before test deadline")
		}
	}
	if len(seen) != 2 {
		t.Fatalf("overlap used %d distinct nodes, want 2", len(seen))
	}
	close(release)
	result := awaitParallelResult(t, ctx, done)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(runner.starts) != 2 || len(runner.starts[1]) != 3 {
		t.Fatalf("latency/speed batches = %v", runner.starts)
	}
	selected := map[string]bool{}
	for _, id := range runner.starts[1] {
		if id == "unhealthy" {
			t.Fatal("unhealthy node selected for speed benchmark")
		}
		selected[id] = true
	}
	requests.mu.Lock()
	defer requests.mu.Unlock()
	if requests.earlySpeed || requests.peak != 2 {
		t.Fatalf("early speed = %v, peak downloads = %d; want false, 2", requests.earlySpeed, requests.peak)
	}
	for _, n := range nodes {
		want := []int{8, 16, 64, 64}
		if !selected[n.ID] {
			want = nil
		}
		if !reflect.DeepEqual(requests.sizes[n.ID], want) {
			t.Errorf("%s samples = %v, want %v", n.ID, requests.sizes[n.ID], want)
		}
	}
	progressMu.Lock()
	defer progressMu.Unlock()
	if len(counts) > 0 && counts[0] == 0 {
		counts = counts[1:]
	}
	if !reflect.DeepEqual(counts, []int{1, 2, 3}) {
		t.Fatalf("speed completed counts = %v, want [1 2 3]", counts)
	}
}

func TestSpeedWorkerOneRetainsSerialDownloads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nodes := []model.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	c := parallelTestConfig(len(nodes))
	c.Benchmark.Speed.Workers = 1
	requests := &parallelTestRequests{expectedLatency: len(nodes), sizes: map[string][]int{}}
	runner := &parallelTestRunner{proxies: map[string]*url.URL{}}
	for _, n := range nodes {
		runner.proxies[n.ID] = requests.proxy(t, n.ID, true, nil)
	}
	measurements, err := parallelTestEngine(c, runner).Run(ctx, nodes, nil)
	if err != nil {
		t.Fatal(err)
	}
	requests.mu.Lock()
	defer requests.mu.Unlock()
	if requests.peak != 1 || len(measurements) != len(nodes) {
		t.Fatalf("peak = %d, measurements = %d; want 1, %d", requests.peak, len(measurements), len(nodes))
	}
	for _, m := range measurements {
		if m.SpeedMbps <= 0 || m.Error != "" {
			t.Errorf("bad measurement: %+v", m)
		}
	}
}

func TestSpeedCancellationDoesNotStartQueuedDownloads(t *testing.T) {
	deadline, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(deadline)
	defer cancel()
	nodes := []model.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	c := parallelTestConfig(len(nodes))
	c.Benchmark.Speed.WarmupBytes = 0
	c.Benchmark.Speed.Repetitions = 1
	requests := &parallelTestRequests{expectedLatency: len(nodes), sizes: map[string][]int{}}
	started := make(chan string, len(nodes))
	runner := &parallelTestRunner{proxies: map[string]*url.URL{}}
	for _, n := range nodes {
		id := n.ID
		runner.proxies[id] = requests.proxy(t, id, true, func(_ http.ResponseWriter, req *http.Request, _ int) {
			started <- id
			<-req.Context().Done()
		})
	}
	done := make(chan parallelTestResult, 1)
	go func() {
		measurements, err := parallelTestEngine(c, runner).Run(ctx, nodes, nil)
		done <- parallelTestResult{measurements, err}
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-deadline.Done():
			t.Fatal("two speed requests did not start")
		}
	}
	cancel()
	result := awaitParallelResult(t, deadline, done)
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", result.err)
	}
	requests.mu.Lock()
	defer requests.mu.Unlock()
	count := 0
	for _, sizes := range requests.sizes {
		count += len(sizes)
	}
	if count != 2 || requests.peak != 2 {
		t.Fatalf("started %d downloads with peak %d, want exactly 2", count, requests.peak)
	}
}

func TestSpeedDownloadErrorOnlyAffectsItsNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nodes := []model.Node{{ID: "good"}, {ID: "bad"}}
	c := parallelTestConfig(len(nodes))
	c.Benchmark.Speed.WarmupBytes = 0
	requests := &parallelTestRequests{expectedLatency: len(nodes), sizes: map[string][]int{}}
	runner := &parallelTestRunner{proxies: map[string]*url.URL{}}
	for _, n := range nodes {
		id := n.ID
		runner.proxies[id] = requests.proxy(t, id, true, func(w http.ResponseWriter, _ *http.Request, size int) {
			if id == "bad" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(make([]byte, size))
		})
	}
	measurements, err := parallelTestEngine(c, runner).Run(ctx, nodes, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range measurements {
		if !m.Healthy {
			t.Errorf("speed failure changed latency health: %+v", m)
		}
		switch m.NodeID {
		case "good":
			if m.Error != "" || m.SpeedMbps <= 0 {
				t.Errorf("good node affected by other download: %+v", m)
			}
		case "bad":
			if m.SpeedMbps != 0 || !strings.Contains(m.Error, fmt.Sprint("HTTP ", http.StatusServiceUnavailable)) {
				t.Errorf("missing node-specific download error: %+v", m)
			}
		}
	}
}
