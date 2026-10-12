package setup

import (
 "path/filepath"
 "testing"

 "github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestPreparedLocalUITemplatesAvoidHTTPSAndWildcardHTTP(t *testing.T) {
 for _, name := range []string{"ui-keenetic.yaml", "ui-linux-openwrt.yaml"} {
  t.Run(name, func(t *testing.T) {
   cfg,err:=config.Load(filepath.Join("..","..","configs",name))
   if err!=nil {t.Fatal(err)}
   if cfg.Web.TLS.Enabled || cfg.Web.TLS.AutoGenerate {
    t.Fatal("new UI template must not require HTTPS certificates")
   }
   if cfg.Web.Listen!="127.0.0.1:9444" {
    t.Fatalf("prepared template should default to loopback until a private LAN IP is explicitly chosen: %q", cfg.Web.Listen)
   }
   if cfg.UIProxy.Upstream!="http://127.0.0.1:9443" || cfg.API.Enabled {
    t.Fatal("UI must not expose the controller API")
   }
  })
 }
}
