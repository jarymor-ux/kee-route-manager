package releasetrust

import (
	_ "embed"
	"strings"
)

//go:embed public.key
var publicKey string

// PublicKey returns the production Ed25519 release verification key embedded
// into the binary at build time.
func PublicKey() string {
	return strings.TrimSpace(publicKey)
}
