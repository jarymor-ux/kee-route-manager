package subscription

import "testing"

func FuzzSubscriptionPayload(f *testing.F) {
	for _, s := range []string{reality, "not a subscription", "dmxlc3M6Ly9pbnZhbGlk", "vless://user@[::1]:443?security=reality&type=tcp"} {
		f.Add(s)
	}
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
