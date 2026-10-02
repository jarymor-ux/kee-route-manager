package xray

import (
	"context"
	"github.com/jarymor-ux/kee-route-manager/internal/config"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommandOutputIsBoundedDuringConcurrentReads(t *testing.T) {
	out := &commandOutput{limit: 1024}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				payload := []byte(strings.Repeat("x", 2048))
				if n, err := out.Write(payload); n != len(payload) || err != nil {
					t.Errorf("short process write n=%d err=%v", n, err)
				}
				_ = out.String()
			}
		}()
	}
	wg.Wait()
	if len(out.String()) != 1024 {
		t.Fatal("process output exceeded memory cap")
	}
}
func TestValidationHonorsCommandTimeoutAndKillsChildren(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "fake-xray")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nsleep 5\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Xray.Binary = binary
	c.Platform.CommandTimeout = config.Dur(30 * time.Millisecond)
	m := NewManager(c, &recordingRunner{}, &fakePlatform{})
	start := time.Now()
	if err := m.validateDir(context.Background(), root); err == nil {
		t.Fatal("hanging validator succeeded")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("validator ignored bounded timeout: %s", elapsed)
	}
}
