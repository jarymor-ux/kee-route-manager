package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpeedWorkersDefaultForLegacyConfig(t *testing.T) {
	if got := Default().Benchmark.Speed.Workers; got != 2 {
		t.Fatalf("default workers = %d, want 2", got)
	}
	if got := validConfig(t).Benchmark.Speed.Workers; got != 2 {
		t.Fatalf("omitted workers = %d, want 2", got)
	}
}

func TestSpeedWorkersBounds(t *testing.T) {
	for _, workers := range []int{-1, 0, 1, 2, 16, 17} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			c := validConfig(t)
			c.Benchmark.Speed.Workers = workers
			wantValid := workers >= 1 && workers <= 16
			if err := c.Validate(); (err == nil) != wantValid {
				t.Fatalf("workers=%d validation error=%v, want valid=%v", workers, err, wantValid)
			}
			body := strings.Replace(validYAML, "  speed:\n    enabled: false", fmt.Sprintf("  speed:\n    enabled: false\n    workers: %d", workers), 1)
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path)
			if (err == nil) != wantValid {
				t.Fatalf("workers=%d load error=%v, want valid=%v", workers, err, wantValid)
			}
			if err == nil && loaded.Benchmark.Speed.Workers != workers {
				t.Fatalf("loaded workers=%d, want %d", loaded.Benchmark.Speed.Workers, workers)
			}
		})
	}
}
