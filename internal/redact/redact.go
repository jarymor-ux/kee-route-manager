// Package redact centralizes secret removal before logs, APIs and diagnostics.
package redact

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	urlRE    = regexp.MustCompile(`(?i)(?:https?|vless|vmess|trojan|ss)://[^\s<>"']+`)
	uuidRE   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	headerRE = regexp.MustCompile(`(?im)\b(Authorization|Cookie|Set-Cookie)\s*:\s*[^\r\n]*`)
	secretRE = regexp.MustCompile(`(?i)(["']?(?:uuid|password|public[_-]?key|pbk|short[_-]?id|sid|token|secret)["']?\s*[:=]\s*["']?)[^\s,"'}]+`)
	ipv4RE   = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	ipv6RE   = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){2,}[0-9a-f:]*\b`)
)

func URL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<redacted-url>"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "<redacted-node-uri>"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}
func Text(s string) string {
	s = urlRE.ReplaceAllStringFunc(s, URL)
	s = uuidRE.ReplaceAllString(s, "<redacted-uuid>")
	s = secretRE.ReplaceAllString(s, "${1}<redacted>")
	return headerRE.ReplaceAllString(s, "${1}: <redacted>")
}
func Diagnostics(s string) string {
	s = Text(s)
	s = urlRE.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "<redacted-url>"
		}
		u.Host = "<redacted-host>"
		return u.String()
	})
	s = ipv4RE.ReplaceAllString(s, "<redacted-ip>")
	return ipv6RE.ReplaceAllString(s, "<redacted-ip>")
}
func Headers(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		switch strings.ToLower(k) {
		case "authorization", "proxy-authorization", "cookie", "set-cookie":
			out[k] = "<redacted>"
		default:
			out[k] = Text(v)
		}
	}
	return out
}
