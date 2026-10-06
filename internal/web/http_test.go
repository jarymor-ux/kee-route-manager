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


type fakeSubscriptionController struct {
	*fakeController
	sources []config.Source
	saved   config.Source
	deleted string
	err     error
}

func (f *fakeSubscriptionController) SubscriptionSources() []config.Source {
	return f.sources
}
func (f *fakeSubscriptionController) SaveSubscription(_ context.Context, source config.Source) error {
	f.saved = source
	return f.err
}
func (f *fakeSubscriptionController) DeleteSubscription(_ context.Context, id string) error {
	f.deleted = id
	return f.err
}

func TestSubscriptionManagementHandlers(t *testing.T) {
	controller := &fakeSubscriptionController{
		fakeController: &fakeController{},
		sources: []config.Source{{
			ID:      "primary",
			Name:    "Primary",
			URL:     "https://example.invalid/sub?token=private",
			Enabled: true,
			Headers: map[string]string{"Authorization": "Bearer private"},
		}},
	}
	s := &Server{mgr: controller}

	w := httptest.NewRecorder()
	s.subscriptions(w, httptest.NewRequest(http.MethodGet, "/", nil), auth.Session{})
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list: code=%d cache=%q body=%s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"id":"primary"`) || !strings.Contains(w.Body.String(), `"headers":{"Authorization":"Bearer private"}`) {
		t.Fatalf("list body = %s", w.Body.String())
	}

	body := `{"id":"backup","name":"Backup","url":"https://backup.invalid/sub","enabled":true,"headers":{"X-Test":"value"}}`
	w = httptest.NewRecorder()
	s.saveSubscription(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), auth.Session{})
	if w.Code != http.StatusOK || controller.saved.ID != "backup" || controller.saved.Headers["X-Test"] != "value" {
		t.Fatalf("save: code=%d source=%#v body=%s", w.Code, controller.saved, w.Body.String())
	}

	w = httptest.NewRecorder()
	s.deleteSubscription(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"id":"backup"}`)), auth.Session{})
	if w.Code != http.StatusOK || controller.deleted != "backup" {
		t.Fatalf("delete: code=%d id=%q body=%s", w.Code, controller.deleted, w.Body.String())
	}

	controller.err = errors.New("private subscription=https://secret.invalid/token")
	w = httptest.NewRecorder()
	s.saveSubscription(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), auth.Session{})
	if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "secret.invalid") || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("secret leaked in rejection: %d %s", w.Code, w.Body.String())
	}
}
