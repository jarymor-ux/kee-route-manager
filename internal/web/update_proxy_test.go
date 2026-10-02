package web

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/update"
)

func launcherSocket(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "krm-web-update-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "launcher.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })
	return socket
}

func TestUpdateProxyPreservesLauncherStatusAndAsyncAcceptance(t *testing.T) {
	for _, local := range []bool{false, true} {
		for _, path := range []string{"status", "check", "apply"} {
			t.Run(path+map[bool]string{true: "/local", false: "/network"}[local], func(t *testing.T) {
				var calls atomic.Int32
				status, response := 200, `{"enabled":true,"launcher":true,"phase":"trial","applying":true,"current_version":"1.0.0"}`
				if path == "check" {
					response = `{"available":true,"stage_supported":true,"latest_version":"1.1.0"}`
				}
				if path == "apply" {
					status, response = 202, `{"accepted":true}`
				}
				s, session := actionServer(t, &fakeController{})
				s.cfg.Update.Enabled = true
				s.cfg.Update.LauncherSocket = launcherSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path != "/"+path {
						t.Errorf("path=%q", r.URL.Path)
					}
					if path == "apply" {
						body, _ := io.ReadAll(r.Body)
						if r.Method != "POST" || string(body) != `{"version":"1.1.0"}` {
							t.Errorf("apply request=%s %s", r.Method, body)
						}
					} else if r.Method != "GET" {
						t.Errorf("method=%s", r.Method)
					}
					w.WriteHeader(status)
					_, _ = io.WriteString(w, response)
				}))
				r := actionRequest("/api/v1/update/"+path, `{"version":"1.1.0"}`, session)
				if path != "apply" {
					r.Method = "GET"
				}
				h := s.Handler()
				if local {
					h = s.LocalHandler()
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != status || !json.Valid(w.Body.Bytes()) || strings.TrimSpace(w.Body.String()) != response || calls.Load() != 1 {
					t.Fatalf("status=%d response=%s calls=%d", w.Code, w.Body, calls.Load())
				}
			})
		}
	}
}

func TestUpdateApplyRejectsUnauthorizedOrMalformedRequestsBeforeLauncher(t *testing.T) {
	for _, invalid := range []string{"cookie", "csrf", "origin", "method", "disabled", "unknown-field", "trailing", "null", "wrong-type"} {
		t.Run(invalid, func(t *testing.T) {
			var calls atomic.Int32
			s, session := actionServer(t, &fakeController{})
			s.cfg.Update.Enabled = true
			s.cfg.Update.LauncherSocket = launcherSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(202) }))
			body := `{}`
			switch invalid {
			case "unknown-field":
				body = `{"url":"https://untrusted.invalid"}`
			case "trailing":
				body = `{} {}`
			case "null":
				body = `null`
			case "wrong-type":
				body = `{"version":5}`
			}
			r := actionRequest("/api/v1/update/apply", body, session)
			want := 400
			switch invalid {
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
				want = 501
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != want || calls.Load() != 0 {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body)
			}
		})
	}
}

func TestUpdateProxyRejectsUntrustedSocketAndBadResponse(t *testing.T) {
	for _, kind := range []string{"missing", "permissions", "directory", "symlink", "invalid-json", "not-accepted", "unexpected-success", "oversized", "launcher-conflict", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			s, session := actionServer(t, &fakeController{})
			s.cfg.Update.Enabled = true
			socket := launcherSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "not-accepted":
					w.WriteHeader(202)
					_, _ = io.WriteString(w, `{"accepted":false}`)
				case "unexpected-success":
					_, _ = io.WriteString(w, `{"accepted":true}`)
				case "invalid-json":
					_, _ = io.WriteString(w, "broken")
				case "oversized":
					_, _ = io.WriteString(w, `{"data":"`+strings.Repeat("a", 4<<20)+`"}`)
				case "launcher-conflict":
					w.WriteHeader(409)
					_, _ = io.WriteString(w, `{"error":"another update is running"}`)
				default:
					w.WriteHeader(202)
					_, _ = io.WriteString(w, `{"accepted":true}`)
				}
			}))
			s.cfg.Update.LauncherSocket = socket
			switch kind {
			case "missing":
				s.cfg.Update.LauncherSocket += "missing"
			case "permissions":
				if e := os.Chmod(socket, 0666); e != nil {
					t.Fatal(e)
				}
			case "directory":
				if e := os.Chmod(filepath.Dir(socket), 0755); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				link := socket + "-link"
				if e := os.Symlink(socket, link); e != nil {
					t.Fatal(e)
				}
				s.cfg.Update.LauncherSocket = link
			}
			r := actionRequest("/api/v1/update/apply", `{}`, session)
			if kind == "canceled" {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			want := 502
			if kind == "launcher-conflict" {
				want = 409
			}
			if w.Code != want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
}

func TestUpdateCheckFallbackDiscoversSignedReleaseWithoutApplyCapability(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var manifest, signature []byte
	var reads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		switch r.URL.Path {
		case "/manifest":
			_, _ = w.Write(manifest)
		case "/signature":
			_, _ = w.Write(signature)
		default:
			t.Errorf("unexpected download %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	// The updater's default transport trusts this synthetic release server only
	// for this sequential test; production TLS verification remains unchanged.
	oldTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	m := update.Manifest{SchemaVersion: 1, UpdateProtocol: 1, Version: "1.2.0", Channel: "stable", MinConfigSchema: 1}
	for _, component := range []string{"daemon", "ui", "ctl"} {
		m.Assets = append(m.Assets, update.Asset{Name: component, Component: component, OS: runtime.GOOS, Arch: runtime.GOARCH, URL: server.URL + "/" + component, SHA256: strings.Repeat("1", 64), Size: 10})
	}
	manifest, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	signature = ed25519.Sign(private, manifest)
	s, session := actionServer(t, &fakeController{})
	s.cfg.Update.Enabled = true
	s.cfg.Update.Channel = "stable"
	s.cfg.Update.PublicKey = base64.RawStdEncoding.EncodeToString(public)
	s.cfg.Update.GitHubRepository = ""
	s.cfg.Update.ManifestURL, s.cfg.Update.SignatureURL = server.URL+"/manifest", server.URL+"/signature"
	s.cfg.Update.LauncherSocket = filepath.Join(t.TempDir(), "missing.sock")
	s.updater = update.New(s.cfg.Update, t.TempDir(), "1.1.0")
	r := actionRequest("/api/v1/update/check", "", session)
	r.Method = "GET"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var result update.CheckResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !result.Available || result.LatestVersion != "1.2.0" || result.StageSupported || len(result.Assets) != 3 || reads.Load() != 2 {
		t.Fatalf("fallback=%d %+v reads=%d", w.Code, result, reads.Load())
	}
	// A reachable launcher's explicit refusal is authoritative; do not hide it
	// by making a second remote discovery request.
	s.cfg.Update.LauncherSocket = launcherSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		_, _ = io.WriteString(w, `{"error":"updates paused"}`)
	}))
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 409 || reads.Load() != 2 {
		t.Fatalf("ignored launcher refusal: %d reads=%d", w.Code, reads.Load())
	}
}
