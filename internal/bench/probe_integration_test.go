package bench

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/xray"
)

func TestProbeAppliesPolicyResponseBoundAndExplicitProxy(t *testing.T) {
	requests := make(chan string, 4)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.String()
		if r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("User-Agent") != "Kee-Route-Manager/1.0" {
			t.Error("probe request headers missing")
		}
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, strings.Repeat("x", 128))
	}))
	defer proxy.Close()
	endpoint, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	p := NewProber(time.Second, 32)
	defer p.Close()
	for _, tc := range []struct {
		name, policy string
		limit        int
		passed       bool
		bytes        int64
	}{
		{"target-limit", "exact:418", 8, true, 8},
		{"default-limit", "exact:418", 0, true, 32},
		{"status-mismatch", "2xx3xx", 8, false, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := p.Probe(context.Background(), endpoint, config.Target{ID: "target", URL: "http://target.invalid/probe", Policy: tc.policy, MaxResponseBytes: config.ByteSize(tc.limit)})
			if result.TargetID != "target" || result.Success != tc.passed || result.Status != http.StatusTeapot || result.Bytes != tc.bytes || result.Duration <= 0 {
				t.Fatalf("unexpected result: %+v", result)
			}
			if !tc.passed && result.Error == "" {
				t.Fatal("HTTP policy failure lacks diagnostic")
			}
			if got := <-requests; got != "http://target.invalid/probe" {
				t.Fatalf("request bypassed explicit proxy: %q", got)
			}
		})
	}
}

func TestCheckMajorityProbesConcurrentlyAndKeepsTargetOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan string, 3)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.URL.Path
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		if r.URL.Path == "/b" {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	p := NewProber(time.Second, 16)
	defer p.Close()
	targets := []config.Target{{ID: "a", URL: srv.URL + "/a", Policy: "exact:204"}, {ID: "b", URL: srv.URL + "/b", Policy: "exact:204"}, {ID: "c", URL: srv.URL + "/c", Policy: "exact:204"}}
	type outcome struct {
		passed, total int
		results       []ProbeResult
	}
	done := make(chan outcome, 1)
	go func() {
		passed, total, results := p.CheckMajority(ctx, nil, targets)
		done <- outcome{passed, total, results}
	}()
	for range targets {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("probes did not overlap")
		}
	}
	close(release)
	select {
	case got := <-done:
		if got.passed != 2 || got.total != 3 || !Majority(got.passed, got.total) {
			t.Fatalf("wrong majority: %+v", got)
		}
		for i, result := range got.results {
			if result.TargetID != targets[i].ID {
				t.Fatal("completion order changed target identity")
			}
		}
	case <-ctx.Done():
		t.Fatal("probes failed to finish")
	}
}

func TestProbeCancellationAndTruncatedResponseAreFailures(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/truncated" {
			w.Header().Set("Content-Length", "20")
			_, _ = io.WriteString(w, "short")
			return
		}
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	p := NewProber(time.Second, 64)
	defer p.Close()
	short := p.Probe(context.Background(), nil, config.Target{ID: "short", URL: srv.URL + "/truncated", Policy: "2xx3xx"})
	if short.Success || short.Bytes != 5 || short.Error == "" {
		t.Fatalf("truncated body accepted: %+v", short)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan ProbeResult, 1)
	go func() {
		done <- p.Probe(ctx, nil, config.Target{ID: "cancelled", URL: srv.URL + "/wait", Policy: "2xx3xx"})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request never started")
	}
	cancel()
	select {
	case result := <-done:
		if result.Success || result.FailureClass != "monitoring_inconclusive" || result.Error == "" {
			t.Fatalf("cancellation misclassified: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation failed to interrupt probe")
	}
}

type failedBatchRunner struct{ err error }

func (r failedBatchRunner) Start(context.Context, []model.Node) (*xray.Batch, error) {
	return nil, r.err
}

func TestBenchmarkRejectsEmptyInputAndReportsBatchFailure(t *testing.T) {
	cfg := config.Default()
	engine := New(cfg, nil)
	if results, err := engine.Run(context.Background(), nil, nil); err == nil || len(results) != 0 {
		t.Fatalf("empty benchmark accepted: %+v %v", results, err)
	}
	failure := errors.New("temporary tunnel failed")
	engine.runner = failedBatchRunner{failure}
	completed := false
	results, err := engine.Run(context.Background(), []model.Node{{ID: "node"}}, func(stage string, _, _ int, _ string) {
		if stage == "complete" {
			completed = true
		}
	})
	if !errors.Is(err, failure) || len(results) != 0 || completed {
		t.Fatalf("batch failure reported as completed measurements: %+v %v complete=%v", results, err, completed)
	}
}
