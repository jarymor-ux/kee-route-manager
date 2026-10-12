package config

import (
 "testing"
)

func TestLocalNetworkPlaintextUIPermittedWithoutCertificates(t *testing.T) {
 c := Default()
 c.Instance.Role = "ui"
 c.Platform.Kind = "keenetic"
 c.API.Enabled = false
 c.UIProxy.Enabled = true
 c.UIProxy.Upstream = "http://127.0.0.1:9443"
 c.Web.Listen = "192.168.1.1:9444"
 c.Web.TLS = TLS{}
 if err := c.Validate(); err != nil { t.Fatalf("local HTTP UI should not require certificates: %v", err) }
}

func TestPlaintextUIIsNotExposedToPublicOrWildcardBind(t *testing.T) {
 for _, listen := range []string{"8.8.8.8:9444", "0.0.0.0:9444"} {
  c := Default()
  c.Instance.Role = "ui"
  c.Platform.Kind = "keenetic"
  c.API.Enabled = false
  c.UIProxy.Enabled = true
  c.UIProxy.Upstream = "http://127.0.0.1:9443"
  c.Web.TLS = TLS{}
  c.Web.Listen = listen
  if err := c.Validate(); err == nil { t.Fatalf("untrusted HTTP listener %q accepted", listen) }
 }
}
