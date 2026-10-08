package web

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestUpdateChannelRejectsUnauthorizedAndMalformedBeforeLauncher(t *testing.T) {
	for _, kind := range []string{"cookie", "csrf", "origin", "method", "disabled", "unknown", "trailing", "null", "type", "channel"} {
		t.Run(kind, func(t *testing.T) {
			s, session := actionServer(t, &fakeController{})
			s.cfg.Update.Enabled = true
			var calls atomic.Int32
			s.cfg.Update.LauncherSocket = launcherSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) }))
			body, want := `{"channel":"stable"}`, 400
			switch kind {
			case "unknown":
				body = `{"channel":"stable","url":"x"}`
			case "trailing":
				body += `{}`
			case "null":
				body = `null`
			case "type":
				body = `{"channel":3}`
			case "channel":
				body = `{"channel":"release"}`
			}
			r := actionRequest("/api/v1/update/channel", body, session)
			switch kind {
			case "cookie":
				r.Header.Del("Cookie")
				want = 401
			case "csrf":
				r.Header.Del("X-KRM-CSRF")
				want = 403
			case "origin":
				r.Header.Set("Origin", "https://attacker.invalid")
				want = 403
			case "method":
				r.Method = "GET"
				want = 405
			case "disabled":
				s.cfg.Update.Enabled = false
				want = 409
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != want || calls.Load() != 0 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body)
			}
		})
	}
}
