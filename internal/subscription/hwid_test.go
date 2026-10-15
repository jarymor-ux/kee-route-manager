package subscription

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestFetcherDiagnosesExplicitHWIDRejections(t *testing.T) {
	const requiresIdentity = "subscription provider requires HWID support; configure a stable X-HWID header"
	const reachedLimit = "subscription provider device limit reached; remove an existing device or contact the provider"
	dummy := "vless://00000000-0000-0000-0000-000000000000@0.0.0.0:1?encryption=none&type=tcp&security=none#App%20not%20supported"
	bodies := map[string]string{
		"base64": base64.StdEncoding.EncodeToString([]byte(dummy)),
		"json":   `{"remarks":"App not supported","outbounds":[{"protocol":"blackhole"}]}`,
	}
	for _, tc := range []struct {
		name    string
		headers http.Header
		want    string
	}{
		{"missing_identity", http.Header{"X-Hwid-Not-Supported": {"true"}}, requiresIdentity},
		{"missing_identity_trimmed", http.Header{"X-Hwid-Not-Supported": {" TrUe "}}, requiresIdentity},
		{"missing_identity_numeric", http.Header{"X-Hwid-Not-Supported": {" 1 "}}, requiresIdentity},
		{"device_limit", http.Header{"X-Hwid-Max-Devices-Reached": {"true"}}, reachedLimit},
		{"device_limit_numeric", http.Header{"X-Hwid-Max-Devices-Reached": {" 1 "}}, reachedLimit},
		{"device_limit_multiple_values", http.Header{"X-Hwid-Max-Devices-Reached": {"false", " TRUE "}}, reachedLimit},
		{"limit_precedes_identity", http.Header{"X-Hwid-Max-Devices-Reached": {"true"}, "X-Hwid-Not-Supported": {"true"}}, reachedLimit},
	} {
		for format, body := range bodies {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					for name, values := range tc.headers {
						for _, value := range values {
							w.Header().Add(name, value)
						}
					}
					// Response metadata and body must never leak into diagnostics.
					w.Header().Set("X-Hwid-Device-Info", "fixture-private-device")
					_, _ = w.Write([]byte(body))
				}))
				defer server.Close()
				cfg := config.Default().Subscriptions
				cfg.CacheEnabled = false
				cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
				result := New(cfg, nil, t.TempDir()).FetchAll(context.Background(), nil, false)
				state := result.States["provider"]
				if len(result.Nodes) != 0 || len(result.Errors) != 1 || result.Errors[0].Error() != "provider: "+tc.want || state.LastError != tc.want || state.Status != "unavailable" || state.UsingCache {
					t.Fatalf("unexpected rejection: nodes=%d, errors=%v, state=%+v", len(result.Nodes), result.Errors, state)
				}
			})
		}
	}
}

func TestFetcherDoesNotTreatHWIDFeatureFlagsAsRejections(t *testing.T) {
	for _, flags := range []http.Header{
		{"X-Hwid-Limit": {"true"}, "X-Hwid-Active": {"true"}},
		{"X-Hwid-Not-Supported": {"false"}, "X-Hwid-Max-Devices-Reached": {"0"}},
		{"X-Hwid-Not-Supported": {"enabled"}, "X-Hwid-Max-Devices-Reached": {"yes"}},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for name, values := range flags {
				w.Header()[name] = values
			}
			_, _ = w.Write([]byte(reality))
		}))
		cfg := config.Default().Subscriptions
		cfg.CacheEnabled = false
		cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
		result := New(cfg, nil, t.TempDir()).FetchAll(context.Background(), nil, false)
		server.Close()
		if len(result.Nodes) != 1 || len(result.Errors) != 0 || result.States["provider"].Status != "healthy" {
			t.Fatalf("feature flags rejected real nodes: nodes=%d, errors=%v", len(result.Nodes), result.Errors)
		}
	}
}

func TestFetcherUnflaggedDummyRetainsParserError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte("App not supported"))))
	}))
	defer server.Close()
	cfg := config.Default().Subscriptions
	cfg.CacheEnabled = false
	cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true}}
	result := New(cfg, nil, t.TempDir()).FetchAll(context.Background(), nil, false)
	if len(result.Nodes) != 0 || len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Error(), "no supported") || strings.Contains(result.Errors[0].Error(), "HWID") {
		t.Fatalf("unflagged response incorrectly classified: %v", result.Errors)
	}
}

func TestFetcherPreservesConfiguredHWIDWithoutGeneratingIdentity(t *testing.T) {
	for _, configured := range []bool{true, false} {
		name := "unconfigured"
		if configured {
			name = "configured"
		}
		t.Run(name, func(t *testing.T) {
			requests := make(chan http.Header, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.Header.Clone()
				_, _ = w.Write([]byte(reality))
			}))
			defer server.Close()
			headers := map[string]string{}
			if configured {
				headers = map[string]string{"User-Agent": "Happ/4.11.0", "X-HWID": "fixture-stable-identity", "X-Device-OS": "iOS"}
			}
			cfg := config.Default().Subscriptions
			cfg.CacheEnabled = false
			cfg.Sources = []config.Source{{ID: "provider", URL: server.URL, Enabled: true, Headers: headers}}
			fetcher := New(cfg, nil, t.TempDir())
			for range 3 {
				result := fetcher.FetchAll(context.Background(), nil, false)
				if len(result.Nodes) != 1 || len(result.Errors) != 0 {
					t.Fatalf("fetch failed: %v", result.Errors)
				}
				request := <-requests
				wantAgent := "Kee-Route-Manager/1.0"
				if configured {
					wantAgent = headers["User-Agent"]
				}
				if request.Get("X-Hwid") != headers["X-HWID"] || request.Get("X-Device-OS") != headers["X-Device-OS"] || request.Get("User-Agent") != wantAgent {
					t.Fatal("configured identity was changed or an unconfigured identity was generated")
				}
			}
			if !reflect.DeepEqual(fetcher.Sources()[0].Headers, headers) {
				t.Fatal("fetcher mutated private source headers")
			}
		})
	}
}
