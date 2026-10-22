package setup

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeDiscoveryFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverRoutingChoosesOnlyUnconditionalVPN(t *testing.T) {
	dir := t.TempDir()
	writeDiscoveryFile(t, dir, "01_inbounds.json", `{"inbounds":[{"tag":"redirect","protocol":"dokodemo-door"},{"tag":"tproxy","protocol":"dokodemo-door"}]}`)
	writeDiscoveryFile(t, dir, "04_outbounds.json", `{"outbounds":[{"tag":"DIRECT","protocol":"freedom"},{"tag":"proxy-main","protocol":"vless"}]}`)
	writeDiscoveryFile(t, dir, "05_routing.json", `{"routing":{"rules":[{"type":"field","inboundTag":["redirect"],"outboundTag":"proxy-main"},{"type":"field","inboundTag":["tproxy"],"outboundTag":"proxy-main"},{"type":"field","inboundTag":["redirect"],"domain":["private"],"outboundTag":"DIRECT"}]}}`)
	got, err := discoverRouting(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []routeCandidate{{File: filepath.Join(dir, "05_routing.json"), InboundTags: []string{"redirect", "tproxy"}, OutboundTag: "proxy-main"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	// Automatic routing needs no additional manual tag input.
	w := &wizard{in: bufio.NewReader(strings.NewReader("")), out: new(strings.Builder), msg: messagesEN}
	file, tags, outbound, err := w.selectRouting(dir)
	if err != nil || file != want[0].File || !reflect.DeepEqual(tags, want[0].InboundTags) || outbound != want[0].OutboundTag {
		t.Fatalf("automatic selection: %s %#v %s %v", file, tags, outbound, err)
	}
}

func TestDiscoverRoutingAmbiguousRequiresChoice(t *testing.T) {
	dir := t.TempDir()
	writeDiscoveryFile(t, dir, "01.json", `{"inbounds":[{"tag":"redirect"},{"tag":"tproxy"}],"outbounds":[{"tag":"a-proxy","protocol":"vless"},{"tag":"b-proxy","protocol":"trojan"}]}`)
	writeDiscoveryFile(t, dir, "05.json", `{"routing":{"rules":[{"type":"field","inboundTag":["redirect"],"outboundTag":"a-proxy"},{"type":"field","inboundTag":["tproxy"],"outboundTag":"b-proxy"}]}}`)
	candidates, err := discoverRouting(dir)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	var output strings.Builder
	w := &wizard{in: bufio.NewReader(strings.NewReader("99\n2\n")), out: &output, msg: messagesEN}
	file, tags, outbound, err := w.selectRouting(dir)
	if err != nil || file != filepath.Join(dir, "05.json") || outbound != "b-proxy" || !reflect.DeepEqual(tags, []string{"tproxy"}) {
		t.Fatalf("manual choice: %s %#v %s %v", file, tags, outbound, err)
	}
	if !strings.Contains(output.String(), messagesEN.AutoRoutingInvalid) {
		t.Fatal("invalid candidate choice was not rejected")
	}
}

func TestDiscoverRoutingRefusesAmbiguousOrMalformedFiles(t *testing.T) {
	for _, scenario := range []struct {
		name, one, two string
	}{
		{"multiple-routing", `{"routing":{"rules":[]}}`, `{"routing":{"rules":[]}}`},
		{"malformed-json", `{"routing":{"rules":[]}}`, `{"inbounds":[}`},
		{"trailing-json", `{"routing":{"rules":[]}}`, `{"outbounds":[]} {}`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDiscoveryFile(t, dir, "01.json", scenario.one)
			writeDiscoveryFile(t, dir, "02.json", scenario.two)
			if _, err := discoverRouting(dir); err == nil {
				t.Fatal("unsafe configuration silently accepted")
			}
		})
	}
}

func TestDiscoverRoutingDoesNotAdoptConditionalOrDirectRules(t *testing.T) {
	dir := t.TempDir()
	writeDiscoveryFile(t, dir, "01.json", `{"inbounds":[{"tag":"redirect"}],"outbounds":[{"tag":"proxy","protocol":"vless"},{"tag":"direct","protocol":"freedom"}]}`)
	writeDiscoveryFile(t, dir, "05.json", `{"routing":{"rules":[{"type":"field","inboundTag":["redirect"],"outboundTag":"direct"},{"type":"field","inboundTag":["redirect"],"outboundTag":"proxy","domain":["example.org"]}]}}`)
	got, err := discoverRouting(dir)
	if err != nil || len(got) != 0 {
		t.Fatalf("unconditional VPN required, got %#v err=%v", got, err)
	}
}

func TestDiscoverRoutingMissingDirRemainsManual(t *testing.T) {
	got, err := discoverRouting(filepath.Join(t.TempDir(), "missing"))
	if err != nil || len(got) != 0 {
		t.Fatalf("missing dependency should defer to manual wizard: %#v %v", got, err)
	}
}

func TestDiscoverRoutingRefusesConflictingUnconditionalOwners(t *testing.T) {
	for _, tc := range []struct {
		name, rules string
	}{
		{"direct-before-vpn", `[{"type":"field","inboundTag":["redirect"],"outboundTag":"DIRECT"},{"type":"field","inboundTag":["redirect"],"outboundTag":"proxy"}]`},
		{"direct-after-vpn", `[{"type":"field","inboundTag":["redirect"],"outboundTag":"proxy"},{"type":"field","inboundTag":["redirect"],"outboundTag":"DIRECT"}]`},
		{"balancer-before-vpn", `[{"type":"field","inboundTag":["redirect"],"balancerTag":"balance"},{"type":"field","inboundTag":["redirect"],"outboundTag":"proxy"}]`},
		{"other-vpn-same-inbound", `[{"type":"field","inboundTag":["redirect"],"outboundTag":"proxy"},{"type":"field","inboundTag":["redirect"],"outboundTag":"proxy2"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDiscoveryFile(t, dir, "01.json", `{"inbounds":[{"tag":"redirect"}],"outbounds":[{"tag":"proxy","protocol":"vless"},{"tag":"proxy2","protocol":"trojan"},{"tag":"DIRECT","protocol":"freedom"}]}`)
			writeDiscoveryFile(t, dir, "05.json", `{"routing":{"rules":`+tc.rules+`}}`)
			got, err := discoverRouting(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Fatalf("conflicting default owners must require manual review, got %#v", got)
			}
		})
	}
}

func TestDiscoverRoutingRejectsSymlinkedConfiguration(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "external.json")
	if err := os.WriteFile(target, []byte(`{"routing":{"rules":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "05_routing.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverRouting(dir); err == nil {
		t.Fatal("installer must not follow external configuration symlinks")
	}
}

func TestDiscoverRoutingAcceptsImplicitFieldInModernXray(t *testing.T) {
	dir := t.TempDir()
	writeDiscoveryFile(t, dir, "03_inbounds.json", `{"inbounds":[{"tag":"redirect","protocol":"tunnel"}]}`)
	writeDiscoveryFile(t, dir, "04_outbounds.json", `{"outbounds":[{"tag":"vpn","protocol":"vless"},{"tag":"direct","protocol":"freedom"}]}`)
	writeDiscoveryFile(t, dir, "05_routing.json", `{"routing":{"rules":[{"inboundTag":["redirect"],"outboundTag":"vpn"}]}}`)
	got, err := discoverRouting(dir)
	want := []routeCandidate{{File: filepath.Join(dir, "05_routing.json"), InboundTags: []string{"redirect"}, OutboundTag: "vpn"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("modern Xray implicit field route: got %#v, want %#v, error %v", got, want, err)
	}
}

func TestDiscoverRoutingRejectsScriptOverride(t *testing.T) {
	dir := t.TempDir()
	writeDiscoveryFile(t, dir, "03_inbounds.json", `{"inbounds":[{"tag":"redirect","protocol":"tunnel"}]}`)
	writeDiscoveryFile(t, dir, "04_outbounds.json", `{"outbounds":[{"tag":"vpn","protocol":"vless"}]}`)
	writeDiscoveryFile(t, dir, "05_routing.json", `{"routing":{"script":"router.lua","rules":[{"inboundTag":["redirect"],"outboundTag":"vpn"}]}}`)
	if _, err := discoverRouting(dir); err == nil {
		t.Fatal("routing with Lua selection must not be auto-adopted")
	}
}
