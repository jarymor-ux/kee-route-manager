package redact

import (
	"strings"
	"testing"
)

func TestQuotedSecretsAreRedactedCompletely(t *testing.T) {
	for _, input := range []string{
		`{"password":"first SENSITIVE_TAIL", "status":"failed"}`,
		`token='first,SENSITIVE_TAIL' status=failed`,
		`{"secret":"first\" SENSITIVE_TAIL", "status":"failed"}`,
		"password=\"first\nSENSITIVE_TAIL\" status=failed",
	} {
		for _, got := range []string{Text(input), Diagnostics(input)} {
			if strings.Contains(got, "first") || strings.Contains(got, "SENSITIVE_TAIL") {
				t.Errorf("quoted credential leaked: %q", got)
			}
			if !strings.Contains(got, "failed") {
				t.Errorf("unrelated diagnostic removed: %q", got)
			}
		}
	}
}

func TestTruncatedQuotedSecretDoesNotLeak(t *testing.T) {
	for _, input := range []string{`password="SENSITIVE_TAIL`, `token='SENSITIVE_TAIL`, `secret="SENSITIVE_TAIL\`} {
		if got := Text(input); strings.Contains(got, "SENSITIVE_TAIL") {
			t.Errorf("truncated credential leaked: %q", got)
		}
	}
}
