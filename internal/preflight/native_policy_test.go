package preflight

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativePolicyRequiresUniqueAuthorizedNonzeroMark(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"valid", `[{"description":"XKeen","mark":"a001"}]`, 200, true},
		{"map", `{"Policy0":{"description":"xkeen","mark":"a001"}}`, 200, true},
		{"missing", `[{"description":"Default","mark":"a001"}]`, 200, false},
		{"zero", `[{"description":"xkeen","mark":"0"}]`, 200, false},
		{"empty", `[{"description":"xkeen"}]`, 200, false},
		{"duplicate", `[{"description":"xkeen","mark":"a001"},{"description":"XKEEN","mark":"a002"}]`, 200, false},
		{"unauthorized", `private-response`, 403, false},
		{"invalid", `[{"description":"xkeen","mark":"not-a-mark"}]`, 200, false},
		{"trailing", `[{"description":"xkeen","mark":"a001"}] {}`, 200, false},
		{"oversized", strings.Repeat(" ", 256<<10) + `[]`, 200, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("X-Ndma-Tkn") != "private-token" || r.URL.Path != "/rci/show/ip/policy" || r.URL.RawQuery != "" {
					t.Error("wrong read-only policy request")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "xkeen.json")
			if err := os.WriteFile(path, []byte(`{"rci_token":"private-token"}`), 0600); err != nil {
				t.Fatal(err)
			}
			err := checkNativePolicy(context.Background(), path, server.URL+"/rci/show/ip/policy")
			if (err == nil) != test.ok {
				t.Fatal("wrong activation gate", err)
			}
			if err != nil && (strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "private-response")) {
				t.Fatal("policy check leaked private material")
			}
		})
	}
}

func TestNativePolicyRefusesInvalidCredentialsBeforeRequest(t *testing.T) {
	for _, data := range []string{`null`, `{"rci_token":"bad\nheader"}`, `{"rci_token":123}`, `{} {}`, strings.Repeat(" ", 64<<10) + `{}`} {
		path := filepath.Join(t.TempDir(), "xkeen.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := checkNativePolicy(context.Background(), path, "http://127.0.0.1:1/rci/show/ip/policy"); err == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
}
