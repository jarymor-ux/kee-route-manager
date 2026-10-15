package subscription

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// All identities and endpoints in these fixtures are synthetic.
func xrayRealityFixture() map[string]any {
	return map[string]any{
		"tag": "Node A", "protocol": "vless",
		"settings":       map[string]any{"vnext": []any{map[string]any{"address": "example.com", "port": 443, "users": []any{map[string]any{"id": "11111111-1111-1111-1111-111111111111", "encryption": "none"}}}}},
		"streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"serverName": "example.com", "publicKey": "abc", "fingerprint": "chrome", "show": false}, "tcpSettings": map[string]any{"header": map[string]any{"type": "none"}}},
		"mux":            map[string]any{"enabled": false, "concurrency": 8},
	}
}
func xrayWSFixture() map[string]any {
	ob := xrayRealityFixture()
	ob["tag"] = "Node B"
	server := ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)
	server["address"] = "ws.example.com"
	server["users"] = []any{map[string]any{"id": "22222222-2222-2222-2222-222222222222", "encryption": "none"}}
	ob["streamSettings"] = map[string]any{"network": "ws", "security": "tls", "tlsSettings": map[string]any{"serverName": "ws.example.com", "allowInsecure": false}, "wsSettings": map[string]any{"path": "/ws", "headers": map[string]any{"Host": "ws.example.com"}}}
	return ob
}
func fixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestXrayJSONSupportedShapesAndURIIdentity(t *testing.T) {
	r, w := xrayRealityFixture(), xrayWSFixture()
	// Provider-level routing, DNS, inbounds and local policy are never imported.
	config := map[string]any{"outbounds": []any{r, w, map[string]any{"protocol": "freedom"}}, "routing": map[string]any{"rules": []any{map[string]any{"outboundTag": "direct"}}}, "dns": map[string]any{"servers": []any{"127.0.0.1"}}, "inbounds": []any{map[string]any{"port": 1080}}}
	for name, payload := range map[string][]byte{
		"config":        fixtureJSON(t, config),
		"config array":  fixtureJSON(t, []any{map[string]any{"outbounds": []any{r}}, map[string]any{"outbounds": []any{w}}}),
		"base64 config": []byte(base64.StdEncoding.EncodeToString(fixtureJSON(t, config))),
	} {
		t.Run(name, func(t *testing.T) {
			nodes, err := ParsePayload(payload, "provider")
			if err != nil || len(nodes) != 2 {
				t.Fatalf("count=%d err=%v", len(nodes), err)
			}
			for i, uri := range []string{reality, ws} {
				expected, err := ParseVLESS(uri, "provider")
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(nodes[i], expected) {
					t.Fatalf("transport identity changed for index %d", i)
				}
			}
		})
	}
	nodes, err := ParsePayload(fixtureJSON(t, r), "provider")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("standalone outbound: count=%d err=%v", len(nodes), err)
	}
}
func TestXrayJSONRealityPasswordAndModernTransportAliases(t *testing.T) {
	ob := xrayRealityFixture()
	stream := ob["streamSettings"].(map[string]any)
	realitySettings := stream["realitySettings"].(map[string]any)
	delete(realitySettings, "publicKey")
	realitySettings["password"] = "abc"
	delete(stream, "network")
	stream["method"] = "raw"
	nodes, err := ParsePayload(fixtureJSON(t, map[string]any{"remarks": "Profile label", "outbounds": []any{ob}}), "p")
	if err != nil || len(nodes) != 1 || nodes[0].PublicKey != "abc" || nodes[0].Network != "tcp" || nodes[0].Label != "Profile label" {
		t.Fatalf("count=%d err=%v", len(nodes), err)
	}
	ws := xrayWSFixture()
	wsStream := ws["streamSettings"].(map[string]any)
	wsStream["network"] = "websocket"
	tls := wsStream["tlsSettings"].(map[string]any)
	tls["alpn"] = []any{"h2", "http/1.1"}
	tls["fingerprint"] = "firefox"
	nodes, err = ParsePayload(fixtureJSON(t, ws), "p")
	if err != nil || len(nodes) != 1 || !reflect.DeepEqual(nodes[0].ALPN, []string{"h2", "http/1.1"}) || nodes[0].Fingerprint != "firefox" {
		t.Fatalf("count=%d err=%v", len(nodes), err)
	}
}
func TestXrayJSONRefusesUnrepresentableOutbound(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"outbound chain": func(ob map[string]any) { ob["proxySettings"] = map[string]any{"tag": "hop"} },
		"dialer proxy": func(ob map[string]any) {
			ob["streamSettings"].(map[string]any)["sockopt"] = map[string]any{"dialerProxy": "hop"}
		},
		"local source":          func(ob map[string]any) { ob["sendThrough"] = "127.0.0.2" },
		"enabled mux":           func(ob map[string]any) { ob["mux"].(map[string]any)["enabled"] = true },
		"unsupported transport": func(ob map[string]any) { ob["streamSettings"].(map[string]any)["network"] = "xhttp" },
		"http obfuscation": func(ob map[string]any) {
			ob["streamSettings"].(map[string]any)["tcpSettings"] = map[string]any{"header": map[string]any{"type": "http"}}
		},
		"reality extra verification": func(ob map[string]any) {
			ob["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)["mldsa65Verify"] = "verification-key"
		},
		"conflicting key aliases": func(ob map[string]any) {
			ob["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)["password"] = "different"
		},
		"bad field type": func(ob map[string]any) {
			ob["streamSettings"].(map[string]any)["realitySettings"] = "ignored-by-loose-parser"
		},
		"conflicting network aliases": func(ob map[string]any) { ob["streamSettings"].(map[string]any)["method"] = "websocket" },
		"new encryption": func(ob map[string]any) {
			ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)["encryption"] = "mlkem768x25519plus.native"
		},
		"placeholder UUID": func(ob map[string]any) {
			ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)["id"] = "00000000-0000-0000-0000-000000000000"
		},
		"unspecified address": func(ob map[string]any) {
			ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["address"] = "0.0.0.0"
		},
		"loopback address": func(ob map[string]any) {
			ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["address"] = "::1"
		},
	} {
		t.Run(name, func(t *testing.T) {
			ob := xrayRealityFixture()
			change(ob)
			nodes, err := ParsePayload(fixtureJSON(t, map[string]any{"outbounds": []any{ob}}), "p")
			if !errors.Is(err, ErrNoSupportedNodes) || len(nodes) != 0 {
				t.Fatalf("unsupported outbound accepted: count=%d err=%v", len(nodes), err)
			}
			// A rejected chain cannot prevent a separate, fully representable outbound.
			nodes, err = ParsePayload(fixtureJSON(t, map[string]any{"outbounds": []any{ob, xrayWSFixture()}}), "p")
			if err != nil || len(nodes) != 1 || nodes[0].Network != "ws" {
				t.Fatalf("safe peer was not isolated: count=%d err=%v", len(nodes), err)
			}
		})
	}
}
func TestXrayJSONRefusesTLSAndWebSocketSemanticLoss(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"insecure": func(s map[string]any) { s["tlsSettings"].(map[string]any)["allowInsecure"] = true },
		"pinning":  func(s map[string]any) { s["tlsSettings"].(map[string]any)["pinnedPeerCertSha256"] = []any{"pin"} },
		"custom trust": func(s map[string]any) {
			s["tlsSettings"].(map[string]any)["certificates"] = []any{map[string]any{"certificateFile": "/provider/path"}}
		},
		"disable roots":  func(s map[string]any) { s["tlsSettings"].(map[string]any)["disableSystemRoot"] = true },
		"bad tls object": func(s map[string]any) { s["tlsSettings"] = []any{} },
		"extra header": func(s map[string]any) {
			s["wsSettings"].(map[string]any)["headers"].(map[string]any)["Authorization"] = "secret"
		},
		"conflicting host": func(s map[string]any) { s["wsSettings"].(map[string]any)["host"] = "other.example" },
		"early data":       func(s map[string]any) { s["wsSettings"].(map[string]any)["maxEarlyData"] = 2048 },
		"bad path type":    func(s map[string]any) { s["wsSettings"].(map[string]any)["path"] = 42 },
	} {
		t.Run(name, func(t *testing.T) {
			ob := xrayWSFixture()
			change(ob["streamSettings"].(map[string]any))
			if nodes, err := ParsePayload(fixtureJSON(t, ob), "p"); err == nil || len(nodes) != 0 {
				t.Fatalf("unsafe conversion count=%d err=%v", len(nodes), err)
			}
		})
	}
}
func TestXrayJSONMalformedAndResourceLimits(t *testing.T) {
	good := string(fixtureJSON(t, xrayRealityFixture()))
	for name, payload := range map[string]string{
		"truncated": good[:len(good)-1], "trailing JSON": good + `{}`, "trailing URI": good + " " + reality,
		"duplicate member":  strings.Replace(good, `"protocol":"vless"`, `"protocol":"freedom","protocol":"vless"`, 1),
		"escaped duplicate": `{"outbounds":[],"out\u0062ounds":[]}`, "deep": strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66),
		"scalar array": `[42]`, "wrong outbounds": `{"outbounds":{}}`,
		"oversize": `{"padding":"` + strings.Repeat("a", 16<<20) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			nodes, err := ParsePayload([]byte(payload), "p")
			if err == nil || len(nodes) != 0 {
				t.Fatalf("malformed JSON accepted count=%d", len(nodes))
			}
			if strings.Contains(err.Error(), "11111111") || strings.Contains(err.Error(), "example.com") {
				t.Fatal("error exposed provider content")
			}
		})
	}
}
func TestProviderDummyDiagnosticAndURISafety(t *testing.T) {
	dummy := `vless://00000000-0000-0000-0000-000000000000@0.0.0.0:1?encryption=none&type=tcp&security=none#App%20not%20supported`
	for _, payload := range []string{dummy, base64.StdEncoding.EncodeToString([]byte(dummy))} {
		if _, err := ParsePayload([]byte(payload), "p"); !errors.Is(err, ErrProviderAppUnsupported) {
			t.Fatalf("missing provider diagnostic: %v", err)
		}
	}
	nodes, err := ParsePayload([]byte(dummy+"\n"+reality), "p")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("valid URI peer lost: count=%d err=%v", len(nodes), err)
	}
	if _, err := ParseVLESS(strings.Replace(reality, "11111111-1111-1111-1111-111111111111", "00000000-0000-0000-0000-000000000000", 1), "p"); err == nil {
		t.Fatal("placeholder UUID accepted")
	}
}
func TestXrayJSONMultipleIdentitiesAndBounds(t *testing.T) {
	ob := xrayRealityFixture()
	server := ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)
	users := server["users"].([]any)
	users = append(users, map[string]any{"id": "33333333-3333-3333-3333-333333333333", "flow": "xtls-rprx-vision"})
	server["users"] = users
	r := ob["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)
	r["shortId"] = "abcdef"
	r["spiderX"] = "/path?x=1"
	nodes, err := ParsePayload(fixtureJSON(t, ob), "p")
	if err != nil || len(nodes) != 2 || nodes[0].ID == nodes[1].ID || nodes[1].Flow != "xtls-rprx-vision" || nodes[1].ShortID != "abcdef" || nodes[1].SpiderX != "/path?x=1" {
		t.Fatalf("identity conversion count=%d err=%v", len(nodes), err)
	}
	if _, err := parseXrayJSON("["+strings.Repeat("0,", 200001)+"0]", "p"); !errors.Is(err, errXrayJSON) {
		t.Fatalf("missing token bound: %v", err)
	}
	if _, err := parseXrayJSON("{\"invalid\":\""+string([]byte{0xff})+"\"}", "p"); !errors.Is(err, errXrayJSON) {
		t.Fatalf("invalid UTF-8 accepted: %v", err)
	}
}
func flatXrayFixture(ob map[string]any) map[string]any {
	server := ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)
	user := server["users"].([]any)[0].(map[string]any)
	settings := map[string]any{"address": server["address"], "port": server["port"]}
	for key, value := range user {
		settings[key] = value
	}
	ob["settings"] = settings
	return ob
}

