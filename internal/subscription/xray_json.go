package subscription

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jarymor-ux/kee-route-manager/internal/model"
)

var errXrayJSON = errors.New("invalid or unsupported Xray JSON subscription")

// JSON subscriptions are data, never executable Xray configuration. Only nodes
// that can be reproduced by the managed outbound builder cross this boundary.
func parseXrayJSON(text, source string) ([]model.Node, error) {
	if len(text) > 16<<20 || !utf8.ValidString(text) {
		return nil, errXrayJSON
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	budget := 200000
	v, err := readSubscriptionJSON(d, 0, &budget)
	if err != nil {
		return nil, errXrayJSON
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errXrayJSON
	}
	configs, ok := v.([]any)
	if !ok {
		configs = []any{v}
	}
	out := []model.Node{}
	for _, c := range configs {
		cfg, ok := c.(map[string]any)
		if !ok {
			return nil, errXrayJSON
		}
		label := stringField(cfg, "remarks")
		var outs []any
		if raw, exists := cfg["outbounds"]; exists {
			outs, ok = raw.([]any)
			if !ok {
				return nil, errXrayJSON
			}
		} else if stringField(cfg, "protocol") != "" {
			outs = []any{cfg}
		}
		for _, raw := range outs {
			ob, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, xrayOutboundNodes(ob, label, source)...)
		}
	}
	if len(out) == 0 {
		return nil, ErrNoSupportedNodes
	}
	return out, nil
}

// A bounded token decoder rejects duplicate keys instead of silently allowing
// later values to change transport or TLS interpretation.
func readSubscriptionJSON(d *json.Decoder, depth int, budget *int) (any, error) {
	*budget = *budget - 1
	if depth > 64 || *budget < 0 {
		return nil, errXrayJSON
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, errXrayJSON
			}
			if _, exists := m[key]; exists {
				return nil, errXrayJSON
			}
			val, err := readSubscriptionJSON(d, depth+1, budget)
			if err != nil {
				return nil, err
			}
			m[key] = val
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errXrayJSON
		}
		return m, nil
	case json.Delim('['):
		a := []any{}
		for d.More() {
			val, err := readSubscriptionJSON(d, depth+1, budget)
			if err != nil {
				return nil, err
			}
			a = append(a, val)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errXrayJSON
		}
		return a, nil
	default:
		if _, delimiter := t.(json.Delim); delimiter {
			return nil, errXrayJSON
		}
		return t, nil
	}
}

