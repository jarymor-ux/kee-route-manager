package platform

import (
	"math"
	"strconv"
)

// RCI numbers may be encoded as JSON numbers or decimal strings. Missing,
// malformed and negative counters remain unavailable rather than becoming zero.
func telemetryNumber(value any) (float64, bool) {
	var result float64
	switch value := value.(type) {
	case float64:
		result = value
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, false
		}
		result = parsed
	default:
		return 0, false
	}
	return result, !math.IsNaN(result) && !math.IsInf(result, 0) && result >= 0
}
