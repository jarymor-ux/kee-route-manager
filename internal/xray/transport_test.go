package xray

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"github.com/jarymor-ux/kee-route-manager/internal/tunnel"
)

func TestOutboundTransportsPreserveConnectionAndTLSSettings(t *testing.T) {
	for _, kind := range []string{"reality", "websocket"} {
		t.Run(kind, func(t *testing.T) {
			node := testNode()
			node.Encryption = ""
			node.Flow, node.ShortID, node.SpiderX = "xtls-rprx-vision", "aabb", "/probe"
			if kind == "websocket" {
				node.Network, node.Security, node.Flow = "ws", "tls", ""
				node.WSHost, node.ALPN = "cdn.example.com", []string{"http/1.1"}
			}
			out, err := Outbound(node, "slot-0")
			if err != nil {
				t.Fatal(err)
			}
			server := out["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)
			user := server["users"].([]any)[0].(map[string]any)
			if server["address"] != node.Address || server["port"] != node.Port || user["id"] != node.UUID || user["encryption"] != "none" {
				t.Fatal("connection identity or encryption lost")
			}
			stream := out["streamSettings"].(map[string]any)
			if kind == "reality" {
				reality := stream["realitySettings"].(map[string]any)
				if reality["publicKey"] != node.PublicKey || reality["shortId"] != node.ShortID || reality["spiderX"] != node.SpiderX || reality["fingerprint"] != node.Fingerprint || user["flow"] != node.Flow {
					t.Fatal("Reality handshake settings lost")
				}
			} else {
				tls := stream["tlsSettings"].(map[string]any)
				ws := stream["wsSettings"].(map[string]any)
				if tls["allowInsecure"] != false || tls["serverName"] != node.ServerName || !reflect.DeepEqual(tls["alpn"], node.ALPN) || ws["path"] != "/" || ws["headers"].(map[string]any)["Host"] != node.WSHost {
					t.Fatal("WebSocket routing or verified TLS settings lost")
				}
			}
		})
	}
	for _, node := range []model.Node{{Protocol: "trojan"}, {Protocol: "vless", Network: "tcp", Security: "none"}} {
		if _, err := Outbound(node, "slot-0"); err == nil {
			t.Fatal("unsupported transport accepted")
		}
	}
}

type selectionFailureRunner struct{ calls int }

func (r *selectionFailureRunner) Run(context.Context, []string) ([]byte, error) {
	r.calls++
	return nil, errors.New("selection API unavailable")
}

func TestSelectionFailureRollsBackAndInvalidTagsNeverCallAPI(t *testing.T) {
	c, _, _, _ := reviewPoolFixture(t)
	r := &selectionFailureRunner{}
	m := NewManager(c, r, &fakePlatform{})
	path := filepath.Join(c.Xray.ManagedDir, "04_90_kee_route_manager_outbounds.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.EnterDirect(context.Background()); err == nil || r.calls != 1 {
		t.Fatalf("API failure not returned: %v, calls %d", err, r.calls)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("failed selection changed persistent restart target")
	}
	if err = m.Select(context.Background(), tunnel.Selection{Tag: "missing"}); err == nil || r.calls != 1 {
		t.Fatal("invalid selection reached runtime API")
	}
	good := NewManager(c, &recordingRunner{}, &fakePlatform{})
	if err = good.EnterDirect(context.Background()); err != nil {
		t.Fatal(err)
	}
	actual, err := good.ActualState(context.Background())
	if err != nil || actual.Selection.Tag != c.Xray.ManagedDirectTag {
		t.Fatalf("successful direct selection not persistent: %+v %v", actual, err)
	}
	var out map[string]any
	after, err = os.ReadFile(path)
	if err != nil || json.Unmarshal(after, &out) != nil {
		t.Fatal("selection left malformed configuration")
	}
}

func TestProbeEndpointsRejectSlotsOutsideConfiguredPool(t *testing.T) {
	c := reproConfig(t)
	m := NewManager(c, &recordingRunner{}, &fakePlatform{})
	for _, slot := range []int{-1, c.Pool.Size} {
		if _, err := m.ProbeEndpoint(slot); err == nil {
			t.Fatalf("invalid slot %d accepted", slot)
		}
	}
	probe, err := m.ProbeEndpoint(1)
	if err != nil || probe.String() != m.SlotProxy(1) || probe.Hostname() != "127.0.0.1" {
		t.Fatalf("invalid probe endpoint: %v %v", probe, err)
	}
	health, err := m.HealthEndpoint()
	if err != nil || health.String() != m.HealthProxy() || health.Hostname() != "127.0.0.1" || health.Port() == probe.Port() {
		t.Fatalf("invalid health endpoint: %v %v", health, err)
	}
}
