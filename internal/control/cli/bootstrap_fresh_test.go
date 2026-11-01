package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestCLIInitConfigFreshXray(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "config.yaml")
	script := "2\nhttps://subscription.example.test/fresh\nFresh\n\nn\nn\nhttps://score.example.test/ping\nhttps://one.example.test/ping\nhttps://two.example.test/ping\nn\n\n\n\nn\nn\ny\n"
	var out strings.Builder
	args := []string{"init-config", "--fresh-xray", "--platform", "keenetic", "--output", output}
	if err := runWithIO(context.Background(), args, "test", "commit", "build", strings.NewReader(script), &out); err != nil {
		t.Fatal(err, out.String())
	}
	cfg, err := config.Load(output)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Xray.AssetDir != "/opt/etc/xray/dat" || len(cfg.Xray.Route.ReplaceOutboundTags) != 1 || cfg.Xray.Route.ReplaceOutboundTags[0] != "krm-bootstrap" {
		t.Fatal("fresh flag not applied")
	}
	if err := runWithIO(context.Background(), append(args, "--overwrite"), "test", "commit", "build", strings.NewReader(script), &out); err == nil {
		t.Fatal("fresh overwrite accepted")
	}
}

func TestCLIInitXrayCreatesPrivateBootstrapAndRefusesOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "configs")
	var out strings.Builder
	args := []string{"init-xray", "--output-dir", dir}
	if err := runWithIO(context.Background(), args, "test", "commit", "build", strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != dir+"\n" {
		t.Fatal("output must contain only the success path")
	}
	original, err := os.ReadFile(filepath.Join(dir, "05_routing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runWithIO(context.Background(), args, "test", "commit", "build", strings.NewReader(""), &out); err == nil {
		t.Fatal("existing bootstrap overwritten")
	}
	now, _ := os.ReadFile(filepath.Join(dir, "05_routing.json"))
	if string(now) != string(original) {
		t.Fatal("routing changed")
	}
	for _, bad := range [][]string{{"init-xray"}, {"init-xray", "--output-dir", ""}, {"init-xray", "--output-dir", filepath.Join(t.TempDir(), "configs"), "extra"}} {
		if err := runWithIO(context.Background(), bad, "test", "commit", "build", strings.NewReader(""), &out); err == nil {
			t.Fatalf("invalid arguments accepted: %v", bad)
		}
	}
}
