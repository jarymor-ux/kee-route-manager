package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestProxyRequestTimeoutIncludesResponseBody(t *testing.T) {
	for _, path := range []string{"/api/v1/status", "/healthz"} {
		t.Run(path, func(t *testing.T) {
			canceled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(canceled)
			}))
			defer upstream.Close()
			c := config.Default()
			c.Web.TLS.Enabled = false
			c.UIProxy.Upstream = upstream.URL
			c.UIProxy.RequestTimeout = config.Dur(40 * time.Millisecond)
			h, err := ProxyHandler(c)
			if err != nil {
				t.Fatal(err)
			}
			front := httptest.NewServer(h)
			defer front.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r, err := http.NewRequestWithContext(ctx, http.MethodGet, front.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			response, err := front.Client().Do(r)
			if err == nil {
				_, err = io.ReadAll(response.Body)
				_ = response.Body.Close()
			}
			if elapsed := time.Since(start); elapsed >= time.Second {
				t.Fatalf("response body exceeded configured 40ms timeout: %v (%v)", elapsed, err)
			}
			if err == nil {
				t.Fatal("partial response was reported as complete")
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("upstream request not canceled")
			}
		})
	}
}
