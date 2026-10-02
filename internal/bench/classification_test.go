package bench

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"net"
	"net/url"
	"syscall"
	"testing"
)

func TestMonitoringClassification(t *testing.T) {
	targets := []config.Target{{ID: "a", URL: "https://one.example/"}, {ID: "b", URL: "https://two.example/"}, {ID: "c", URL: "https://three.example/"}}
	results := func(a, b, c bool) []ProbeResult {
		return []ProbeResult{{TargetID: "a", Success: a}, {TargetID: "b", Success: b}, {TargetID: "c", Success: c}}
	}
	for _, tc := range []struct {
		name     string
		vpn, wan []ProbeResult
		want     string
	}{
		{"target outage", results(true, false, true), results(true, false, true), "healthy"},
		{"VPN path down", results(false, false, false), results(true, true, true), "vpn_path_failed"},
		{"same target down across paths", results(false, false, false), results(false, false, false), "monitoring_inconclusive"},
		{"insufficient independent working targets", results(true, false, false), results(true, false, false), "target_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(targets, tc.vpn, tc.wan, 2); got != tc.want {
				t.Fatalf("classification %s, want %s", got, tc.want)
			}
		})
	}
	targets[1].URL = "https://one.example/other"
	targets = targets[:2]
	if got := Classify(targets, results(true, true, true), results(true, true, true), 2); got != "monitoring_inconclusive" {
		t.Fatal("same host must not satisfy independent quorum")
	}
}

func TestTypedMonitoringFailuresPreservePathClassification(t *testing.T) {
	targets := []config.Target{{ID: "a", URL: "https://one.example/"}, {ID: "b", URL: "https://two.example/"}}
	for _, kind := range []string{"dns_failed", "wan_failed"} {
		wan := []ProbeResult{{TargetID: "a", FailureClass: kind}, {TargetID: "b", FailureClass: kind}}
		if got := Classify(targets, nil, wan, 2); got != kind {
			t.Fatalf("kind=%s classification=%s", kind, got)
		}
	}
	if got := ClassifyProbeError(&url.Error{Err: &net.DNSError{Err: "no such host", Name: "example.invalid"}}); got != "dns_failed" {
		t.Fatal("typed DNS failure lost")
	}
	if got := ClassifyProbeError(&net.OpError{Err: syscall.ENETUNREACH}); got != "wan_failed" {
		t.Fatal("typed WAN unreachable failure lost")
	}
	if got := ClassifyProbeError(context.DeadlineExceeded); got != "monitoring_inconclusive" {
		t.Fatal("timeout guessed as WAN failure")
	}
}
