package platform

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestKeeneticMetricsDistinguishMissingTrafficAndCPU(t *testing.T) {
	for _, test := range []struct {
		name, stats string
		available   bool
		rx, tx      float64
	}{
		{"missing", `{}`, false, 0, 0},
		{"partial", `{"rxspeed":0}`, false, 0, 0},
		{"idle", `{"rxspeed":0,"txspeed":"0"}`, true, 0, 0},
		{"active", `{"rxspeed":1000000,"txspeed":"250000"}`, true, 8, 2},
		{"invalid", `{"rxspeed":"NaN","txspeed":4}`, false, 0, 0},
		{"negative", `{"rxspeed":-1,"txspeed":4}`, false, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			k := newKeenetic(config.Default(), Runner{}).(*keenetic)
			k.http = &http.Client{Transport: platformRegressionRT(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/rci/show/system":
					return platformRegressionResponse(`{"uptime":1}`), nil
				case "/rci/show/interface":
					return platformRegressionResponse(`{"wan":{"id":"ISP","connected":"yes","defaultgw":true}}`), nil
				case "/rci/show/interface/stat":
					return platformRegressionResponse(test.stats), nil
				case "/rci/show/system/cpustat":
					return platformRegressionResponse(`{}`), nil
				default:
					return nil, fmt.Errorf("unexpected request %s", r.URL.Path)
				}
			})}
			m, err := k.Metrics(context.Background())
			if err != nil || m.TrafficAvailable != test.available || m.RXMbps != test.rx || m.TXMbps != test.tx || m.CPUPercent != nil {
				t.Fatalf("bad availability: %+v %v", m, err)
			}
		})
	}
}

func TestTelemetryNumberRejectsUnavailableValues(t *testing.T) {
	for _, value := range []any{nil, true, "", "bad", "NaN", "Inf", "-1", -1.0, math.NaN(), math.Inf(1)} {
		if _, ok := telemetryNumber(value); ok {
			t.Fatalf("invalid telemetry accepted: %v", value)
		}
	}
	for _, value := range []any{0.0, "0", "12.5", 12.5} {
		if _, ok := telemetryNumber(value); !ok {
			t.Fatalf("valid telemetry rejected: %v", value)
		}
	}
}