func TestXrayJSONFlatSettingsPreserveURIIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, uri, flow string
		fixture         func() map[string]any
	}{
		{"Reality TCP", reality, "", xrayRealityFixture},
		{"Reality TCP Vision", strings.Replace(reality, "type=tcp", "type=tcp&flow=xtls-rprx-vision", 1), "xtls-rprx-vision", xrayRealityFixture},
		{"Reality TCP Vision udp443", strings.Replace(reality, "type=tcp", "type=tcp&flow=xtls-rprx-vision-udp443", 1), "xtls-rprx-vision-udp443", xrayRealityFixture},
		{"WebSocket TLS", ws, "", xrayWSFixture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ob := tc.fixture()
			ob["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)["flow"] = tc.flow
			expected, err := ParseVLESS(tc.uri, "p")
			if err != nil {
				t.Fatal(err)
			}
			for _, payload := range []any{ob, map[string]any{"outbounds": []any{flatXrayFixture(tc.fixture())}}} {
				// Exercise the legacy flow path and the normalized flat path equally.
				target := payload.(map[string]any)
				if outbounds, ok := target["outbounds"].([]any); ok {
					outbounds[0].(map[string]any)["settings"].(map[string]any)["flow"] = tc.flow
				}
				nodes, err := ParsePayload(fixtureJSON(t, payload), "p")
				if err != nil || len(nodes) != 1 || !reflect.DeepEqual(nodes[0], expected) {
					t.Fatalf("conversion changed identity: count=%d err=%v", len(nodes), err)
				}
			}
		})
	}
}

