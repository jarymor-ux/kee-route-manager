package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/core"
	"github.com/jarymor-ux/kee-route-manager/internal/operation"
)

type fakeController struct {
	Controller
	readyErr     error
	benchmarkErr error
}

func (f *fakeController) Status(context.Context) core.Status {
	return core.Status{Version: "test", XrayRunning: true}
}
func (f *fakeController) Readiness() error { return f.readyErr }
func (f *fakeController) RequestBenchmark(context.Context, string) (*operation.Operation, error) {
	if f.benchmarkErr != nil {
		return nil, f.benchmarkErr
	}
	return &operation.Operation{ID: "op-reserved", Status: "running"}, nil
}
func (f *fakeController) SystemLogs(context.Context, int) (string, error) { return "test log", nil }
func (f *fakeController) Diagnostics(context.Context) (string, error) {
	return "", errors.New("subscription=https://private/user?secret=token")
}
func TestHealthReadinessAndRequestID(t *testing.T) {
	f := &fakeController{}
	s := &Server{cfg: config.Default(), mgr: f}
	h := s.LocalHandler()
	for _, code := range []int{200, 503} {
		if code == 503 {
			f.readyErr = errors.New("not reconciled")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
		if w.Code != code {
			t.Fatalf("health=%d want=%d", w.Code, code)
		}
		if len(w.Header().Get("X-Request-ID")) != 32 {
			t.Fatal("missing server request ID")
		}
		if strings.Contains(w.Body.String(), "not reconciled") {
			t.Fatal("internal readiness error leaked")
		}
	}
}
func TestBenchmarkReturnsReservedID(t *testing.T) {
	f := &fakeController{}
	s := &Server{cfg: config.Default(), mgr: f}
	h := s.LocalHandler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/actions/benchmark", nil))
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"operation_id":"op-reserved"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	f.benchmarkErr = operation.ErrBusy
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/actions/benchmark", nil))
	if w.Code != 409 {
		t.Fatalf("busy=%d", w.Code)
	}
}
func TestSystemLogsContractAndErrorHiding(t *testing.T) {
	s := &Server{mgr: &fakeController{}}
	w := httptest.NewRecorder()
	s.logs(w, httptest.NewRequest("GET", "/", nil), auth.Session{})
	if !strings.Contains(w.Body.String(), `"output":"test log"`) {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	s.diagnostics(w, httptest.NewRequest("GET", "/", nil), auth.Session{})
	if strings.Contains(w.Body.String(), "private") || w.Code != 500 {
		t.Fatal(w.Body.String())
	}
}
func TestRejectTrailingBody(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"index":1} {"index":2}`))
	var q struct {
		Index int `json:"index"`
	}
	if decodeBody(w, r, &q) == nil || w.Code != http.StatusBadRequest {
		t.Fatal("accepted multiple JSON values")
	}
}
