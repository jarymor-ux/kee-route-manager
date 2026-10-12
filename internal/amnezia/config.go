// Package amnezia parses untrusted AmneziaWG client profiles without executing directives.
package amnezia

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

type Interface struct {
	PrivateKey          string
	Addresses           []string
	DNS                 []string
	MTU                 int
	Jc, Jmin, Jmax      int
	S1, S2, S3, S4      int
	H1, H2, H3, H4      string
	HeaderProtectionKey string
	Additional          map[string]string
}

type Peer struct {
	PublicKey           string
	PreSharedKey        string
	Endpoint            string
	AllowedIPs          []string
	PersistentKeepalive string
}

type Profile struct {
	Interface Interface
	Peer      Peer
}

// Summary intentionally excludes private/public keys, obfuscation signatures and secrets.
func (p Profile) Summary() string {
	return fmt.Sprintf("AmneziaWG %s, %d allowed networks", p.Peer.Endpoint, len(p.Peer.AllowedIPs))
}

func ParseConfig(data []byte) (Profile, error) {
	if len(data) == 0 || len(data) > 256*1024 {
		return Profile{}, errors.New("AmneziaWG configuration must be 1..262144 bytes")
	}
	var p Profile
	p.Interface.Additional = map[string]string{}
	section := ""
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	scanner.Buffer(make([]byte, 1024), 256*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			switch strings.ToLower(line) {
			case "[interface]":
				section = "interface"
			case "[device]":
				section = "device"
			case "[peer]":
				section = "peer"
			default:
				return Profile{}, fmt.Errorf("unsupported profile section on line %d", lineNum)
			}
			if seen["section:"+section] {
				return Profile{}, fmt.Errorf("duplicate %s section", section)
			}
			seen["section:"+section] = true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			return Profile{}, fmt.Errorf("invalid configuration on line %d", lineNum)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if value == "" {
			return Profile{}, fmt.Errorf("empty %s", key)
		}
		if seen[section+":"+key] {
			return Profile{}, fmt.Errorf("duplicate key %s", key)
		}
		seen[section+":"+key] = true
		// Shell hooks are never accepted as configuration; KRM never executes profile content.
		switch key {
		case "postup", "postdown", "preup", "predown", "saveconfig", "table":
			return Profile{}, fmt.Errorf("profile directive %q is not allowed", key)
		}
		if section == "interface" || section == "device" {
			if section == "device" && (key=="privatekey" || key=="address" || key=="dns" || key=="mtu") {return Profile{},fmt.Errorf("invalid Device directive %q",key)}
			switch key {
			case "privatekey":
				p.Interface.PrivateKey = value
			case "address":
				p.Interface.Addresses = fields(value)
			case "dns":
				p.Interface.DNS = fields(value)
			case "mtu":
				n, e := strconv.Atoi(value)
				if e != nil || n < 576 || n > 9000 {
					return Profile{}, fmt.Errorf("invalid MTU")
				}
				p.Interface.MTU = n
			case "jc":
				n, e := positiveInt(value)
				if e != nil {
					return Profile{}, e
				}
				p.Interface.Jc = n
			case "jmin":
				n, e := positiveInt(value)
				if e != nil {
					return Profile{}, e
				}
				p.Interface.Jmin = n
			case "jmax":
				n, e := positiveInt(value)
				if e != nil {
					return Profile{}, e
				}
				p.Interface.Jmax = n
			case "s1", "s2", "s3", "s4":
				n, e := positiveInt(value)
				if e != nil {
					return Profile{}, e
				}
				switch key {
				case "s1":
					p.Interface.S1 = n
				case "s2":
					p.Interface.S2 = n
				case "s3":
					p.Interface.S3 = n
				case "s4":
					p.Interface.S4 = n
				}
			case "h1", "h2", "h3", "h4":
				if !validUIntRange(value) {
					return Profile{}, fmt.Errorf("invalid header range")
				}
				switch key {
				case "h1":
					p.Interface.H1 = value
				case "h2":
					p.Interface.H2 = value
				case "h3":
					p.Interface.H3 = value
				case "h4":
					p.Interface.H4 = value
				}
			case "headerprotectionkey":
				if !validKey(value) {
					return Profile{}, fmt.Errorf("invalid header protection key")
				}
				p.Interface.HeaderProtectionKey = value
			case "i1", "i2", "i3", "i4", "i5":
				if len(value) > 512 {
					return Profile{}, fmt.Errorf("oversized signature")
				}
				p.Interface.Additional[key] = value
			case "contentpaddingaddition", "rekeyaftertime", "rekeytimeout", "rejectaftertime", "keepalivetimeout", "maxhandshakeattempts":
				if !validUIntRange(value) {
					return Profile{}, fmt.Errorf("invalid range %s", key)
				}
				p.Interface.Additional[key] = value
			default:
				return Profile{}, fmt.Errorf("unsupported interface directive %q", key)
			}
		} else {
			switch key {
			case "publickey":
				p.Peer.PublicKey = value
			case "presharedkey":
				p.Peer.PreSharedKey = value
			case "endpoint":
				p.Peer.Endpoint = value
			case "allowedips":
				p.Peer.AllowedIPs = fields(value)
			case "persistentkeepalive":
				if !validUIntRange(value) {
					return Profile{}, fmt.Errorf("invalid keepalive")
				}
				p.Peer.PersistentKeepalive = value
			default:
				return Profile{}, fmt.Errorf("unsupported peer directive %q", key)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Profile{}, err
	}
	if !seen["section:interface"] || !seen["section:peer"] {
		return Profile{}, errors.New("Interface and Peer sections required")
	}
	if !validKey(p.Interface.PrivateKey) || !validKey(p.Peer.PublicKey) {
		return Profile{}, errors.New("invalid WireGuard key")
	}
	if p.Peer.PreSharedKey != "" && !validKey(p.Peer.PreSharedKey) {
		return Profile{}, errors.New("invalid preshared key")
	}
	if len(p.Interface.Addresses) == 0 || len(p.Peer.AllowedIPs) == 0 {
		return Profile{}, errors.New("interface address and allowed IPs required")
	}
	for _, s := range p.Interface.Addresses {
		if _, e := netip.ParsePrefix(s); e != nil {
			return Profile{}, fmt.Errorf("invalid interface address")
		}
	}
	for _, s := range p.Peer.AllowedIPs {
		if _, e := netip.ParsePrefix(s); e != nil {
			return Profile{}, fmt.Errorf("invalid allowed IP")
		}
	}
	for _, s := range p.Interface.DNS {
		if _, e := netip.ParseAddr(s); e != nil {
			return Profile{}, fmt.Errorf("invalid DNS address")
		}
	}
	host, portText, e := net.SplitHostPort(p.Peer.Endpoint)
	if e != nil || host == "" {
		return Profile{}, errors.New("invalid endpoint")
	}
	port, e := strconv.Atoi(portText)
	if e != nil || port < 1 || port > 65535 {
		return Profile{}, errors.New("invalid endpoint port")
	}
	if p.Interface.Jmax < p.Interface.Jmin {
		return Profile{}, errors.New("invalid junk size range")
	}
	if p.Interface.HeaderProtectionKey != "" && (p.Interface.S1 < 12 || p.Interface.S2 < 12 || p.Interface.S3 < 12 || p.Interface.S4 < 12) {
		return Profile{}, errors.New("header protection needs S1-S4 >= 12")
	}
	return p, nil
}

func fields(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, v := range parts {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func positiveInt(s string) (int, error) {
	n, e := strconv.Atoi(s)
	if e != nil || n < 0 || n > 65535 {
		return 0, errors.New("invalid nonnegative integer")
	}
	return n, nil
}
func validUIntRange(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) > 2 {
		return false
	}
	n, e := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 32)
	if e != nil {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	end, e := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
	return e == nil && end >= n
}
func validKey(s string) bool {
	v, e := base64.StdEncoding.DecodeString(s)
	return e == nil && len(v) == 32
}
