package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/auth"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"github.com/jarymor-ux/kee-route-manager/internal/setup"
)

func TestPasswordCanStageCredentialsWithoutWritingConfiguredDestination(t *testing.T) {
	dir := t.TempDir()
	path := configFile(t, filepath.Join(dir, "control.sock"))
	destination := filepath.Join(dir, "destination.json")
	staged := filepath.Join(dir, "staged.json")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Web.CredentialsFile = destination
	if err := setup.WriteConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	input, err := os.CreateTemp(dir, "password")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err := input.WriteString("installation-only-password\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = input
	defer func() { os.Stdin = previous }()
	args := []string{"passwd", "--config", path, "--username", "admin", "--password-stdin", "--credentials-output", staged, "--expected-credentials-file", destination}
	if err := Run(context.Background(), args, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatal("configured destination written before transaction")
	}
	if _, err := auth.LoadCredentials(staged); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), args, "", "", ""); err == nil {
		t.Fatal("staged credentials overwritten")
	}
	after, _ := os.ReadFile(staged)
	if string(before) != string(after) {
		t.Fatal("existing credentials changed")
	}
	args[len(args)-1] = "/wrong/path"
	if err := Run(context.Background(), args, "", "", ""); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatal("credential path mismatch accepted", err)
	}
}
