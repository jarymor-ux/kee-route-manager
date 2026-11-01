package setup

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

type freshOutputRaceReader struct {
	io.Reader
	path    string
	created bool
}

func (r *freshOutputRaceReader) Read(p []byte) (int, error) {
	if !r.created {
		r.created = true
		if err := os.WriteFile(r.path, []byte("preserve concurrent output"), 0600); err != nil {
			return 0, err
		}
	}
	return r.Reader.Read(p)
}

func TestFreshInstallerDoesNotOfferOverwriteForConcurrentOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "config.yaml")
	// The extra yes would authorize the legacy overwrite prompt; fresh mode must
	// refuse a file created after its initial guard instead of asking that prompt.
	in := &freshOutputRaceReader{Reader: strings.NewReader(freshInstallerScript("2", false) + "y\n"), path: output}
	var out strings.Builder
	if err := InitFreshInstallerConfig(in, &out, output, "", "", PlatformKeenetic); err == nil {
		t.Fatal("fresh installer overwrote concurrently created output")
	}
	raw, err := os.ReadFile(output)
	if err != nil || string(raw) != "preserve concurrent output" {
		t.Fatal("concurrent output changed", err)
	}
	if strings.Contains(out.String(), messagesEN.Overwrite) {
		t.Fatal("fresh installer offered overwrite")
	}
}

func TestFreshInstallerInsufficientHealthTargetsWritesNothing(t *testing.T) {
	output := filepath.Join(t.TempDir(), "config.yaml")
	script := "2\nhttps://subscription.example.test/fresh\nFresh\n\nn\nn\nhttps://score.example.test/ping\nhttps://one.example.test/ping\n"
	if err := InitFreshInstallerConfig(strings.NewReader(script), &strings.Builder{}, output, "", "", PlatformKeenetic); err == nil {
		t.Fatal("one health hostname accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("insufficient targets wrote configuration", err)
	}
}

func freshInstallerScript(lang string, localUI bool) string {
	answers := []string{lang, "https://subscription.example.test/fresh", "Fresh", "", "n", "n", "https://score.example.test/ping", "https://one.example.test/ping", "https://ONE.example.test/duplicate", "https://two.example.test/ping", "n", "", "", "", "n", "n"}
	if localUI {
		answers = append(answers, "192.168.1.1", "9444", "krm.home.arpa")
	}
	return strings.Join(append(answers, "y"), "\n") + "\n"
}

func TestFreshInstallerPinsGeneratedXrayAndRequiresIndependentHealthHosts(t *testing.T) {
	for _, lang := range []string{"1", "2"} {
		for _, localUI := range []bool{false, true} {
			t.Run(lang+map[bool]string{false: "core", true: "ui"}[localUI], func(t *testing.T) {
				dir := t.TempDir()
				output, ui, tls := filepath.Join(dir, "config.yaml"), "", ""
				if localUI {
					ui, tls = filepath.Join(dir, "ui.yaml"), filepath.Join(dir, "tls")
				}
				var out strings.Builder
				if err := InitFreshInstallerConfig(strings.NewReader(freshInstallerScript(lang, localUI)), &out, output, ui, tls, PlatformKeenetic); err != nil {
					t.Fatal(err, out.String())
				}
				cfg, err := config.Load(output)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Xray.Binary != "/opt/sbin/xray" || cfg.Xray.AssetDir != "/opt/etc/xray/dat" || cfg.Xray.ConfigDir != "/opt/etc/xray/configs" || cfg.Xray.ManagedDir != cfg.Xray.ConfigDir || cfg.Xray.BaseRoutingFile != cfg.Xray.ConfigDir+"/05_routing.json" || strings.Join(cfg.Xray.Route.InboundTags, ",") != "redirect,tproxy" || strings.Join(cfg.Xray.Route.ReplaceOutboundTags, ",") != "krm-bootstrap" {
					t.Fatalf("wrong fresh Xray configuration: %#v", cfg.Xray)
				}
				wantRestart := []string{"/bin/sh", "-c", "XKEEN_FOREGROUND=1 exec /opt/sbin/xkeen -restart </dev/null"}
				if !reflect.DeepEqual(cfg.Platform.XrayRestartCommand, wantRestart) {
					t.Fatalf("fresh Xray restart must wait for foreground XKeen: got %#v, want %#v", cfg.Platform.XrayRestartCommand, wantRestart)
				}
				targets := cfg.TargetsByRole("health")
				if len(targets) != 2 || targets[0].URL != "https://one.example.test/ping" || targets[1].URL != "https://two.example.test/ping" {
					t.Fatalf("independent health targets not enforced: %#v", targets)
				}
				if cfg.Update.AutoApply || cfg.Benchmark.Speed.Enabled {
					t.Fatal("unsafe defaults")
				}
				if strings.Contains(out.String(), messagesEN.XrayBinaryPrompt+" [") || strings.Contains(out.String(), messagesRU.XrayBinaryPrompt+" [") {
					t.Fatal("fresh install still asks manual Xray paths")
				}
				if localUI {
					uiCfg, err := config.Load(ui)
					if err != nil {
						t.Fatal(err)
					}
					if uiCfg.Web.Listen != "192.168.1.1:9444" || uiCfg.UIProxy.Upstream != "https://127.0.0.1:9443" || !cfg.API.TLS.Enabled {
						t.Fatal("local UI trust/listen not preserved")
					}
				}
			})
		}
	}
}

func TestLegacyInstallerKeepsRestartCommand(t *testing.T) {
	output := filepath.Join(t.TempDir(), "config.yaml")
	if err := InitConfigForPlatform(strings.NewReader(installerScript("2", "", "", nil)), &strings.Builder{}, output, false, PlatformKeenetic); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(output)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Platform.XrayRestartCommand, []string{"/opt/sbin/xkeen", "-restart"}) {
		t.Fatalf("legacy restart command changed: %#v", cfg.Platform.XrayRestartCommand)
	}
}

func TestFreshInstallerRejectsUnsupportedAndExistingOutputsBeforeReading(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "config.yaml")
	for _, platform := range []Platform{"", "linux", "openwrt"} {
		if err := InitFreshInstallerConfig(strings.NewReader(""), &strings.Builder{}, output, "", "", platform); err == nil {
			t.Fatalf("unsupported platform %q accepted", platform)
		}
	}
	if err := os.WriteFile(output, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InitFreshInstallerConfig(strings.NewReader(freshInstallerScript("2", false)), &strings.Builder{}, output, "", "", PlatformKeenetic); err == nil {
		t.Fatal("existing output accepted")
	}
	if raw, _ := os.ReadFile(output); string(raw) != "preserve" {
		t.Fatal("existing output changed")
	}
	if err := InitFreshInstallerConfig(strings.NewReader(""), &strings.Builder{}, filepath.Join(dir, "new.yaml"), filepath.Join(dir, "ui.yaml"), "", PlatformKeenetic); err == nil {
		t.Fatal("incomplete UI pair accepted")
	}
}
