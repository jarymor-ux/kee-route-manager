package bench

import "testing"

func TestMajority(t *testing.T) {
	cases := []struct {
		p, n int
		want bool
	}{{1, 1, true}, {1, 2, false}, {2, 3, true}, {2, 4, false}, {0, 0, false}}
	for _, c := range cases {
		if got := Majority(c.p, c.n); got != c.want {
			t.Fatalf("%d/%d got %v", c.p, c.n, got)
		}
	}
}
func TestStatusPolicy(t *testing.T) {
	if !statusOK(204, "2xx3xx") || statusOK(404, "2xx3xx") || !statusOK(404, "exact:404") {
		t.Fatal("status policy")
	}
}
