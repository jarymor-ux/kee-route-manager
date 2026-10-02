package redact

import (
	"strings"
	"testing"
)

func FuzzRedaction(f *testing.F) {
	for _, s := range []string{"https://user:password@example.invalid/path?token=secret", "Authorization: Bearer secret", `{"public_key":"secret","short_id":"secret"}`, "12345678-1234-1234-1234-123456789abc", "https://[::1]/a?b=secret"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 8192 {
			t.Skip()
		}
		out := Text(s)
		_ = Diagnostics(s)
		if strings.Contains(out, "12345678-1234-1234-1234-123456789abc") {
			t.Fatal("known UUID remained")
		}
		if len(out) > len(s)*8+128 {
			t.Fatal("unbounded redaction expansion")
		}
	})
}
