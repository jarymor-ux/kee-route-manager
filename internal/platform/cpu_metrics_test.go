package platform

import (
	"errors"
	"testing"
)

func TestCPUSamplerLoadAndBaselineRecovery(t *testing.T) {
	data := "cpu 10 0 10 80 0 0 0 0 5 0\ncpu0 10 0 10 80\n"
	var readErr error
	s := cpuSampler{readFile: func(path string) ([]byte, error) {
		if path != "/proc/stat" {
			t.Fatalf("unexpected CPU source %s", path)
		}
		return []byte(data), readErr
	}}
	if s.sample() != nil {
		t.Fatal("first sample must wait for a delta")
	}
	data = "cpu 30 0 20 150 0 0 0 0 15 0\n"
	if load := s.sample(); load == nil || *load != 30 {
		t.Fatalf("delta load=%v, want 30%% (guest must not double count)", load)
	}
	if s.sample() != nil {
		t.Fatal("unchanged counters must not invent a measurement")
	}
	data = "cpu 1 0 1 8 0 0 0 0\n"
	if s.sample() != nil {
		t.Fatal("reset counters must establish a new baseline")
	}
	data = "cpu 3 0 2 15 0 0 0 0\n"
	if load := s.sample(); load == nil || *load != 30 {
		t.Fatal("counter reset did not recover")
	}
	readErr = errors.New("unavailable")
	if s.sample() != nil {
		t.Fatal("read error must yield unknown load")
	}
	readErr = nil
	if s.sample() != nil {
		t.Fatal("first sample after a gap must establish a baseline")
	}
	data = "cpu corrupt\n"
	if s.sample() != nil {
		t.Fatal("invalid CPU data must yield unknown load")
	}
}

func TestParseCPUTimesRejectsMalformedAndOverflow(t *testing.T) {
	for _, data := range []string{"", "cpu0 1 2 3 4", "cpu 1 2 3", "cpu 1 x 3 4", "cpu -1 2 3 4", "cpu 18446744073709551615 1 0 0"} {
		if _, err := parseCPUTimes([]byte(data)); err == nil {
			t.Fatalf("invalid CPU counters accepted: %q", data)
		}
	}
	got, err := parseCPUTimes([]byte("cpu 1 2 3 4 5 6 7 8 100 200\n"))
	if err != nil || got.total != 36 || got.idle != 9 {
		t.Fatalf("guest exclusion or idle parsing failed: %+v %v", got, err)
	}
}
