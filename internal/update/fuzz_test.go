package update

import "testing"

func FuzzSemVer(f *testing.F) {
	for _, p := range [][2]string{{"1.0.0-rc.9", "1.0.0-rc.10"}, {"v1.0.0+build1", "1.0.0+build2"}, {"01.0.0", "1.0.0"}, {"1.0.0-alpha.999999999999999999999", "1.0.0-alpha.1000000000000000000000"}} {
		f.Add(p[0], p[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		if len(a) > 512 || len(b) > 512 {
			t.Skip()
		}
		if _, err := parseSemVer(a); err != nil {
			return
		}
		if _, err := parseSemVer(b); err != nil {
			return
		}
		if compareVersions(a, a) != 0 {
			t.Fatal("non-reflexive comparator")
		}
		x, y := compareVersions(a, b), compareVersions(b, a)
		if x != -y {
			t.Fatal("non-antisymmetric comparator")
		}
	})
}
