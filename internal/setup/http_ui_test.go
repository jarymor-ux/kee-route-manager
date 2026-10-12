package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestNewLocalUIUsesHTTPWithoutCertificates(t *testing.T) {
	c, err := BuildUIConfig(UIOptions{
		Platform: PlatformKeenetic,
		Listen:   "192.168.1.1:9444",
		Upstream: "http://127.0.0.1:9443",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Web.TLS.Enabled || c.API.TLS.Enabled || c.UIProxy.Upstream != "http://127.0.0.1:9443" {
		t.Fatalf("new local UI must use HTTP without certificates: %+v", c)
	}
	if got := panelURL("192.168.1.1:9444", ""); got != "http://192.168.1.1:9444" {
		t.Fatalf("new panel URL %q", got)
	}
}

func TestInteractiveLocalUIInstallRequiresNoTLSStaging(t *testing.T) {
	base := t.TempDir()
	controller := filepath.Join(base, "controller.yaml")
	ui := filepath.Join(base, "ui.yaml")
	stagedTLS := filepath.Join(base, "tls")
	script := wizardScript("2", false, false, "") + "\n\ny\n"
	var out strings.Builder
	if err := InitLocalUIConfig(strings.NewReader(script), &out, controller, ui, stagedTLS, PlatformKeenetic); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stagedTLS); !os.IsNotExist(err) {
		t.Fatalf("unexpected certificate staging: %v", err)
	}
	controllerConfig, err := config.Load(controller)
	if err != nil {
		t.Fatal(err)
	}
	uiConfig, err := config.Load(ui)
	if err != nil {
		t.Fatal(err)
	}
	if controllerConfig.API.TLS.Enabled || uiConfig.Web.TLS.Enabled {
		t.Fatal("installer unexpectedly enabled TLS")
	}
	if uiConfig.UIProxy.Upstream != "http://127.0.0.1:9443" {
		t.Fatalf("unexpected upstream: %q", uiConfig.UIProxy.Upstream)
	}
}
