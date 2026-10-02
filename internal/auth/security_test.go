package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestVerificationRejectsUnboundedInputsAndCost(t *testing.T) {
	c := Credentials{SchemaVersion: 1, Username: "admin", Algorithm: "pbkdf2-sha256", Iterations: 100000, Salt: base64.RawStdEncoding.EncodeToString(make([]byte, 24)), PasswordHash: base64.RawStdEncoding.EncodeToString(make([]byte, 32))}
	for _, tc := range []struct{ user, pass string }{{strings.Repeat("a", 65), "valid-password"}, {"admin", strings.Repeat("x", 1025)}} {
		if Verify(c, tc.user, tc.pass) {
			t.Fatal("unbounded input accepted")
		}
	}
	c.Iterations = 0
	c.PasswordHash = base64.RawStdEncoding.EncodeToString(pbkdf2SHA256([]byte("valid-password"), make([]byte, 24), 0, 32))
	if Verify(c, "admin", "valid-password") {
		t.Fatal("invalid work factor accepted")
	}
}
func TestLimiterPrunesStaleKeysAndHasGlobalLimit(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	l.attempts["stale"] = []time.Time{time.Now().Add(-2 * time.Minute)}
	if !l.Allow("one") {
		t.Fatal("first rejected")
	}
	if _, ok := l.attempts["stale"]; ok {
		t.Fatal("stale entry retained")
	}
	// A flood from many addresses must be bounded globally.
	for i := 0; i < 100; i++ {
		l.Allow(strings.Repeat("x", i+1))
	}
	if l.Allow("fresh-address") {
		t.Fatal("global flood accepted")
	}
}
