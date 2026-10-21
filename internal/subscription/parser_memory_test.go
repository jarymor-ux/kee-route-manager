package subscription

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestBase64VariantsPreservePlainPayloadSelection(t *testing.T) {
	for _, payload := range []string{reality, reality + "\n" + ws, reality + "☃😀", ws + "\n" + reality + "\nunsupported"} {
		want, err := ParsePayload([]byte(payload), "fixture")
		if err != nil {
			t.Fatal(err)
		}
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			encoded := encoding.EncodeToString([]byte(payload))
			// Providers may wrap subscriptions with whitespace regardless of alphabet.
			encoded = " \n" + encoded[:len(encoded)/2] + "\r\n\t" + encoded[len(encoded)/2:] + "\n"
			got, err := ParsePayload([]byte(encoded), "fixture")
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("base64 variant changed selection: got=%+v err=%v want=%+v", got, err, want)
			}
		}
	}
}

func BenchmarkParsePayload(b *testing.B) {
	for _, count := range []int{50, 500, 5000} {
		var plain strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&plain, "vless://%08x-1111-1111-1111-111111111111@edge-%d.example.test:443?type=ws&security=tls&path=%%2Fws#Node%%20%04d\n", i+1, i, i)
		}
		for _, encoded := range []bool{false, true} {
			name, payload := "plain", []byte(plain.String())
			if encoded {
				name, payload = "base64", []byte(base64.RawStdEncoding.EncodeToString(payload))
			}
			b.Run(fmt.Sprintf("%s/nodes-%d", name, count), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(payload)))
				for iteration := 0; iteration < b.N; iteration++ {
					nodes, err := ParsePayload(payload, "synthetic")
					if err != nil || len(nodes) != count {
						b.Fatalf("nodes=%d want=%d err=%v", len(nodes), count, err)
					}
				}
			})
		}
	}
}
