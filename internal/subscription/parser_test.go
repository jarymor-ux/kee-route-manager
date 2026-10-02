package subscription

import (
	"encoding/base64"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"testing"
)

const reality = `vless://11111111-1111-1111-1111-111111111111@example.com:443?type=tcp&security=reality&sni=example.com&pbk=abc&fp=chrome#Node%20A`
const ws = `vless://22222222-2222-2222-2222-222222222222@ws.example.com:443?type=ws&security=tls&sni=ws.example.com&host=ws.example.com&path=%2Fws#Node%20B`

func TestParsePlainAndBase64(t *testing.T) {
	xs, e := ParsePayload([]byte(reality+"\n"+ws), "p1")
	if e != nil || len(xs) != 2 {
		t.Fatalf("%d %v", len(xs), e)
	}
	b := base64.StdEncoding.EncodeToString([]byte(reality))
	xs, e = ParsePayload([]byte(b), "p1")
	if e != nil || len(xs) != 1 {
		t.Fatalf("base64: %d %v", len(xs), e)
	}
	if xs[0].Network != "tcp" || xs[0].Security != "reality" {
		t.Fatal("wrong transport")
	}
}
func TestDeduplicateAcrossSources(t *testing.T) {
	a, _ := ParseVLESS(reality, "a")
	b, _ := ParseVLESS(reality, "b")
	xs := Merge([]model.Node{a, b}, 10)
	if len(xs) != 1 || len(xs[0].Sources) != 2 {
		t.Fatalf("dedupe failed: %#v", xs)
	}
}
func TestUnsupportedRejected(t *testing.T) {
	if _, e := ParseVLESS(`vless://11111111-1111-1111-1111-111111111111@example.com:443?type=grpc&security=tls`, "x"); e == nil {
		t.Fatal("expected unsupported error")
	}
}
