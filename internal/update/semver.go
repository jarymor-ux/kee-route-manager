package update

import (
	"fmt"
	"regexp"
	"strings"
)

// SemVer 2.0.0 precedence ignores metadata and compares numeric identifiers
// numerically. Compare lengths rather than machine integers to avoid overflow.
var semVerRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

type semVer struct {
	core []string
	pre  []string
}

func parseSemVer(v string) (semVer, error) {
	m := semVerRE.FindStringSubmatch(strings.TrimPrefix(v, "v"))
	if m == nil {
		return semVer{}, fmt.Errorf("invalid SemVer %q", v)
	}
	x := semVer{core: m[1:4]}
	if m[4] != "" {
		x.pre = strings.Split(m[4], ".")
		for _, id := range x.pre {
			if numeric(id) && len(id) > 1 && id[0] == '0' {
				return semVer{}, fmt.Errorf("numeric prerelease has a leading zero")
			}
		}
	}
	return x, nil
}
func numeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
func compareNumber(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}
func compareVersions(a, b string) int {
	x, ex := parseSemVer(a)
	y, ey := parseSemVer(b)
	if ex != nil || ey != nil {
		return 0
	} // Check rejects malformed versions before comparison.
	for i := 0; i < 3; i++ {
		if c := compareNumber(x.core[i], y.core[i]); c != 0 {
			return c
		}
	}
	if len(x.pre) == 0 && len(y.pre) == 0 {
		return 0
	}
	if len(x.pre) == 0 {
		return 1
	}
	if len(y.pre) == 0 {
		return -1
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		p, q := x.pre[i], y.pre[i]
		pn, qn := numeric(p), numeric(q)
		c := 0
		switch {
		case pn && qn:
			c = compareNumber(p, q)
		case pn:
			c = -1
		case qn:
			c = 1
		default:
			c = strings.Compare(p, q)
		}
		if c != 0 {
			return c
		}
	}
	if len(x.pre) < len(y.pre) {
		return -1
	}
	if len(x.pre) > len(y.pre) {
		return 1
	}
	return 0
}
