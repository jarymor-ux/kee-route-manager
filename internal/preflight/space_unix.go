//go:build linux || darwin || freebsd

package preflight

import (
	"fmt"
	"math"
	"syscall"
)

func freeSpace(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	if st.Bsize <= 0 || uint64(st.Bavail) > math.MaxUint64/uint64(st.Bsize) {
		return 0, fmt.Errorf("invalid filesystem capacity")
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
