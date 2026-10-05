package releasetrust

import (
	"encoding/base64"
	"testing"
)

func TestEmbeddedPublicKeyIsEd25519(t *testing.T) {
	key, err := base64.RawStdEncoding.DecodeString(PublicKey())
	if err != nil {
		t.Fatalf("decode embedded release public key: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("embedded release public key is %d bytes, want 32", len(key))
	}
}
