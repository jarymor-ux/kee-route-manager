package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/components"
)

type componentTestBackend struct {
	installed    int
	latest       int
	installCalls int
}

func (b *componentTestBackend) Installed(_ context.Context, id components.ID) (string, error) {
	b.installed++
	if id == components.Xray {
		return "1.0.0", nil
	}
	return "", nil
}
func (b *componentTestBackend) Latest(_ context.Context, id components.ID) (string, error) {
	b.latest++
	return "2.0.0", nil
}
func (b *componentTestBackend) Install(_ context.Context, _ components.ID, _ string) error {
	b.installCalls++
	return nil
}

func TestComponentAPIRequiresExplicitInstall(t *testing.T) {
	backend := &componentTestBackend{}
	m, err := components.NewManager("keenetic", 7*24*time.Hour, backend, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{components: m}
	w := httptest.NewRecorder()
	s.componentStatus(w, httptest.NewRequest(http.MethodGet, "/api/v1/components/status", nil), auth.Session{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"xray"`) || !strings.Contains(w.Body.String(), `"amneziawg"`) {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	initial := backend.latest
	w = httptest.NewRecorder()
	s.componentStatus(w, httptest.NewRequest(http.MethodGet, "/api/v1/components/status", nil), auth.Session{})
	if backend.latest != initial {
		t.Fatal("component status bypassed weekly cache")
	}
	w = httptest.NewRecorder()
	s.componentCheck(w, httptest.NewRequest(http.MethodPost, "/api/v1/components/check", nil), auth.Session{})
	if w.Code != 200 || backend.latest <= initial {
		t.Fatalf("manual check not forced: %d", w.Code)
	}
	if backend.installCalls != 0 {
		t.Fatal("component discovery installed software")
	}
	w = httptest.NewRecorder()
	s.componentInstall(w, httptest.NewRequest(http.MethodPost, "/api/v1/components/install", strings.NewReader(`{"id":"random"}`)), auth.Session{})
	if w.Code == 200 || backend.installCalls != 0 {
		t.Fatal("invalid component install accepted")
	}
}
