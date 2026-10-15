package subscription

import "testing"

func FuzzSubscriptionPayload(f *testing.F) {
	for _, s := range []string{reality, "not a subscription", "dmxlc3M6Ly9pbnZhbGlk", "vless://user@[::1]:443?security=reality&type=tcp"} {
		f.Add(s)
	}
	f.Add(`{"remarks":"Fixture","outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"node.example.invalid","port":443,"users":[{"id":"11111111-1111-1111-1111-111111111111","encryption":"none"}]}]},"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"example.invalid","publicKey":"fixture-public-key"}}}]}`)
	f.Add(`[{"outbounds":[]},{"outbounds":[],"outbounds":[]}]`)
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 8192 {
			t.Skip()
		}
		nodes, err := ParsePayload([]byte(s), "fixture")
		if err != nil {
			return
		}
		for _, n := range nodes {
			if n.ID == "" || n.Port < 1 || n.Port > 65535 || !uuidOK(n.UUID) {
				t.Fatal("invalid parsed node")
			}
		}
	})
}
