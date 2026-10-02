package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestCredentialsAndSessions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "credentials.json")
	if e := CreateCredentials(p, "admin", "correct horse battery staple"); e != nil {
		t.Fatal(e)
	}
	c, e := LoadCredentials(p)
	if e != nil {
		t.Fatal(e)
	}
	if !Verify(c, "admin", "correct horse battery staple") {
		t.Fatal("valid password rejected")
	}
	if Verify(c, "admin", "wrong password") {
		t.Fatal("invalid password accepted")
	}
	s := NewSessionStore(time.Minute)
	v, e := s.Create("admin", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "https://krm.local/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: v.ID})
	if _, e = ReadSession(r, s); e != nil {
		t.Fatal(e)
	}
}