func xrayOutboundNodes(ob map[string]any, label, source string) []model.Node {
	if !textFields(ob, "protocol", "tag", "remarks") || !objectFields(ob, "settings", "streamSettings", "mux") || stringField(ob, "protocol") != "vless" || !onlyFields(ob, "protocol", "tag", "remarks", "settings", "streamSettings", "mux") {
		return nil
	}
	if mux := objectField(ob, "mux"); !onlyFields(mux, "concurrency", "xudpConcurrency", "xudpProxyUDP443") {
		return nil
	}
	servers, ok := xrayServers(objectField(ob, "settings"))
	if !ok {
		return nil
	}
	stream := objectField(ob, "streamSettings")
	if !textFields(stream, "network", "method", "security") || !objectFields(stream, "realitySettings", "tlsSettings", "wsSettings", "tcpSettings", "rawSettings") {
		return nil
	}
	if !onlyFields(stream, "network", "method", "security", "realitySettings", "tlsSettings", "wsSettings", "tcpSettings", "rawSettings") {
		return nil
	}
	network := def(stringField(stream, "network"), stringField(stream, "method"), "tcp")
	if other := stringField(stream, "method"); other != "" && stringField(stream, "network") != "" && normalizeNetwork(other) != normalizeNetwork(network) {
		return nil
	}
	network = normalizeNetwork(network)
	security := stringField(stream, "security")
	if !((network == "tcp" && security == "reality") || (network == "ws" && security == "tls")) {
		return nil
	}
	q := url.Values{"type": {network}, "security": {security}}
	if network == "tcp" {
		if !plainTCP(objectField(stream, "tcpSettings")) || !plainTCP(objectField(stream, "rawSettings")) {
			return nil
		}
		r := objectField(stream, "realitySettings")
		if !textFields(r, "serverName", "fingerprint", "publicKey", "password", "shortId", "spiderX") || !onlyFields(r, "serverName", "fingerprint", "publicKey", "password", "shortId", "spiderX") {
			return nil
		}
		pk, password := stringField(r, "publicKey"), stringField(r, "password")
		if pk != "" && password != "" && pk != password {
			return nil
		}
		for field, param := range map[string]string{"serverName": "sni", "fingerprint": "fp", "shortId": "sid", "spiderX": "spx"} {
			q.Set(param, stringField(r, field))
		}
		q.Set("pbk", def(password, pk))
	} else {
		tls := objectField(stream, "tlsSettings")
		if !textFields(tls, "serverName", "fingerprint") || !onlyFields(tls, "serverName", "fingerprint", "alpn") {
			return nil
		}
		q.Set("sni", stringField(tls, "serverName"))
		q.Set("fp", stringField(tls, "fingerprint"))
		if raw, exists := tls["alpn"]; exists {
			values, ok := raw.([]any)
			if !ok {
				return nil
			}
			alpn := []string{}
			for _, val := range values {
				s, ok := val.(string)
				if !ok || strings.Contains(s, ",") {
					return nil
				}
				alpn = append(alpn, s)
			}
			q.Set("alpn", strings.Join(alpn, ","))
		}
		w := objectField(stream, "wsSettings")
		if !textFields(w, "path", "host") || !objectFields(w, "headers") || !onlyFields(w, "path", "host", "headers") {
			return nil
		}
		q.Set("path", stringField(w, "path"))
		headers := objectField(w, "headers")
		host := stringField(w, "host")
		for name, raw := range headers {
			value, ok := raw.(string)
			if !ok || !strings.EqualFold(name, "Host") || (host != "" && value != host) {
				return nil
			}
			host = value
		}
		q.Set("host", host)
	}
	nodes := []model.Node{}
	for _, raw := range servers {
		server, ok := raw.(map[string]any)
		if !ok || !textFields(server, "address") || !onlyFields(server, "address", "port", "users") {
			continue
		}
		address := stringField(server, "address")
		if ip := net.ParseIP(address); ip != nil && (ip.IsUnspecified() || ip.IsLoopback()) {
			continue
		}
		port, ok := server["port"].(json.Number)
		if !ok {
			continue
		}
		p, err := strconv.Atoi(string(port))
		if err != nil || p < 1 || p > 65535 {
			continue
		}
		users, ok := server["users"].([]any)
		if !ok {
			continue
		}
		for _, raw := range users {
			user, ok := raw.(map[string]any)
			if !ok || !textFields(user, "id", "encryption", "flow", "email") || !onlyFields(user, "id", "encryption", "flow", "email", "level") {
				continue
			}
			id := stringField(user, "id")
			if id == "00000000-0000-0000-0000-000000000000" {
				continue
			}
			encryption := def(stringField(user, "encryption"), "none")
			if encryption != "none" {
				continue
			}
			flow := stringField(user, "flow")
			if flow != "" && flow != "xtls-rprx-vision" && flow != "xtls-rprx-vision-udp443" {
				continue
			}
			if network == "ws" && flow != "" {
				continue
			}
			params := url.Values{}
			for key, values := range q {
				params[key] = values
			}
			params.Set("encryption", encryption)
			params.Set("flow", flow)
			name := def(label, stringField(ob, "remarks"), stringField(ob, "tag"))
			uri := url.URL{Scheme: "vless", User: url.User(id), Host: net.JoinHostPort(address, strconv.Itoa(p)), RawQuery: params.Encode(), Fragment: name}
			node, err := ParseVLESS(uri.String(), source)
			if err == nil {
				nodes = append(nodes, node)
			}
		}
	}
	return nodes
}

// Normalize the two official VLESS settings shapes into the same bounded
// node-extraction path. Mixed shapes are ambiguous and must not override one another.
func xrayServers(settings map[string]any) ([]any, bool) {
	flatFields := []string{"address", "port", "id", "encryption", "flow", "email", "level"}
	if raw, exists := settings["vnext"]; exists {
		for _, key := range flatFields {
			if _, mixed := settings[key]; mixed {
				return nil, false
			}
		}
		if !onlyFields(settings, "vnext") {
			return nil, false
		}
		servers, ok := raw.([]any)
		return servers, ok
	}
	if !onlyFields(settings, flatFields...) || !textFields(settings, "address", "id", "encryption", "flow", "email") {
		return nil, false
	}
	user := map[string]any{}
	for _, key := range []string{"id", "encryption", "flow", "email", "level"} {
		if value, exists := settings[key]; exists {
			user[key] = value
		}
	}
	return []any{map[string]any{"address": settings["address"], "port": settings["port"], "users": []any{user}}}, true
}

func normalizeNetwork(s string) string {
	switch s {
	case "raw":
		return "tcp"
	case "websocket":
		return "ws"
	}
	return s
}
func plainTCP(m map[string]any) bool {
	if !objectFields(m, "header") || !onlyFields(m, "header") {
		return false
	}
	h := objectField(m, "header")
	return textFields(h, "type") && onlyFields(h, "type") && (stringField(h, "type") == "" || stringField(h, "type") == "none")
}
func stringField(m map[string]any, key string) string { v, _ := m[key].(string); return v }
func objectField(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}
func onlyFields(m map[string]any, fields ...string) bool {
	for key, val := range m {
		supported := false
		for _, field := range fields {
			if key == field {
				supported = true
				break
			}
		}
		if !supported && !inactiveJSON(val) {
			return false
		}
	}
	return true
}
func inactiveJSON(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case json.Number:
		return x == "0"
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

func textFields(m map[string]any, fields ...string) bool {
	for _, key := range fields {
		if value, exists := m[key]; exists && value != nil {
			if _, ok := value.(string); !ok {
				return false
			}
		}
	}
	return true
}
func objectFields(m map[string]any, fields ...string) bool {
	for _, key := range fields {
		if value, exists := m[key]; exists && value != nil {
			if _, ok := value.(map[string]any); !ok {
				return false
			}
		}
	}
	return true
}