func TestXrayJSONFlatSettingsRejectAmbiguityAndUnsupportedFields(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"mixed vnext":      func(s map[string]any) { s["vnext"] = []any{} },
		"mixed null vnext": func(s map[string]any) { s["vnext"] = nil },
		"reverse":          func(s map[string]any) { s["reverse"] = map[string]any{"tag": "reverse-target"} },
		"fallback":         func(s map[string]any) { s["fallbacks"] = []any{map[string]any{"dest": 80}} },
		"new encryption":   func(s map[string]any) { s["encryption"] = "mlkem768x25519plus.native" },
		"test seed":        func(s map[string]any) { s["testseed"] = []any{1, 2, 3, 4} },
		"bad port type":    func(s map[string]any) { s["port"] = "443" },
		"bad id type":      func(s map[string]any) { s["id"] = 123 },
	} {
		t.Run(name, func(t *testing.T) {
			ob := flatXrayFixture(xrayRealityFixture())
			change(ob["settings"].(map[string]any))
			nodes, err := ParsePayload(fixtureJSON(t, ob), "p")
			if err == nil || len(nodes) != 0 {
				t.Fatalf("unsupported flat config accepted: count=%d", len(nodes))
			}
		})
	}
	ob := xrayRealityFixture()
	ob["settings"].(map[string]any)["address"] = ""
	if _, err := ParsePayload(fixtureJSON(t, ob), "p"); err == nil {
		t.Fatal("mixed vnext/flat settings with empty field accepted")
	}
	wsOutbound := flatXrayFixture(xrayWSFixture())
	wsOutbound["settings"].(map[string]any)["flow"] = "xtls-rprx-vision-udp443"
	if _, err := ParsePayload(fixtureJSON(t, wsOutbound), "p"); err == nil {
		t.Fatal("Reality-only flow accepted on WebSocket TLS")
	}
}

func FuzzXrayJSONSubscription(f *testing.F) {
	seed, _ := json.Marshal(xrayRealityFixture())
	f.Add(string(seed))
	flatSeed, _ := json.Marshal(flatXrayFixture(xrayRealityFixture()))
	f.Add(string(flatSeed))
	f.Add(`{"outbounds":[]}`)
	f.Add(`[{"outbounds":[{"protocol":"vless"}]}]`)
	f.Fuzz(func(t *testing.T, payload string) {
		if len(payload) > 8192 {
			t.Skip()
		}
		nodes, err := parseXrayJSON(payload, "fixture")
		if err != nil {
			return
		}
		for _, n := range nodes {
			if n.ID == "" || !uuidOK(n.UUID) || n.Port < 1 || n.Port > 65535 {
				t.Fatal("invalid node")
			}
		}
	})
}
