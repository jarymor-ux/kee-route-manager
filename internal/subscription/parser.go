package subscription

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jarymor-ux/kee-route-manager/internal/model"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

func ParsePayload(data []byte, source string) ([]model.Node, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty subscription")
	}
	text := strings.TrimSpace(string(data))
	candidates := []string{text}
	compact := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, text)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(compact); err == nil && utf8.Valid(b) {
			candidates = append(candidates, string(b))
		}
	}
	var best []model.Node
	for _, c := range candidates {
		xs := parseLines(c, source)
		if len(xs) > len(best) {
			best = xs
		}
	}
	if len(best) == 0 {
		return nil, fmt.Errorf("no supported VLESS Reality TCP or VLESS WS/TLS nodes")
	}
	return best, nil
}
func parseLines(text, source string) []model.Node {
	fields := strings.FieldsFunc(strings.ReplaceAll(text, "\r", "\n"), func(r rune) bool { return r == '\n' || r == '\t' || r == ' ' || r == ',' })
	out := []model.Node{}
	for _, v := range fields {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), "vless://") {
			if n, err := ParseVLESS(strings.TrimSpace(v), source); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}
func ParseVLESS(raw, source string) (model.Node, error) {
	if len(raw) > 16384 {
		return model.Node{}, fmt.Errorf("URI too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return model.Node{}, err
	}
	if strings.ToLower(u.Scheme) != "vless" {
		return model.Node{}, fmt.Errorf("unsupported scheme")
	}
	id := ""
	if u.User != nil {
		id = u.User.Username()
	}
	if !uuidOK(id) {
		return model.Node{}, fmt.Errorf("invalid UUID")
	}
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil || host == "" || port < 1 || port > 65535 {
		return model.Node{}, fmt.Errorf("invalid endpoint")
	}
	q := u.Query()
	network := lowerDefault(q.Get("type"), "tcp")
	security := lowerDefault(q.Get("security"), "none")
	if !((network == "tcp" && security == "reality") || (network == "ws" && security == "tls")) {
		return model.Node{}, fmt.Errorf("unsupported transport %s/%s", network, security)
	}
	label, _ := url.PathUnescape(u.Fragment)
	label = sanitize(label)
	if label == "" {
		label = net.JoinHostPort(host, u.Port())
	}
	n := model.Node{Label: label, Protocol: "vless", Address: host, Port: port, UUID: id, Flow: q.Get("flow"), Encryption: def(q.Get("encryption"), "none"), Network: network, Security: security, Fingerprint: q.Get("fp"), ServerName: def(q.Get("sni"), q.Get("serverName")), PublicKey: def(q.Get("pbk"), q.Get("publicKey")), ShortID: def(q.Get("sid"), q.Get("shortId")), SpiderX: def(q.Get("spx"), q.Get("spiderX")), WSHost: q.Get("host"), WSPath: q.Get("path"), Sources: []string{source}}
	if security == "reality" && (n.ServerName == "" || n.PublicKey == "") {
		return model.Node{}, fmt.Errorf("Reality node missing SNI/public key")
	}
	if network == "ws" && n.WSPath == "" {
		n.WSPath = "/"
	}
	if a := q.Get("alpn"); a != "" {
		for _, x := range strings.Split(a, ",") {
			if x = strings.TrimSpace(x); x != "" {
				n.ALPN = append(n.ALPN, x)
			}
		}
	}
	n.ID = Fingerprint(n)
	return n, nil
}
func Fingerprint(n model.Node) string {
	v := struct {
		Protocol, Address                                                                                               string
		Port                                                                                                            int
		UUID, Flow, Encryption, Network, Security, Fingerprint, ServerName, PublicKey, ShortID, SpiderX, WSHost, WSPath string
		ALPN                                                                                                            []string
	}{n.Protocol, strings.ToLower(n.Address), n.Port, n.UUID, n.Flow, n.Encryption, n.Network, n.Security, n.Fingerprint, n.ServerName, n.PublicKey, n.ShortID, n.SpiderX, n.WSHost, n.WSPath, n.ALPN}
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}
func Merge(xs []model.Node, max int) []model.Node {
	m := map[string]model.Node{}
	for _, n := range xs {
		if old, ok := m[n.ID]; ok {
			old.Sources = uniq(append(old.Sources, n.Sources...))
			m[n.ID] = old
		} else {
			n.Sources = uniq(n.Sources)
			m[n.ID] = n
		}
	}
	out := make([]model.Node, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label == out[j].Label {
			return out[i].ID < out[j].ID
		}
		return out[i].Label < out[j].Label
	})
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}
func uuidOK(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > 256 {
		s = s[:256]
	}
	return s
}
func def(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
func lowerDefault(v, d string) string {
	if v == "" {
		return d
	}
	return strings.ToLower(v)
}
func uniq(xs []string) []string {
	m := map[string]bool{}
	for _, x := range xs {
		if x != "" {
			m[x] = true
		}
	}
	out := make([]string, 0, len(m))
	for x := range m {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}
