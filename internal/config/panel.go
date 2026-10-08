package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type TLSCertificate struct {
	Hostname string `json:"hostname"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

type PanelInput struct {
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
}
type PanelRevision struct {
	Revision string `json:"revision"`
}
type PanelStatus struct {
	Supported            bool      `json:"supported"`
	Hostname             string    `json:"hostname,omitempty"`
	Port                 int       `json:"port,omitempty"`
	ListenIP             string    `json:"listen_ip,omitempty"`
	URL                  string    `json:"url,omitempty"`
	Revision             string    `json:"revision,omitempty"`
	Status               string    `json:"status"`
	CertificatePEM       string    `json:"certificate_pem,omitempty"`
	CertificateChanged   bool      `json:"certificate_changed"`
	ConfirmationDeadline time.Time `json:"confirmation_deadline,omitempty"`
	Error                string    `json:"error,omitempty"`
}

var panelLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func ValidPanelHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 || host != strings.ToLower(host) || net.ParseIP(host) != nil || host == "localhost" || host == "local" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".localhost") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !panelLabel.MatchString(label) {
			return false
		}
	}
	return true
}
func ValidPanelPort(port int) bool { return port >= 1 && port <= 65535 && port != 9443 }

// Literal addresses may select only the already configured listener IP; this
// permits port-only changes without inventing a DNS alias or moving the UI.
func ValidPanelAddressHost(host, listenIP string) bool {
	return ValidPanelHostname(host) || host == listenIP && net.ParseIP(host) != nil
}
func PanelURL(host string, port int) string {
	if port == 443 {
		if strings.Contains(host, ":") {
			return "https://[" + host + "]"
		}
		return "https://" + host
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(port))
}
func PanelListen(c Config) (string, int, error) {
	host, port, err := listenPort(c.Web.Listen)
	ip := net.ParseIP(host)
	if err != nil || c.Instance.Role != "ui" || !c.UIProxy.Enabled || !c.Web.Enabled || !c.Web.TLS.Enabled || ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return "", 0, fmt.Errorf("panel address editing requires a managed HTTPS UI on a fixed local IP")
	}
	return host, port, nil
}
func (c Config) panelTLSErrors() []error {
	if len(c.API.TLS.AdditionalCertificates) != 0 {
		return []error{fmt.Errorf("additional certificates are supported only by the UI TLS listener")}
	}
	if len(c.Web.TLS.AdditionalCertificates) == 0 {
		return nil
	}
	if c.Instance.Role != "ui" || !c.Web.TLS.Enabled || len(c.Web.TLS.AdditionalCertificates) > 16 {
		return []error{fmt.Errorf("additional certificates require an HTTPS UI and at most 16 hostnames")}
	}
	seen := map[string]bool{}
	var errors []error
	for _, cert := range c.Web.TLS.AdditionalCertificates {
		if !ValidPanelHostname(cert.Hostname) || seen[cert.Hostname] || !filepath.IsAbs(cert.CertFile) || !filepath.IsAbs(cert.KeyFile) || cert.CertFile == cert.KeyFile || strings.ContainsRune(cert.CertFile+cert.KeyFile, 0) {
			errors = append(errors, fmt.Errorf("invalid additional UI certificate"))
		}
		seen[cert.Hostname] = true
	}
	return errors
}

// PanelConfig patches only the listener and optional SNI identities. Existing
// primary TLS paths, upstream trust, relative paths and other values survive.
func PanelConfig(data []byte, path string, input PanelInput, additional []TLSCertificate) ([]byte, Config, error) {
	c, err := LoadBytes(data, path)
	if err != nil {
		return nil, Config{}, err
	}
	ip, _, err := PanelListen(c)
	if err != nil || !ValidPanelAddressHost(input.Hostname, ip) || !ValidPanelPort(input.Port) {
		return nil, Config{}, fmt.Errorf("invalid panel address")
	}
	var document yaml.Node
	if yaml.Unmarshal(normalizeLegacyPlainMappingScalars(data), &document) != nil || len(document.Content) != 1 {
		return nil, Config{}, fmt.Errorf("invalid panel configuration")
	}
	values := map[string]any{"web": map[string]any{"listen": net.JoinHostPort(ip, strconv.Itoa(input.Port))}}
	if additional != nil {
		raw, _ := json.Marshal(additional)
		var certs any
		_ = json.Unmarshal(raw, &certs)
		values["web"].(map[string]any)["tls"] = map[string]any{"additional_certificates": certs}
	}
	patchSettingsNode(document.Content[0], values)
	// The supported YAML subset forbids nonempty flow mappings. The generic
	// scalar patcher decodes JSON arrays as flow nodes; certificate sequences
	// therefore need block mappings before serialization.
	if additional != nil {
		certs := document.Content[0]
		for _, key := range []string{"web", "tls", "additional_certificates"} {
			for i := 0; i+1 < len(certs.Content); i += 2 {
				if certs.Content[i].Value == key {
					certs = certs.Content[i+1]
					break
				}
			}
		}
		panelBlockNodes(certs)
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if encoder.Encode(&document) != nil || encoder.Close() != nil {
		return nil, Config{}, fmt.Errorf("encode panel configuration")
	}
	next, err := LoadBytes(out.Bytes(), path)
	return out.Bytes(), next, err
}

func panelBlockNodes(node *yaml.Node) {
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		node.Style = 0
	}
	for _, child := range node.Content {
		panelBlockNodes(child)
	}
}
