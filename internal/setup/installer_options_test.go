package setup

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/tlsutil"
)

func installerScript(lang, interval, cache string, localUIAnswers []string) string {
	lines := strings.Split(strings.TrimSuffix(wizardScript(lang, false, false, "y"), "\n"), "\n")
	// The common wizard fixture includes platform selection; installers pin it.
	lines = append(lines[:1], lines[2:]...)
	// New settings follow pool size, before speed/update choices.
	lines[15] = interval
	lines[16] = cache
	if localUIAnswers != nil {
		lines = append(append(lines[:len(lines)-1], localUIAnswers...), "y")
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestInteractiveInstallerCadenceAndCacheValidationInBothLanguages(t *testing.T) {
	for _, lang := range []string{"1", "2"} {
		for _, cache := range []struct {
			answer  string
			enabled bool
		}{{"", false}, {"y", true}, {"n", false}} {
			t.Run(lang+cache.answer, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "config.yaml")
				var out strings.Builder
				// Invalid durations are reprompted without silently applying runtime clamps.
				script := installerScript(lang, "bad-duration\n0s\n30s\n721h\n9999999999999999999h\n5m", "maybe\n"+cache.answer, nil)
				if err := InitConfigForPlatform(strings.NewReader(script), &out, path, false, PlatformKeenetic); err != nil {
					t.Fatal(err)
				}
				cfg, err := config.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Benchmark.FullInterval.Duration != 5*time.Minute || cfg.Subscriptions.CacheEnabled != cache.enabled {
					t.Fatal("new options not persisted")
				}
				messages := messagesEN
				if lang == "1" {
					messages = messagesRU
				}
				if !strings.Contains(out.String(), messages.InvalidBenchmarkInterval) || !strings.Contains(out.String(), messages.InvalidBool) || !strings.Contains(out.String(), messages.SummaryBenchmarkInterval+": 5m0s") {
					t.Fatal("localized validation/summary absent", out.String())
				}
			})
		}
	}
}
func TestInteractiveInstallerDefaultsDoNotChangeLegacyDefaults(t *testing.T) {
	if !config.Default().Subscriptions.CacheEnabled || config.Default().Benchmark.FullInterval.Duration != 6*time.Hour {
		t.Fatal("legacy defaults changed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	var out strings.Builder
	if err := InitConfigForPlatform(strings.NewReader(installerScript("2", "", "", nil)), &out, path, false, PlatformOpenWrt); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Benchmark.FullInterval.Duration != 6*time.Hour || cfg.Subscriptions.CacheEnabled {
		t.Fatal("incorrect new interactive defaults")
	}
	options := validOptions(PlatformOpenWrt)
	generated, err := BuildControllerConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	if !generated.Subscriptions.CacheEnabled || generated.Benchmark.FullInterval.Duration != 6*time.Hour {
		t.Fatal("programmatic/prepared defaults changed")
	}
	options.Benchmark.FullInterval = config.Dur(11 * time.Minute)
	cache := false
	options.SubscriptionCacheEnabled = &cache
	generated, err = BuildControllerConfig(options)
	if err != nil {
		t.Fatal(err)
	}
	if generated.Benchmark.FullInterval.Duration != 11*time.Minute || generated.Subscriptions.CacheEnabled {
		t.Fatal("custom prepared options ignored")
	}
}
func TestInteractiveInstallerIntervalBounds(t *testing.T) {
	for _, duration := range []time.Duration{time.Minute, 30 * 24 * time.Hour} {
		options := validOptions(PlatformKeenetic)
		options.Benchmark.FullInterval = config.Dur(duration)
		if _, err := BuildControllerConfig(options); err != nil {
			t.Fatal(err)
		}
	}
	for _, duration := range []time.Duration{-time.Second, 30 * time.Second, 30*24*time.Hour + time.Second} {
		options := validOptions(PlatformKeenetic)
		options.Benchmark.FullInterval = config.Dur(duration)
		if _, err := BuildControllerConfig(options); err == nil {
			t.Fatalf("accepted interval %s", duration)
		}
	}
}
func TestLocalUIWizardValidatesBindPortAndHostnameInBothLanguages(t *testing.T) {
	for _, lang := range []string{"1", "2"} {
		t.Run(lang, func(t *testing.T) {
			dir := t.TempDir()
			corePath, uiPath, tlsDir := filepath.Join(dir, "core.yaml"), filepath.Join(dir, "ui.yaml"), filepath.Join(dir, "tls")
			var out strings.Builder
			answers := []string{
				"0.0.0.0", "8.8.8.8", "::1", "hostname.invalid", "192.168.1.1",
				"invalid", "0", "65536", "9443", "443",
				"https://alice.jopa", "alice.jopa:443", "alice.jopa/path", "alice..jopa", "-alice.jopa", "alice.local", "alice.jopa",
			}
			if err := InitLocalUIConfig(strings.NewReader(installerScript(lang, "7m", "n", answers)), &out, corePath, uiPath, tlsDir, PlatformKeenetic); err != nil {
				t.Fatal(err)
			}
			core, err := config.Load(corePath)
			if err != nil {
				t.Fatal(err)
			}
			ui, err := config.Load(uiPath)
			if err != nil {
				t.Fatal(err)
			}
			if ui.Web.Listen != "192.168.1.1:443" || core.API.Listen != "127.0.0.1:9443" || core.Benchmark.FullInterval.Duration != 7*time.Minute || core.Subscriptions.CacheEnabled {
				t.Fatal("UI options modified controller ownership or benchmark settings")
			}
			if ui.UIProxy.Upstream != "https://127.0.0.1:9443" || ui.UIProxy.InsecureTLS || !ui.Web.TLS.AutoGenerate || !containsSetupHost(ui.Web.TLS.Hosts, "alice.jopa") {
				t.Fatal("UI TLS trust or hostname SAN missing")
			}
			messages := messagesEN
			if lang == "1" {
				messages = messagesRU
			}
			for _, message := range []string{messages.InvalidUIBind, messages.InvalidUIPort, messages.InvalidUIHostname, "https://alice.jopa", "alice.jopa → 192.168.1.1"} {
				if !strings.Contains(out.String(), message) {
					t.Fatalf("missing prompt/summary %q", message)
				}
			}
			// Generate exactly the configured UI identity in an isolated test directory.
			uiTLS := ui.Web.TLS
			uiTLS.CertFile = filepath.Join(dir, "ui.crt")
			uiTLS.KeyFile = filepath.Join(dir, "ui.key")
			if err := tlsutil.EnsureTLS(uiTLS, ui.Web.Listen); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(uiTLS.CertFile)
			if err != nil {
				t.Fatal(err)
			}
			block, _ := pem.Decode(raw)
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			if cert.VerifyHostname("alice.jopa") != nil || cert.VerifyHostname("192.168.1.1") != nil {
				t.Fatal("generated UI certificate does not cover panel address")
			}
		})
	}
}
func containsSetupHost(hosts []string, want string) bool {
	for _, host := range hosts {
		if host == want {
			return true
		}
	}
	return false
}
func TestPanelHostnameValidationAndSecureUIDefault(t *testing.T) {
	for _, name := range []string{"alice.jopa", "alice.home.arpa", "Alice.Example.RU", "xn--e1afmkfd.xn--p1ai"} {
		if !validPanelHostname(name) {
			t.Fatalf("valid hostname rejected: %q", name)
		}
	}
	for _, name := range []string{"", "localhost", "192.168.1.1", "alice.local", "alice.LOCAL", "https://alice.jopa", "alice.jopa:443", "alice.jopa/", "alice..jopa", "alice_.jopa", ".alice.jopa", "alice.jopa.", "-alice.jopa", "alice.jopa-", strings.Repeat("a", 64) + ".jopa"} {
		if validPanelHostname(name) {
			t.Fatalf("invalid hostname accepted: %q", name)
		}
	}
	ui, err := BuildUIConfig(UIOptions{Platform: PlatformLinuxSystemd, Hostname: "Alice.Jopa"})
	if err != nil {
		t.Fatal(err)
	}
	if ui.Web.Listen != "127.0.0.1:9444" || !containsSetupHost(ui.Web.TLS.Hosts, "alice.jopa") {
		t.Fatal("unsafe UI defaults")
	}
	if _, err = BuildUIConfig(UIOptions{Platform: PlatformKeenetic, Hostname: "https://alice.jopa"}); err == nil {
		t.Fatal("programmatic builder accepted invalid hostname")
	}
	// Explicit prepared bindings retain their configured deployment behavior.
	ui, err = BuildUIConfig(UIOptions{Platform: PlatformOpenWrt, Listen: "192.168.88.1:18443"})
	if err != nil || ui.Web.Listen != "192.168.88.1:18443" {
		t.Fatal("prepared listen ignored", err)
	}
}

func TestLocalUIWizardLeavesNoPairOnCoreWriteFailure(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "missing-parent", "core.yaml")
	uiPath := filepath.Join(dir, "ui.yaml")
	tlsDir := filepath.Join(dir, "tls")
	var out strings.Builder
	err := InitLocalUIConfig(strings.NewReader(installerScript("2", "", "", []string{"", "", ""})), &out, corePath, uiPath, tlsDir, PlatformOpenWrt)
	if err == nil {
		t.Fatal("unwritable core destination accepted")
	}
	for _, path := range []string{corePath, uiPath, tlsDir} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("partial pair retained: %s", path)
		}
	}
}
func TestLocalUIWizardDeclineAndTLSFailurePreserveOtherFiles(t *testing.T) {
	for _, scenario := range []string{"decline", "tls-parent-missing", "ui-parent-missing"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			corePath, uiPath, tlsDir := filepath.Join(dir, "core.yaml"), filepath.Join(dir, "ui.yaml"), filepath.Join(dir, "tls")
			marker := filepath.Join(dir, "existing-other-config")
			if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			script := installerScript("2", "", "", []string{"", "", ""})
			switch scenario {
			case "decline":
				script = strings.TrimSuffix(script, "y\n") + "n\n"
			case "tls-parent-missing":
				tlsDir = filepath.Join(dir, "missing-parent", "tls")
			case "ui-parent-missing":
				uiPath = filepath.Join(dir, "missing-parent", "ui.yaml")
			}
			var out strings.Builder
			if err := InitLocalUIConfig(strings.NewReader(script), &out, corePath, uiPath, tlsDir, PlatformKeenetic); err == nil {
				t.Fatal("failed setup unexpectedly succeeded")
			}
			for _, path := range []string{corePath, uiPath, tlsDir} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("partial pair retained: %s", path)
				}
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "preserve" {
				t.Fatal("unrelated configuration changed")
			}
		})
	}
}
