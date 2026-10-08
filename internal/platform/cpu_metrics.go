package platform

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
)

type cpuTimes struct {
	total uint64
	idle  uint64
}

type cpuSampler struct {
	mu       sync.Mutex
	readFile func(string) ([]byte, error)
	previous cpuTimes
	ready    bool
}

// CPU load is a difference between observations, not the lifetime average.
// Missing data and reset counters invalidate the baseline instead of inventing 0%.
func (s *cpuSampler) sample() *float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	readFile := s.readFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile("/proc/stat")
	if err != nil {
		s.ready = false
		return nil
	}
	current, err := parseCPUTimes(data)
	if err != nil {
		s.ready = false
		return nil
	}
	previous, ready := s.previous, s.ready
	s.previous, s.ready = current, true
	if !ready || current.total <= previous.total || current.idle < previous.idle {
		return nil
	}
	total, idle := current.total-previous.total, current.idle-previous.idle
	if idle > total {
		return nil
	}
	percent := 100 * float64(total-idle) / float64(total)
	return &percent
}

func parseCPUTimes(data []byte) (cpuTimes, error) {
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuTimes{}, fmt.Errorf("missing aggregate CPU counters")
	}
	var result cpuTimes
	// guest and guest_nice (fields 9 and 10) are already included in user/nice.
	for i := 1; i < len(fields) && i <= 8; i++ {
		value, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil || value > math.MaxUint64-result.total {
			return cpuTimes{}, fmt.Errorf("invalid CPU counter")
		}
		result.total += value
		if i == 4 || i == 5 {
			result.idle += value
		}
	}
	return result, nil
}
