package platform

import (
	"encoding/json"
	"testing"
)

func TestKnownIdleTrafficSerializesZeroForDashboard(t *testing.T) {
	data, err := json.Marshal(Metrics{TrafficAvailable: true})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["rx_mbps"] != float64(0) || payload["tx_mbps"] != float64(0) || payload["traffic_available"] != true {
		t.Fatal("valid idle traffic disappeared from API response")
	}
}
