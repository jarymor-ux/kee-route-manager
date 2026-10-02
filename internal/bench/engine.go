package bench

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
)

type Progress func(stage string, current, total int, message string)
type Engine struct {
	cfg    config.Config
	runner *xray.BatchRunner
	prober *Prober
}

func New(c config.Config, r *xray.BatchRunner) *Engine {
	return &Engine{c, r, NewProber(c.Health.RequestTimeout.Duration, int64(c.Health.MaxResponseBytes))}
}
func (e *Engine) Run(ctx context.Context, nodes []model.Node, progress Progress) ([]model.Measurement, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes")
	}
	results := map[string]model.Measurement{}
	batchSize := e.cfg.Benchmark.BatchSize
	for start := 0; start < len(nodes); start += batchSize {
		end := start + batchSize
		if end > len(nodes) {
			end = len(nodes)
		}
		if progress != nil {
			progress("latency", start, len(nodes), fmt.Sprintf("nodes %d-%d", start+1, end))
		}
		batch, err := e.runner.Start(ctx, nodes[start:end])
		if err != nil {
			return nil, err
		}
		measured := e.measureBatch(ctx, nodes[start:end], batch.Proxies)
		batch.Stop()
		for _, m := range measured {
			results[m.NodeID] = m
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	healthy := []model.Measurement{}
	for _, m := range results {
		if m.Healthy {
			healthy = append(healthy, m)
		}
	}
	sort.Slice(healthy, func(i, j int) bool { return healthy[i].LatencyMS < healthy[j].LatencyMS })
	finalists := e.cfg.Benchmark.Finalists
	if finalists > len(healthy) {
		finalists = len(healthy)
	}
	if e.cfg.Benchmark.Speed.Enabled && finalists > 0 {
		ids := map[string]bool{}
		for _, m := range healthy[:finalists] {
			ids[m.NodeID] = true
		}
		selected := []model.Node{}
		for _, n := range nodes {
			if ids[n.ID] {
				selected = append(selected, n)
			}
		}
		batch, err := e.runner.Start(ctx, selected)
		if err != nil {
			return nil, err
		}
		for i, n := range selected {
			if progress != nil {
				progress("speed", i, finalists, n.Label)
			}
			m := results[n.ID]
			speed, err := e.speed(ctx, batch.Proxies[n.ID])
			if err != nil {
				m.Error = join(m.Error, "speed: "+err.Error())
			} else {
				m.SpeedMbps = speed
			}
			results[n.ID] = m
		}
		batch.Stop()
	}
	bestLatency := math.MaxFloat64
	bestSpeed := 0.0
	for _, m := range results {
		if m.Healthy && m.LatencyMS > 0 && m.LatencyMS < bestLatency {
			bestLatency = m.LatencyMS
		}
		if m.SpeedMbps > bestSpeed {
			bestSpeed = m.SpeedMbps
		}
	}
	if bestLatency == math.MaxFloat64 {
		bestLatency = 1
	}
	out := make([]model.Measurement, 0, len(nodes))
	for _, n := range nodes {
		m := results[n.ID]
		if !m.Healthy {
			m.Score = 999999
		} else {
			score := 600 * (m.LatencyMS / bestLatency)
			if bestSpeed > 0 && m.SpeedMbps > 0 {
				score += 400 * (bestSpeed / m.SpeedMbps)
			} else if e.cfg.Benchmark.Speed.Enabled {
				score += 4000
			}
			score += float64(m.Failures) * 250
			m.Score = math.Round(score)
		}
		results[n.ID] = m
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].LatencyMS < out[j].LatencyMS
		}
		return out[i].Score < out[j].Score
	})
	if progress != nil {
		progress("complete", len(out), len(out), "benchmark complete")
	}
	return out, nil
}
func (e *Engine) measureBatch(ctx context.Context, nodes []model.Node, proxies map[string]*url.URL) []model.Measurement {
	out := make([]model.Measurement, len(nodes))
	workers := e.cfg.Benchmark.LatencyWorkers
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, n := range nodes {
		i, n := i, n
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				out[i] = model.Measurement{NodeID: n.ID, Error: ctx.Err().Error(), CheckedAt: time.Now().UTC()}
				return
			}
			defer func() { <-sem }()
			out[i] = e.node(ctx, n.ID, proxies[n.ID])
		}()
	}
	wg.Wait()
	return out
}
func (e *Engine) node(ctx context.Context, id string, proxy *url.URL) model.Measurement {
	targets := e.cfg.TargetsByRole("score")
	latencies, responses := []float64{}, []float64{}
	successTargets, failures := 0, 0
	for _, t := range targets {
		reps := t.Weight * e.cfg.Benchmark.RequestsPerWeight
		if reps < 1 {
			reps = 1
		}
		samples := []float64{}
		successAttempts := 0
		for i := 0; i < reps; i++ {
			r := e.prober.Probe(ctx, proxy, t)
			if r.Success {
				ms := float64(r.Duration.Microseconds()) / 1000
				samples = append(samples, ms)
				responses = append(responses, ms)
				successAttempts++
			} else {
				failures++
			}
		}
		if Majority(successAttempts, reps) {
			successTargets++
			latencies = append(latencies, median(samples))
		}
	}
	healthy := Majority(successTargets, len(targets))
	errText := ""
	if !healthy {
		errText = fmt.Sprintf("score majority failed: %d/%d", successTargets, len(targets))
	}
	return model.Measurement{NodeID: id, LatencyMS: median(latencies), ResponseMS: median(responses), Failures: failures, Successes: successTargets, Healthy: healthy, Error: errText, CheckedAt: time.Now().UTC()}
}
func (e *Engine) speed(ctx context.Context, proxy *url.URL) (float64, error) {
	s := e.cfg.Benchmark.Speed
	if s.WarmupBytes > 0 {
		_, _, _ = e.download(ctx, proxy, int64(s.WarmupBytes))
	}
	size := int64(s.MinSampleBytes)
	values := []float64{}
	for i := 0; i < s.Repetitions; i++ {
		bps, d, err := e.download(ctx, proxy, size)
		if err != nil {
			return 0, err
		}
		values = append(values, bps*8/1e6)
		next := int64(bps * s.TargetDuration.Duration.Seconds())
		if next < int64(s.MinSampleBytes) {
			next = int64(s.MinSampleBytes)
		}
		if next > int64(s.MaxSampleBytes) {
			next = int64(s.MaxSampleBytes)
		}
		if d > 0 {
			size = next
		}
	}
	return median(values), nil
}
func (e *Engine) download(ctx context.Context, proxy *url.URL, size int64) (float64, time.Duration, error) {
	u := strings.ReplaceAll(e.cfg.Benchmark.Speed.URLTemplate, "{bytes}", fmt.Sprint(size))
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 2, MaxIdleConnsPerHost: 1}
	client := &http.Client{Transport: tr, Timeout: e.cfg.Benchmark.Speed.TargetDuration.Duration*4 + 20*time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "Kee-Route-Manager/1.0")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return 0, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, size+1))
	d := time.Since(start)
	if err != nil {
		return 0, d, err
	}
	if n < size*9/10 {
		return 0, d, fmt.Errorf("speed response too small: received %d of %d bytes", n, size)
	}
	return float64(n) / d.Seconds(), d, nil
}
func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
