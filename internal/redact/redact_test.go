package redact

import (
	"strings"
	"testing"
)

func TestRedaction(t *testing.T) {
	input := `Get "https://admin:password@private.example/sub?token=secret": UUID 12345678-1234-1234-1234-123456789abc Authorization: Bearer secret Cookie: krm_session=secret publicKey=secret shortId=secret`
	got := Text(input)
	for _, v := range []string{"admin:password", "token=secret", "12345678-1234-1234-1234-123456789abc", "Bearer secret", "krm_session=secret", "publicKey=secret", "shortId=secret"} {
		if strings.Contains(got, v) {
			t.Errorf("leaked %s in %s", v, got)
		}
	}
	if strings.Contains(Diagnostics(input+" 203.0.113.25"), "private.example") || strings.Contains(Diagnostics(input+" 203.0.113.25"), "203.0.113.25") {
		t.Fatal("diagnostics leaked host/IP")
	}
}
func TestHeadersCopy(t *testing.T) {
	in := map[string]string{"authorization": "secret", "Cookie": "secret", "Accept": "text/plain"}
	got := Headers(in)
	if got["authorization"] != "<redacted>" || got["Accept"] != "text/plain" || in["Cookie"] != "secret" {
		t.Fatal("headers not sanitized as independent copy")
	}
}
