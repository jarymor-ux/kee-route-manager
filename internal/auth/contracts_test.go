package auth

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPBKDF2SHA256KnownAnswers(t *testing.T) {
	for _, tc := range []struct {
		iterations int
		want       string
	}{
		{1, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{2, "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
	} {
		got := pbkdf2SHA256([]byte("password"), []byte("salt"), tc.iterations, 32)
		if hex.EncodeToString(got) != tc.want {
			t.Fatalf("iterations=%d: %x", tc.iterations, got)
		}
	}
}

func TestSessionExpirationRevocationAndCapacity(t *testing.T) {
	s := NewSessionStore(time.Hour)
	one, err := s.Create("admin", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.Create("admin", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID == two.ID || one.CSRF == two.CSRF || one.ID == one.CSRF || len(one.ID) < 40 {
		t.Fatal("session tokens are not independent")
	}
	s.Delete(one.ID)
	if _, ok := s.Get(one.ID); ok {
		t.Fatal("revoked session accepted")
	}
	if _, ok := s.Get(two.ID); !ok {
		t.Fatal("revocation affected another session")
	}
	two.ExpiresAt = time.Now().Add(-time.Second)
	s.sessions[two.ID] = two
	if _, ok := s.Get(two.ID); ok {
		t.Fatal("expired session accepted")
	}
	if len(s.sessions) != 0 {
		t.Fatal("expired sessions retained")
	}
	for i := 0; i < 1024; i++ {
		if _, err := s.Create("admin", "192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create("admin", "192.0.2.1"); err == nil {
		t.Fatal("unbounded session growth accepted")
	}
	for id, value := range s.sessions {
		value.ExpiresAt = time.Now().Add(-time.Second)
		s.sessions[id] = value
	}
	if _, err := s.Create("admin", "192.0.2.1"); err != nil || len(s.sessions) != 1 {
		t.Fatalf("expired sessions blocked new login: %v", err)
	}
}

func TestSessionCookieBoundary(t *testing.T) {
	s := NewSessionStore(time.Hour)
	v, err := s.Create("admin", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, secure := range []bool{false, true} {
		w := httptest.NewRecorder()
		SetCookie(w, v, secure)
		cookie := w.Result().Cookies()[0]
		if cookie.Name != CookieName || cookie.Value != v.ID || !cookie.HttpOnly || cookie.Secure != secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge <= 0 {
			t.Fatalf("unsafe session cookie: %+v", cookie)
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(cookie)
		if got, err := ReadSession(r, s); err != nil || got.ID != v.ID {
			t.Fatalf("cookie session=%+v err=%v", got, err)
		}
		w = httptest.NewRecorder()
		ClearCookie(w, secure)
		cookie = w.Result().Cookies()[0]
		if cookie.Value != "" || cookie.MaxAge != -1 || !cookie.HttpOnly || cookie.Secure != secure || cookie.Path != "/" {
			t.Fatalf("incomplete cookie removal: %+v", cookie)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	if _, err := ReadSession(r, s); err == nil {
		t.Fatal("missing cookie authenticated")
	}
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "forged"})
	if _, err := ReadSession(r, s); err == nil {
		t.Fatal("forged cookie authenticated")
	}
	for _, addr := range []string{"192.0.2.1:8000", "[2001:db8::1]:8000", "unparsed-peer"} {
		r.RemoteAddr = addr
		r.Header.Set("X-Forwarded-For", "attacker-controlled")
		got := RemoteIP(r)
		if got == "attacker-controlled" || (addr == "192.0.2.1:8000" && got != "192.0.2.1") || (addr == "[2001:db8::1]:8000" && got != "2001:db8::1") || (addr == "unparsed-peer" && got != addr) {
			t.Fatalf("peer=%s got=%s", addr, got)
		}
	}
}

func TestInvalidCredentialsDoNotOverwriteExistingSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte("existing secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ user, password string }{{"ab", "valid-password"}, {"admin\nname", "valid-password"}, {strings.Repeat("a", 65), "valid-password"}, {"admin", "short"}, {"admin", strings.Repeat("x", 1025)}} {
		if err := CreateCredentials(path, tc.user, tc.password); err == nil {
			t.Fatal("invalid credentials accepted")
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != "existing secret" {
			t.Fatal("invalid credentials replaced existing secret")
		}
	}
	if _, err := LoadCredentials(path); err == nil {
		t.Fatal("invalid credentials file accepted")
	}
	if _, err := LoadCredentials(path + "-missing"); err == nil {
		t.Fatal("missing credentials accepted")
	}
}
