package config

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type Duration struct{ time.Duration }

func Dur(v time.Duration) Duration { return Duration{v} }

func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = v
	return nil
}
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

type ByteSize int64

func (b *ByteSize) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		v, err := ParseByteSize(s)
		if err != nil {
			return err
		}
		*b = ByteSize(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("byte size must be a string or integer")
	}
	if n < 0 {
		return fmt.Errorf("byte size cannot be negative")
	}
	*b = ByteSize(n)
	return nil
}
func (b ByteSize) MarshalJSON() ([]byte, error) { return json.Marshal(FormatByteSize(int64(b))) }

func ParseByteSize(raw string) (int64, error) {
	s := strings.TrimSpace(strings.ToUpper(raw))
	if s == "" {
		return 0, fmt.Errorf("empty byte size")
	}
	units := []struct {
		suffix string
		mult   int64
	}{{"GIB", 1 << 30}, {"GB", 1_000_000_000}, {"MIB", 1 << 20}, {"MB", 1_000_000}, {"KIB", 1 << 10}, {"KB", 1_000}, {"B", 1}}
	mult := int64(1)
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			mult = u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("invalid byte size %q", raw)
	}
	if f >= float64(^uint64(0)>>1)/float64(mult) {
		return 0, fmt.Errorf("byte size too large")
	}
	return int64(f * float64(mult)), nil
}
func FormatByteSize(v int64) string {
	switch {
	case v > 0 && v%(1<<30) == 0:
		return fmt.Sprintf("%dGiB", v>>30)
	case v > 0 && v%(1<<20) == 0:
		return fmt.Sprintf("%dMiB", v>>20)
	case v > 0 && v%(1<<10) == 0:
		return fmt.Sprintf("%dKiB", v>>10)
	default:
		return fmt.Sprintf("%dB", v)
	}
}
