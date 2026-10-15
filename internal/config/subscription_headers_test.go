package config

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
)

func TestSubscriptionHeadersRejectCaseInsensitiveDuplicates(t *testing.T) {
	for _, names := range [][2]string{{"X-HWID", "x-hwid"}, {"User-Agent", "user-agent"}, {"Authorization", "AUTHORIZATION"}, {"X-Custom", "x-CUSTOM"}} {
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/enabled=%t", names[0], enabled), func(t *testing.T) {
				c := validConfig(t)
				c.Subscriptions.Sources[0].Enabled = enabled
				c.Subscriptions.Sources[0].Headers = map[string]string{names[0]: "synthetic-private-one", names[1]: "synthetic-private-two"}
				err := c.Validate()
				if err == nil || !strings.Contains(err.Error(), "duplicate header names") {
					t.Fatalf("case-insensitive duplicate headers were not rejected: %v", err)
				}
				for _, private := range []string{names[0], names[1], "synthetic-private-one", "synthetic-private-two"} {
					if strings.Contains(err.Error(), private) {
						t.Fatal("duplicate header diagnostic exposed private header details")
					}
				}
			})
		}
	}
}

func TestSubscriptionHeadersDistinctNamesRemainUnchanged(t *testing.T) {
	c := validConfig(t)
	headers := map[string]string{"x-hwid": "fixture-stable-identity", "User-Agent": "Happ/4.11.0", "X-Device-OS": "Linux", "Authorization": "synthetic-private-one", "X-Custom": "synthetic-private-two"}
	wantHeaders := maps.Clone(headers)
	c.Subscriptions.Sources[0].Headers = headers
	other := c.Subscriptions.Sources[0]
	other.ID = "provider_b"
	other.Headers = map[string]string{"X-HWID": "different-stable-identity", "user-agent": "Custom/2"}
	c.Subscriptions.Sources = append(c.Subscriptions.Sources, other)
	if err := c.Validate(); err != nil {
		t.Fatalf("distinct headers or same header names across separate sources rejected: %v", err)
	}
	if !reflect.DeepEqual(c.Subscriptions.Sources[0].Headers, wantHeaders) || c.Subscriptions.Sources[1].Headers["X-HWID"] != "different-stable-identity" {
		t.Fatal("validation changed private source headers")
	}
}
