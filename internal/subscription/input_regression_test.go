package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/redact"
)

func TestRetryBackoffWithoutUsableCache(t *testing.T) {
	for _, cacheKind := range []string{"missing", "malformed", "healthy"} {
		t.Run(cacheKind, func(t *testing.T) {
			var requests atomic.Int64
			var available atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				if !available.Load() {
					http.Error(w, "outage", http.StatusServiceUnavailable)
					return
				}
				_, _ = w.Write([]byte(reality))
			}))
			defer server.Close()
			cfg := config.Default().Subscriptions
			cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
			f := New(cfg, []config.Duration{config.Dur(time.Minute)}, t.TempDir())
			now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
			f.now = func() time.Time { return now }
			var result Result
			if cacheKind == "healthy" {
				available.Store(true)
				result = f.FetchAll(context.Background(), nil, true)
				available.Store(false)
				now = now.Add(time.Hour)
			}
			result = f.FetchAll(context.Background(), result.States, true)
			if cacheKind == "malformed" {
				if err := os.WriteFile(filepath.Join(f.dir, "provider.json"), []byte("invalid cache"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			first := result.States["provider"]
			attempts := requests.Load()
			now = now.Add(15 * time.Second)
			for _, force := range []bool{false, true} {
				result = f.FetchAll(context.Background(), result.States, force)
				state := result.States["provider"]
				if requests.Load() != attempts || state.ConsecutiveFailures != first.ConsecutiveFailures || state.RetryLevel != first.RetryLevel || !state.LastAttemptAt.Equal(first.LastAttemptAt) || !state.NextRetryAt.Equal(first.NextRetryAt) {
					t.Errorf("force=%t attempted during backoff: first=%+v current=%+v requests=%d->%d", force, first, state, attempts, requests.Load())
				}
				if state.UsingCache != (cacheKind == "healthy") {
					t.Errorf("unexpected cache status %+v", state)
				}
			}
			// A due retry remains able to recover, including after an initial outage.
			available.Store(true)
			now = result.States["provider"].NextRetryAt.Add(time.Second)
			before := requests.Load()
			result = f.FetchAll(context.Background(), result.States, true)
			if requests.Load() != before+1 || len(result.Nodes) != 1 || result.States["provider"].ConsecutiveFailures != 0 || !result.States["provider"].NextRetryAt.IsZero() {
				t.Fatalf("due retry did not recover: %+v", result)
			}
		})
	}
}

func TestSubscriptionPathSecretsAreRedacted(t *testing.T) {
	cfg := config.Default().Subscriptions
	cfg.Sources = []config.Source{{ID: "provider", URL: "http://127.0.0.1:0/sub/SYNTHETIC_PATH_TOKEN?token=SYNTHETIC_QUERY_TOKEN", Enabled: true}}
	f := New(cfg, nil, t.TempDir())
	result := f.FetchAll(context.Background(), nil, true)
	if len(result.Errors) != 1 {
		t.Fatalf("expected transport failure: %+v", result)
	}
	for _, value := range []string{result.Errors[0].Error(), result.States["provider"].LastError, redact.Diagnostics(result.States["provider"].LastError)} {
		if strings.Contains(value, "SYNTHETIC_PATH_TOKEN") || strings.Contains(value, "SYNTHETIC_QUERY_TOKEN") {
			t.Errorf("subscription secret exposed: %q", value)
		}
		if !strings.Contains(value, "connect") {
			t.Errorf("transport category missing: %q", value)
		}
	}
}

func TestFileSourcesRequireRegularFilesAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscription")
	if err := os.WriteFile(path, []byte(reality), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Subscriptions
	cfg.RequestTimeout = config.Dur(40 * time.Millisecond)
	f := New(cfg, nil, t.TempDir())
	source := config.Source{ID: "provider", URL: "file://" + path, Enabled: true}
	body, err := f.download(context.Background(), source)
	if err != nil || string(body) != reality {
		t.Fatalf("regular file failed: %q %v", body, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.download(ctx, source); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled regular source: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel = context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	go func() { _, err := f.download(ctx, source); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("FIFO accepted")
		}
	case <-time.After(150 * time.Millisecond):
		// Release the defective implementation so the regression itself leaks no worker.
		writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write([]byte(reality))
		_ = writer.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("FIFO worker could not be released")
		}
		t.Error("FIFO remained blocked after deadline")
	}
	// Directories and oversized regular files also remain invalid inputs.
	source.URL = "file://" + filepath.Dir(path)
	if _, err := f.download(context.Background(), source); err == nil {
		t.Error("directory accepted")
	}
	source.URL = "file://" + filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(strings.TrimPrefix(source.URL, "file://"), []byte(reality), 0600); err != nil {
		t.Fatal(err)
	}
	f.cfg.MaxResponseBytes = 1
	if _, err := f.download(context.Background(), source); err == nil {
		t.Error("byte limit bypassed")
	}
}

type cancelOnRead struct {
	cancel context.CancelFunc
}

func (r cancelOnRead) Read(p []byte) (int, error) {
	r.cancel()
	return copy(p, reality), nil
}

func TestSubscriptionReadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := limited(ctx, cancelOnRead{cancel}, 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bytes accepted: %v", err)
	}
	if body, err := limited(context.Background(), strings.NewReader(reality), 1024); err != nil || string(body) != reality {
		t.Fatalf("ordinary bounded read failed: %q %v", body, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.RequestTimeout = config.Dur(40 * time.Millisecond)
	_, err := New(cfg, nil, t.TempDir()).download(context.Background(), config.Source{URL: server.URL})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HTTP streaming deadline: %v", err)
	}
}

func TestPayloadPreservesALPNCommas(t *testing.T) {
	raw := strings.Replace(reality, "pbk=abc", "alpn=h2,http/1.1&pbk=abc", 1)
	direct, err := ParseVLESS(raw, "provider")
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]string{
		"plain":         raw,
		"base64":        base64.StdEncoding.EncodeToString([]byte(raw)),
		"encoded comma": strings.Replace(raw, ",", "%2C", 1),
	} {
		t.Run(name, func(t *testing.T) {
			nodes, err := ParsePayload([]byte(payload), "provider")
			if err != nil || len(nodes) != 1 {
				t.Fatalf("nodes=%d err=%v", len(nodes), err)
			}
			if !reflect.DeepEqual(nodes[0], direct) || !reflect.DeepEqual(nodes[0].ALPN, []string{"h2", "http/1.1"}) {
				t.Fatalf("URI semantics changed: %+v", nodes[0])
			}
		})
	}
	// Commas separating complete URIs remain supported, including mixed case schemes.
	nodes, err := ParsePayload([]byte(raw+","+strings.Replace(ws, "vless://", "VLESS://", 1)), "provider")
	if err != nil || len(nodes) != 2 {
		t.Errorf("comma-separated URI list: nodes=%d err=%v", len(nodes), err)
	}
	nodes, err = ParsePayload([]byte(raw+", \n"+ws), "provider")
	if err != nil || len(nodes) != 2 || !reflect.DeepEqual(nodes[0], direct) {
		t.Errorf("comma and whitespace URI boundary changed semantics: nodes=%+v err=%v", nodes, err)
	}
	if _, err := ParsePayload([]byte(strings.Replace(ws, "type=ws", "type=grpc", 1)), "provider"); err == nil {
		t.Error("unsupported transport accepted")
	}
}

func TestCommaURIListPreservesUnicodeLabels(t *testing.T) {
	for _, label := range []string{"İ-node", "invalid-\xff-label"} {
		first := strings.Split(reality, "#")[0] + "#" + label
		expected, err := ParseVLESS(first, "provider")
		if err != nil {
			t.Fatal(err)
		}
		nodes, err := ParsePayload([]byte(first+","+ws), "provider")
		if err != nil || len(nodes) != 2 || !reflect.DeepEqual(nodes[0], expected) {
			t.Fatalf("URI byte boundary changed for %q: %+v %v", label, nodes, err)
		}
	}
}
