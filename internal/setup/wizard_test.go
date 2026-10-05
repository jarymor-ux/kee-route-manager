package setup

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func wizardScript(lang string, speed, updates bool, final string) string {
	lines := []string{
		lang,
		"1",
		"",
		"",
		"",
		"",
		"proxy-main",
		"https://sub.example.test/main",
		"Main subscription",
		"",
		"n",
		"n",
		"https://score.example.test/ping",
		"https://health.example.test/ping",
		"n",
		"",
	}
	if speed {
		lines = append(lines, "y", "https://speed.example.test/download?bytes={bytes}")
	} else {
		lines = append(lines, "n")
	}
	if updates {
		lines = append(lines, "y", "")
	} else {
		lines = append(lines, "n")
	}
	lines = append(lines, final)
	return strings.Join(lines, "\n") + "\n"
}

func runWizardToFile(t *testing.T, input string) (config.Config, string, string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	var out strings.Builder
	err := InitConfig(strings.NewReader(input), &out, path, false)
	if err != nil {
		return config.Config{}, out.String(), path, err
	}
	cfg, loadErr := config.Load(path)
	return cfg, out.String(), path, loadErr
}

func TestInitConfigRussianScenarioProducesValidConfig(t *testing.T) {
	cfg, out, _, err := runWizardToFile(t, wizardScript("1", false, true, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Platform.Kind != "keenetic" || cfg.Pool.Size != 5 || cfg.Update.Channel != "rc" || cfg.Update.AutoApply {
		t.Fatalf("unexpected generated config: platform=%q pool=%d update=%#v", cfg.Platform.Kind, cfg.Pool.Size, cfg.Update)
	}
	if !strings.Contains(out, "Выберите язык") || !strings.Contains(out, "Создать конфигурацию?") {
		t.Fatalf("Russian wizard output missing expected prompts:\n%s", out)
	}
}

func TestInitConfigEnglishScenario(t *testing.T) {
	cfg, out, _, err := runWizardToFile(t, wizardScript("2", false, false, "y"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Platform.Kind != "keenetic" || cfg.Update.Enabled {
		t.Fatalf("unexpected generated config: %#v", cfg.Update)
	}
	if !strings.Contains(out, "Choose language") || !strings.Contains(out, "Create configuration?") {
		t.Fatalf("English wizard output missing expected prompts:\n%s", out)
	}
}

func TestRussianAndEnglishAnswersProduceEquivalentConfig(t *testing.T) {
	ru, _, _, err := runWizardToFile(t, wizardScript("1", true, true, "y"))
	if err != nil {
		t.Fatal(err)
	}
	en, _, _, err := runWizardToFile(t, wizardScript("2", true, true, "y"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ru, en) {
		t.Fatalf("RU and EN configs differ\nRU=%#v\nEN=%#v", ru, en)
	}
}

func TestInvalidLanguageChoiceIsRejectedAndReprompted(t *testing.T) {
	input := "9\n" + wizardScript("1", false, false, "y")
	_, out, _, err := runWizardToFile(t, input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1. Русский") || !strings.Contains(out, "2. English") {
		t.Fatalf("language choices missing:\n%s", out)
	}
}

func TestInvalidURLIsRejectedAndReprompted(t *testing.T) {
	script := wizardScript("2", false, false, "y")
	script = strings.Replace(script, "https://sub.example.test/main\n", "not-a-url\nhttps://sub.example.test/main\n", 1)
	cfg, out, _, err := runWizardToFile(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Subscriptions.Sources) != 1 || !strings.Contains(out, "valid http(s) URL") {
		t.Fatalf("URL was not reprompted: output=%s", out)
	}
}

func TestEmptyRequiredInputIsRejected(t *testing.T) {
	script := wizardScript("2", false, false, "y")
	script = strings.Replace(script, "proxy-main\n", "\nproxy-main\n", 1)
	_, out, _, err := runWizardToFile(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "required") {
		t.Fatalf("required input error missing:\n%s", out)
	}
}

func TestDefaultsAreAppliedOnEnter(t *testing.T) {
	cfg, _, _, err := runWizardToFile(t, wizardScript("2", false, false, "y"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pool.Size != 5 || cfg.Xray.Binary != "/opt/sbin/xray" || cfg.Xray.ConfigDir != "/opt/etc/xray/configs" || cfg.Xray.BaseRoutingFile != "/opt/etc/xray/configs/05_routing.json" {
		t.Fatalf("defaults not applied: pool=%d xray=%#v", cfg.Pool.Size, cfg.Xray)
	}
}

func TestCustomConfigDirDefaultsBaseRoutingInsideIt(t *testing.T) {
	script := wizardScript("2", false, false, "y")
	script = strings.Replace(
		script,
		"1\n\n\n\n\nproxy-main\n",
		"1\n\n/custom/xray/configs\n\n\nproxy-main\n",
		1,
	)
	cfg, _, _, err := runWizardToFile(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Xray.ConfigDir != "/custom/xray/configs" {
		t.Fatalf("config dir=%q", cfg.Xray.ConfigDir)
	}
	if cfg.Xray.BaseRoutingFile != "/custom/xray/configs/05_routing.json" {
		t.Fatalf("base routing file=%q", cfg.Xray.BaseRoutingFile)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("generated config is invalid: %v", err)
	}
}

func TestWizardEOF(t *testing.T) {
	var out strings.Builder
	err := InitConfig(strings.NewReader(""), &out, filepath.Join(t.TempDir(), "config.yaml"), false)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestWizardCancellation(t *testing.T) {
	var out strings.Builder
	err := InitConfig(strings.NewReader("1\ncancel\n"), &out, filepath.Join(t.TempDir(), "config.yaml"), false)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestFinalConfirmationCanDecline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	var out strings.Builder
	err := InitConfig(strings.NewReader(wizardScript("2", false, false, "n")), &out, path, false)
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("expected final refusal, got %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("config unexpectedly created: %v", statErr)
	}
}

func TestExistingOutputFileIsNotSilentlyOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	input := wizardScript("2", false, false, "y") + "n\n"
	err := InitConfig(strings.NewReader(input), &out, path, false)
	if !errors.Is(err, ErrNotConfirmed) {
		t.Fatalf("expected overwrite refusal, got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original\n" {
		t.Fatalf("existing file changed: %q", got)
	}
}

func TestWriteConfigWithoutOverwriteRefusesExistingFile(t *testing.T) {
	cfg, _, _, err := runWizardToFile(t, wizardScript("2", false, false, "y"))
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err = writeConfig(path, cfg, false)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected os.ErrExist, got %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original\n" {
		t.Fatalf("existing file changed: %q", got)
	}
}

func TestInvalidSubscriptionHeaderNameIsRejectedAndReprompted(t *testing.T) {
	script := wizardScript("2", false, false, "y")
	script = strings.Replace(
		script,
		"n\nn\nhttps://score.example.test/ping\n",
		"y\nBad Header\nAuthorization\nBearer test\nn\nn\nhttps://score.example.test/ping\n",
		1,
	)
	cfg, out, _, err := runWizardToFile(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "valid HTTP header name") {
		t.Fatalf("invalid header name was not rejected:\n%s", out)
	}
	if cfg.Subscriptions.Sources[0].Headers["Authorization"] != "Bearer test" {
		t.Fatalf("valid header was not saved: %#v", cfg.Subscriptions.Sources[0].Headers)
	}
}

func TestMultipleSubscriptions(t *testing.T) {
	script := wizardScript("2", false, false, "y")
	old := "n\nhttps://score.example.test/ping\n"
	repl := "y\nhttps://sub.example.test/backup\nBackup\ny\nn\nn\nhttps://score.example.test/ping\n"
	script = strings.Replace(script, old, repl, 1)
	cfg, _, _, err := runWizardToFile(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Subscriptions.Sources) != 2 {
		t.Fatalf("subscriptions=%d", len(cfg.Subscriptions.Sources))
	}
}

func TestMultipleHealthTargets(t *testing.T) {
	script := wizardScript("2", false, false, "y")
	old := "https://health.example.test/ping\nn\n"
	repl := "https://health.example.test/ping\ny\nhttps://health2.example.test/ping\nn\n"
	script = strings.Replace(script, old, repl, 1)
	cfg, _, _, err := runWizardToFile(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.TargetsByRole("health")); got != 2 {
		t.Fatalf("health targets=%d", got)
	}
}

func TestSpeedTestDisabledByDefault(t *testing.T) {
	cfg, _, _, err := runWizardToFile(t, wizardScript("2", false, false, "y"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Benchmark.Speed.Enabled {
		t.Fatal("speed test unexpectedly enabled")
	}
}

func TestSpeedTestEnabled(t *testing.T) {
	cfg, _, _, err := runWizardToFile(t, wizardScript("2", true, false, "y"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Benchmark.Speed.Enabled || cfg.Benchmark.Speed.URLTemplate != "https://speed.example.test/download?bytes={bytes}" {
		t.Fatalf("unexpected speed config: %#v", cfg.Benchmark.Speed)
	}
}

func TestSpeedURLRequiresBytesPlaceholder(t *testing.T) {
	script := wizardScript("2", true, false, "y")
	script = strings.Replace(script, "https://speed.example.test/download?bytes={bytes}\n", "https://speed.example.test/download\nhttps://speed.example.test/download?bytes={bytes}\n", 1)
	_, out, _, err := runWizardToFile(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "{bytes}") {
		t.Fatalf("missing placeholder validation message:\n%s", out)
	}
}

func TestSubscriptionSecretsAreNotPrintedInSummary(t *testing.T) {
	secret := "Bearer SYNTHETIC-WIZARD-SECRET"
	script := wizardScript("2", false, false, "y")
	script = strings.Replace(script, "n\nn\nhttps://score.example.test/ping\n", "y\nAuthorization\n"+secret+"\nn\nn\nhttps://score.example.test/ping\n", 1)
	cfg, out, _, err := runWizardToFile(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("secret leaked to wizard output:\n%s", out)
	}
	if cfg.Subscriptions.Sources[0].Headers["Authorization"] != secret {
		t.Fatal("secret header was not saved")
	}
}

func TestGeneratedConfigPassesLoadAndValidate(t *testing.T) {
	cfg, _, path, err := runWizardToFile(t, wizardScript("2", true, true, "y"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, loaded) {
		t.Fatal("config changed across load/validate")
	}
}

func TestSpeedURLRequiresHTTPSAndReprompts(t *testing.T) {
	script := wizardScript("2", true, false, "y")
	script = strings.Replace(script,
		"https://speed.example.test/download?bytes={bytes}\n",
		"http://speed.example.test/download?bytes={bytes}\nhttps://speed.example.test/download?bytes={bytes}\n",
		1,
	)
	cfg, out, _, err := runWizardToFile(t, script)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Benchmark.Speed.URLTemplate != "https://speed.example.test/download?bytes={bytes}" {
		t.Fatalf("unexpected speed URL: %q", cfg.Benchmark.Speed.URLTemplate)
	}
	if !strings.Contains(out, "HTTPS") {
		t.Fatalf("HTTP speed URL was not rejected by wizard:\n%s", out)
	}
}

func TestWizardURLValidationMatchesConfigSecurityRules(t *testing.T) {
	for _, raw := range []string{
		"https://user:pass@example.test/path",
		"https://example.test/path#fragment",
	} {
		if validHTTPURL(raw) {
			t.Fatalf("wizard accepted URL rejected by config validation: %q", raw)
		}
	}
	if !validHTTPURL("http://example.test/path?token=value") || !validHTTPURL("https://example.test/path") {
		t.Fatal("wizard rejected a valid HTTP(S) URL")
	}
}

func TestRussianWizardUsesLocalizedPromptsAndSummary(t *testing.T) {
	_, out, _, err := runWizardToFile(t, wizardScript("1", false, true, "д"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Название подписки",
		"Включить подписку?",
		"Добавить HTTP-заголовок?",
		"Размер пула [5]",
		"Включить проверку скорости?",
		"Включить обновления?",
		"Подписки:",
		"Размер пула:",
		"Обновления:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Russian localization missing %q:\n%s", want, out)
		}
	}
}

func TestSummaryShowsKeyNonSecretConfiguration(t *testing.T) {
	_, out, _, err := runWizardToFile(t, wizardScript("2", false, false, "y"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"/opt/sbin/xray",
		"/opt/etc/xray/configs",
		"/opt/etc/xray/configs/05_routing.json",
		"redirect, tproxy",
		"proxy-main",
		"Main subscription",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestTranslationParity(t *testing.T) {
	rv, ev := reflect.ValueOf(messagesRU), reflect.ValueOf(messagesEN)
	rt := rv.Type()
	if rt != ev.Type() {
		t.Fatalf("translation types differ: %v vs %v", rt, ev.Type())
	}
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if rv.Field(i).String() == "" || ev.Field(i).String() == "" {
			t.Fatalf("translation key %s missing in RU or EN", name)
		}
	}
}
