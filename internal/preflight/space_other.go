//go:build !linux && !darwin && !freebsd

package preflight

import "fmt"

func freeSpace(string) (uint64, error) {
	return 0, fmt.Errorf("preflight requires a supported Unix host")
}
