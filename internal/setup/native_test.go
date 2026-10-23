package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestNativeInstallerStagesBlockedLoopbackConfiguration(t *testing.T) {
	for _, lang := range []string{"1", "2"} {
		t.Run(lang, func(t *testing.T) {
			dir := t.TempDir()
			core, native := filepath.Join(dir, "core.yaml"), filepath.Join(dir, "native")
			// Clean setup has fixed owned paths/tags, so no discovery questions.
			input := strings.Join([]string{lang, "https://sub.example.test/main", "Main", "y", "n", "n", "https://score.example.test/ping", "https://health.example.test/ping", "n", "", "", "n", "n", "", "", "y"}, "\n") + "\n"
			var out strings.Builder
			if err := InitNativeInstallerConfig(strings.NewReader(input), &out, core, "", "", native, PlatformKeenetic); err != nil {
				t.Fatal(err, out.String())
			}
			cfg, err := config.Load(core)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.Xray.Route.InboundTags, []string{"redirect", "tproxy"}) || !reflect.DeepEqual(cfg.Xray.Route.ReplaceOutboundTags, []string{"bootstrap-blocked"}) {
				t.Fatal("native routing was not pinned", cfg.Xray.Route)
			}
			if !reflect.DeepEqual(cfg.Platform.XrayRestartCommand, []string{"/opt/etc/kee-route-manager/xray-restart.sh"}) || cfg.Xray.AssetDir != "/opt/etc/xray/dat" {
				t.Fatal("native runtime paths differ from XKeen", cfg.Xray)
			}
			if cfg.Pool.Size != 2 || cfg.Benchmark.BatchSize != 2 || cfg.Benchmark.LatencyWorkers != 2 || cfg.Benchmark.Speed.Enabled || cfg.Update.AutoApply || !cfg.Update.Enabled || cfg.Update.Channel != "stable" {
				t.Fatal("unsafe clean-install defaults")
			}
			var in struct {
				Inbounds []struct {
					Listen string
					Tag    string
				}
			}
			data, err := os.ReadFile(filepath.Join(native, "configs", "03_inbounds.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &in); err != nil {
				t.Fatal(err)
			}
			if len(in.Inbounds) != 2 {
				t.Fatal("missing native inbounds")
			}
			for _, inbound := range in.Inbounds {
				if inbound.Listen != "127.0.0.1" {
					t.Fatal("non-loopback inbound")
				}
			}
			var outbound struct {
				Outbounds []struct {
					Tag      string
					Protocol string
				}
			}
			data, err = os.ReadFile(filepath.Join(native, "configs", "04_outbounds.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &outbound); err != nil {
				t.Fatal(err)
			}
			if len(outbound.Outbounds) != 1 || outbound.Outbounds[0].Protocol != "blackhole" || outbound.Outbounds[0].Tag != "bootstrap-blocked" {
				t.Fatal("initial route can leak direct")
			}
			for _, path := range []string{native, filepath.Join(native, "configs"), filepath.Join(native, "xkeen.json")} {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				want := os.FileMode(0600)
				if info.IsDir() {
					want = 0700
				}
				if info.Mode().Perm() != want {
					t.Fatal("public native staging", path)
				}
			}
			if strings.Contains(out.String(), "https://sub.example.test") {
				t.Fatal("subscription leaked into summary")
			}
		})
	}
}

func TestNativeInstallerRefusesExistingOutputBeforePrompt(t *testing.T) {
	dir := t.TempDir()
	native := filepath.Join(dir, "native")
	if err := os.Mkdir(native, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(native, "operator"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := InitNativeInstallerConfig(strings.NewReader(""), &out, filepath.Join(dir, "core"), "", "", native, PlatformKeenetic); err == nil {
		t.Fatal("existing native output accepted")
	}
	if out.Len() != 0 {
		t.Fatal("prompted before refusing existing output")
	}
	data, err := os.ReadFile(filepath.Join(native, "operator"))
	if err != nil || string(data) != "preserve" {
		t.Fatal("operator content changed")
	}
}

func TestNativeInstallerCancellationDoesNotCreateNativeFiles(t *testing.T) {
	dir := t.TempDir()
	native := filepath.Join(dir, "native")
	var out strings.Builder
	if err := InitNativeInstallerConfig(strings.NewReader("2\n"), &out, filepath.Join(dir, "core"), "", "", native, PlatformKeenetic); err == nil {
		t.Fatal("incomplete wizard succeeded")
	}
	if _, err := os.Lstat(native); !os.IsNotExist(err) {
		t.Fatal("native effects before confirmation")
	}
}
