package redact

import "testing"

func TestUUIDRedactionDoesNotRequireWordBoundaries(t *testing.T) {
	const uuid = "12345678-1234-1234-1234-123456789abc"
	const masked = "<redacted-uuid>"
	for _, test := range []struct {
		name, input, want string
	}{
		{"digit prefix", "0" + uuid, "0" + masked},
		{"digit suffix", uuid + "0", masked + "0"},
		{"word context", "node_" + uuid + "_failed", "node_" + masked + "_failed"},
		{"adjacent UUIDs", uuid + uuid, masked + masked},
		{"uppercase", "node_12345678-ABCD-1234-ABCD-123456789ABC_failed", "node_" + masked + "_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for name, redact := range map[string]func(string) string{"Text": Text, "Diagnostics": Diagnostics} {
				if got := redact(test.input); got != test.want {
					t.Errorf("%s: got %q, want %q", name, got, test.want)
				}
			}
		})
	}
}
