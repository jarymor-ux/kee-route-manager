package setup

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedInstallerTargetsFailBeforeWriting(t *testing.T) {
	for _, name := range []string{"openwrt", "linux-systemd"} {
		t.Run(name, func(t *testing.T) {
			platform := Platform(name)
			if _, err := BuildControllerConfig(validOptions(platform)); err == nil {
				t.Fatal("removed controller target accepted")
			}
			if _, err := BuildUIConfig(UIOptions{Platform: platform}); err == nil {
				t.Fatal("removed installer identity accepted")
			}
			dir := t.TempDir()
			core, ui, tls := filepath.Join(dir, "core.yaml"), filepath.Join(dir, "ui.yaml"), filepath.Join(dir, "tls")
			if err := InitConfigForPlatform(strings.NewReader(""), io.Discard, core, false, platform); err == nil {
				t.Fatal("removed wizard target accepted")
			}
			if err := InitLocalUIConfig(strings.NewReader(""), io.Discard, core, ui, tls, platform); err == nil {
				t.Fatal("removed local UI target accepted")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("refused target created files: %v %v", entries, err)
			}
		})
	}
}
